package marketplace

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/LukasHirt/extctl/internal/config"
)

// VerifyMinOCIS empirically determines minOCIS for a staged submission by
// actually running its e2e tests against real owncloud/ocis Docker images,
// instead of trusting a carried-forward or history-inferred guess (see
// ResolveMinOCIS's doc comment on why those are heuristics, not verified
// compatibility checks). idArg is "<app-id>" if exactly one version is
// staged for it, or "<app-id>@<version>" to disambiguate — same convention
// as RetryScreenshots/Approve.
//
// The search starts from the submission's current minOCIS (if any) as a
// seed: BisectMinOCIS checks that version FIRST, and only falls through to
// a full bisection over every available oCIS image if the seed no longer
// passes — the common case across an extension's own release history is
// that its floor hasn't moved, so most calls cost exactly one Docker
// bring-up/teardown cycle, not O(log n) of them.
//
// Serialized via withE2ELock, same as the gate's own e2e stage and
// screenshot capture, since a second concurrent Docker Compose stack in the
// same checkout would collide on the shared oCIS instance.
func VerifyMinOCIS(cfg *config.Config, idArg string, w io.Writer) (string, error) {
	printf := func(format string, a ...any) { _, _ = fmt.Fprintf(w, format, a...) }
	checkout := cfg.MarketplaceRepo.Checkout

	appID, version, branch, err := ResolvePendingBranch(checkout, idArg)
	if err != nil {
		return "", err
	}
	// -f (force): checkout is documented as always safe to hard-reset (see
	// EnsureCheckout) — it holds no real developer work. A plain checkout
	// can still refuse here once git-lfs filtering is active locally and
	// checkout's tree has any of the pre-#238 files main still carries
	// (raw blobs where .gitattributes now says LFS pointer): git recomputes
	// those as "locally modified" against the filtered content it would now
	// produce, and blocks the branch switch on files this operation never
	// touched or cared about.
	if err := runGit(checkout, "checkout", "-f", branch); err != nil {
		return "", fmt.Errorf("checkout %s: %w", branch, err)
	}

	relDir := filepath.Join("extensions", appID, "releases", version)
	extPath := filepath.Join(checkout, relDir, "extension.yaml")
	ext, err := readExtensionYAMLFile(extPath)
	if err != nil {
		return "", err
	}

	bundlePath := filepath.Join(checkout, relDir, "bundle.zip")
	if _, err := os.Stat(bundlePath); err != nil {
		return "", fmt.Errorf("staged bundle.zip not found at %s: %w", bundlePath, err)
	}

	tag := appID + "-v" + version
	result, ok, err := verifyMinOCISBundle(cfg, appID, tag, bundlePath, ext.MinOCIS, printf)
	if err != nil {
		return "", fmt.Errorf("bisect minOCIS: %w", err)
	}
	if !ok {
		return "", fmt.Errorf("%s@%s failed its e2e tests against every available oCIS image — the extension itself needs attention before any minOCIS value can be trusted",
			appID, version)
	}

	if result == ext.MinOCIS {
		printf("minOCIS confirmed: %s (already correct, no change)\n", result)
		return result, nil
	}

	if err := AmendSubmissionMinOCIS(checkout, appID, version, result); err != nil {
		return "", fmt.Errorf("amend submission: %w", err)
	}
	if ext.MinOCIS == "" {
		printf("minOCIS set: %s (e2e-verified)\n", result)
	} else {
		printf("minOCIS updated: %s -> %s (e2e-verified)\n", ext.MinOCIS, result)
	}
	return result, nil
}

func passFailLabel(passed bool) string {
	if passed {
		return "PASS"
	}
	return "FAIL"
}

// verifyMinOCISDuringStaging runs the e2e bisection automatically as part
// of stageOne, seeded from the heuristic guess ResolveMinOCIS already
// picked (minOCIS/minOCISSource). Best-effort, matching every other heavy
// step in the staging path (screenshot capture): a Docker/infra failure —
// or the extension simply failing its own e2e tests against every
// available oCIS image — is logged and staging falls back to the
// unverified heuristic value rather than aborting the whole submission.
// Requiring Docker to succeed here would make an otherwise fast, reliable
// command newly fragile for what is, for most extensions most of the time,
// just a confirmation of a value that was already right.
//
// On success, the verified value is also recorded into metadata
// (runMetadataCache.recordMinOCIS) so a LATER version of the same
// extension staged in this same batch seeds its own verification from the
// now-confirmed value instead of the original unverified guess.
func verifyMinOCISDuringStaging(cfg *config.Config, r Result, metadata *runMetadataCache, bundlePath, minOCIS string, minOCISSource MinOCISSource, printf func(string, ...any)) (string, MinOCISSource) {
	result, ok, err := verifyMinOCISBundle(cfg, r.AppID, r.Tag, bundlePath, minOCIS, printf)
	switch {
	case err != nil:
		printf("  warning: could not e2e-verify minOCIS: %v — keeping unverified value %q\n", err, minOCIS)
		return minOCIS, minOCISSource
	case !ok:
		printf("  warning: %s@%s failed its e2e tests against every available oCIS image — keeping unverified value %q; the extension itself may need attention\n", r.AppID, r.Version, minOCIS)
		return minOCIS, minOCISSource
	case result == minOCIS && minOCISSource == MinOCISSourceE2EVerified:
		return minOCIS, minOCISSource // already verified this run via a sibling version
	case minOCIS == "":
		printf("  minOCIS: %s (e2e-verified)\n", result)
	case result != minOCIS:
		printf("  minOCIS: %s (e2e-verified — was %s)\n", result, minOCIS)
	default:
		printf("  minOCIS: %s (confirmed by e2e tests)\n", result)
	}
	metadata.recordMinOCIS(r.AppID, result, MinOCISSourceE2EVerified)
	return result, MinOCISSourceE2EVerified
}

// verifyMinOCISBundle is the e2e-bisection core shared by VerifyMinOCIS
// (re-verifying an already-staged submission) and stageOne's automatic
// verification pass (verifying before the first commit is ever made):
// fetches the candidate oCIS version list, then runs BisectMinOCIS under
// withE2ELock — serialized, same as the gate's own e2e stage and screenshot
// capture, since a second concurrent Docker Compose stack in the same
// checkout would collide on the shared oCIS instance.
func verifyMinOCISBundle(cfg *config.Config, appID, tag, bundlePath, seed string, printf func(string, ...any)) (result string, ok bool, err error) {
	versions, err := AvailableOCISImageVersions()
	if err != nil {
		return "", false, fmt.Errorf("list available oCIS image versions: %w", err)
	}

	lockErr := withE2ELock(func() error {
		result, ok, err = BisectMinOCIS(versions, seed, func(ocisVersion string) (bool, error) {
			printf("  checking %s against oCIS %s...\n", tag, ocisVersion)
			passed, cerr := checkExtensionAgainstOCIS(cfg, appID, tag, bundlePath, ocisVersion, printf)
			if cerr != nil {
				return false, cerr
			}
			printf("    %s\n", passFailLabel(passed))
			return passed, nil
		})
		return err
	})
	if lockErr != nil {
		return "", false, lockErr
	}
	return result, ok, nil
}

// checkExtensionAgainstOCIS pins packages/web-app-<appID> in
// cfg.TargetRepo.Checkout to tag's exact source tree, stages the
// already-downloaded bundlePath as its dist/ (same PreparePlaywrightRun
// screenshot capture uses), brings up a fresh oCIS stack pinned to
// ocisVersion, and runs the extension's own e2e Playwright suite against
// it — restricted to ONE Playwright project (ProjectForScreenshots, same
// config screenshot capture already uses), not the full chrome/firefox/
// webkit matrix. Confirmed the hard way: running all three unfiltered
// reported a definitive FAIL against every single oCIS version tested,
// chrome included, even though chrome alone (screenshot capture's own
// project) is what actually produces this repo's existing screenshots —
// one of the other two projects was failing to launch for reasons that
// have nothing to do with oCIS version compatibility, and Playwright
// reports the whole run as failed if any project fails.
//
// A single failure is retried once before being reported as a definitive
// fail — a freshly-provisioned oCIS is the same class of possibly-flaky
// environment waitForOCIS's doc comment describes (auth backend not fully
// warmed up despite passing /health/live), and BisectMinOCIS itself does
// not retry, so any flakiness has to be absorbed here.
func checkExtensionAgainstOCIS(cfg *config.Config, appID, tag, bundlePath, ocisVersion string, printf func(string, ...any)) (bool, error) {
	mainCheckout := cfg.TargetRepo.Checkout

	restore, err := pinExtensionAndSupportToRelease(mainCheckout, appID, tag)
	defer func() { _ = restore() }()
	if err != nil {
		return false, fmt.Errorf("pin source to %s: %w", tag, err)
	}

	if err := PreparePlaywrightRun(mainCheckout, appID, bundlePath); err != nil {
		return false, fmt.Errorf("stage dist: %w", err)
	}

	image := "owncloud/ocis:" + ocisVersion
	if err := freshOCISUpWithImage(mainCheckout, image); err != nil {
		return false, fmt.Errorf("bring up oCIS %s: %w", image, err)
	}
	defer func() {
		downCmd := exec.Command("docker", "compose", "down")
		downCmd.Dir = mainCheckout
		_, _ = downCmd.CombinedOutput()
	}()

	if err := waitForOCIS(); err != nil {
		return false, fmt.Errorf("oCIS %s never became reachable: %w", image, err)
	}

	extDir := filepath.Join(mainCheckout, "packages", "web-app-"+appID)
	if err := clearStaleAuthCache(extDir); err != nil {
		return false, err
	}

	// --no-frozen-lockfile: pinExtensionSourceToRelease moved this
	// package's package.json back to an old release's dependency
	// versions, which the current, unpinned root pnpm-lock.yaml won't
	// match for any release but the latest — same reasoning as
	// prepareOCISForCapture's identical install step.
	installCmd := exec.Command("pnpm", "install", "--no-frozen-lockfile")
	installCmd.Dir = extDir
	installCmd.Env = append(os.Environ(), "CI=true")
	if out, err := installCmd.CombinedOutput(); err != nil {
		return false, fmt.Errorf("pnpm install: %w\n%s", err, strings.TrimSpace(string(out)))
	}

	project := ProjectForScreenshots(cfg, appID)
	e2eErr := runExtensionE2E(mainCheckout, appID, project)
	if e2eErr != nil {
		e2eErr = runExtensionE2E(mainCheckout, appID, project) // retry once, see doc comment
	}
	if e2eErr != nil {
		// Not returned as a bisection-aborting error — a failing e2e run
		// is exactly the "not compatible with this version" signal
		// BisectMinOCIS expects — but logged so a FAIL in the printed
		// bisection trace is diagnosable instead of silently opaque (see
		// this function's doc comment for why that mattered here).
		printf("    (%v)\n", e2eErr)
		return false, nil
	}
	return true, nil
}

// runExtensionE2E runs the extension's own Playwright e2e suite —
// whatever spec(s) tests/e2e/ contains — against whatever oCIS stack is
// currently up, restricted to project (see checkExtensionAgainstOCIS's doc
// comment on why running the full browser matrix unfiltered is wrong
// here), using the same BASE_URL_OCIS/OCIS_URL env vars real screenshot
// capture uses (playwrightCaptureEnv) so extensions with a custom
// global-setup.ts that falls back to an external host on a missing env
// var behave the same way here as they do during capture.
//
// Run from mainCheckout (the repo root), not the extension's own
// directory, with an explicit --config pointing at the extension's
// playwright.config.ts — matching exactly how upstream web-extensions' own
// CI invokes it (`pnpm --filter ./packages/<app> test:e2e`, and pnpm's own
// --filter still executes from the workspace root). Confirmed the hard
// way: an older test helper (pre-dating scaffold's import.meta.url
// convention) resolves its fixture path as a plain
// path.resolve('./support/filesForUpload/...') — relative to
// process.cwd() — which only exists at the REPO ROOT, not inside
// packages/web-app-<appID>/support/. Running with cmd.Dir set to the
// extension's own directory (this function's first version) made every
// such release fail as "ENOENT" regardless of oCIS compatibility.
func runExtensionE2E(mainCheckout, appID, project string) error {
	configPath := filepath.Join("packages", "web-app-"+appID, "playwright.config.ts")
	cmd := exec.Command("pnpm", "playwright", "test", "--config", configPath, "--project="+project, "--retries=0", "--reporter=list")
	cmd.Dir = mainCheckout
	cmd.Env = append(os.Environ(), playwrightCaptureEnv(filepath.Join(os.TempDir(), "extctl-minocis-report.json"))...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("pnpm playwright test: %w\n%s", err, strings.TrimSpace(string(out)))
	}
	return nil
}
