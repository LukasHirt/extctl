package marketplace

import (
	"archive/zip"
	"crypto/tls"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/LukasHirt/extctl/internal/config"
)

// PreparePlaywrightRun stages the released version's built dist/ into
// cfg.TargetRepo.Checkout so its docker-compose.yml (already wired by a
// prior build.WireExtension run to mount
// ./packages/web-app-<appID>/dist:/web/apps/<appID>) serves the exact
// released bytes: it extracts bundleZipPath's <appID>/* contents directly
// into packages/web-app-<appID>/dist/, overwriting any stale local dist/.
// No pnpm build is needed — the release zip already contains built output.
func PreparePlaywrightRun(mainCheckout, appID, bundleZipPath string) error {
	distDir := filepath.Join(mainCheckout, "packages", "web-app-"+appID, "dist")
	if err := os.RemoveAll(distDir); err != nil {
		return fmt.Errorf("clear stale dist %s: %w", distDir, err)
	}
	if err := os.MkdirAll(distDir, 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", distDir, err)
	}

	r, err := zip.OpenReader(bundleZipPath)
	if err != nil {
		return fmt.Errorf("open %s: %w", bundleZipPath, err)
	}
	defer r.Close() //nolint:errcheck

	prefix := appID + "/"
	for _, f := range r.File {
		if f.Name == appID {
			continue
		}
		if !strings.HasPrefix(f.Name, prefix) {
			continue
		}
		rel := strings.TrimPrefix(f.Name, prefix)
		if rel == "" {
			continue
		}
		dst := filepath.Join(distDir, filepath.FromSlash(rel))
		if f.FileInfo().IsDir() {
			if err := os.MkdirAll(dst, 0o755); err != nil {
				return err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return err
		}
		if err := extractZipFile(f, dst); err != nil {
			return fmt.Errorf("extract %s: %w", f.Name, err)
		}
	}
	return nil
}

func extractZipFile(f *zip.File, dst string) error {
	src, err := f.Open()
	if err != nil {
		return err
	}
	defer src.Close() //nolint:errcheck
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, src); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}

// forceScreenshotConfigTemplate is a sibling config
// (playwright.config.publish.ts) that imports the extension's OWN
// playwright.config.ts unmodified and overrides only use.screenshot/video —
// every other setting the extension's config carries (custom baseURL,
// timeout, testMatch, extra projects, whatever it needs to actually run) is
// preserved via the `...extensionConfig`/`...extensionConfig.use` spreads.
// Written into extDir itself (not over the original file) so testDir's
// relative path resolves exactly the way it does for the extension's own
// config, since both files live in the same directory.
//
// Extensions scaffolded before scaffold/playwright.config.ts gained its
// `screenshot: process.env.CI ? 'on' : 'only-on-failure'` pattern have their
// own committed config with no such override at all (verified against a
// real extension, ai-doc-summary, whose config is just `{...baseConfig,
// testDir: './tests/e2e'}` — CI=true has nothing to hook into there, so
// Playwright falls back to its own default of screenshot:'off'). Relying on
// each extension's own config already having that pattern was too fragile;
// this makes capture work regardless of scaffold vintage, without touching
// (or risking) anything the extension's config already does.
const forceScreenshotConfigTemplate = `import { defineConfig } from '@playwright/test'
import extensionConfig from './playwright.config'

export default defineConfig({
  ...extensionConfig,
  use: {
    ...extensionConfig.use,
    screenshot: 'on',
    video: 'off'
  }
})
`

const forceScreenshotConfigName = "playwright.config.publish.ts"

// writeForceScreenshotConfig writes forceScreenshotConfigTemplate into
// extDir and returns its path. The caller is responsible for removing it
// afterward — mainCheckout gets hard-reset onto origin/<branch> by
// EnsureCheckout at the start of every command, but `git reset --hard` does
// not remove untracked files, so a leftover copy would otherwise persist
// across runs.
func writeForceScreenshotConfig(extDir string) (string, error) {
	path := filepath.Join(extDir, forceScreenshotConfigName)
	if err := os.WriteFile(path, []byte(forceScreenshotConfigTemplate), 0o644); err != nil {
		return "", err
	}
	return path, nil
}

// ProjectForScreenshots resolves which single Playwright project
// (chrome/firefox/webkit) to actually run for appID's screenshot capture:
// appID's entry in cfg.Publish.ScreenshotProjectOverrides if present, else
// the org-wide cfg.Publish.ScreenshotProject default. Only ONE project
// runs — see runPlaywrightDirect's doc comment for why running all three
// unconditionally was wasted work.
func ProjectForScreenshots(cfg *config.Config, appID string) string {
	if override, ok := cfg.Publish.ScreenshotProjectOverrides[appID]; ok && override != "" {
		return override
	}
	return cfg.Publish.ScreenshotProject
}

// localOCISURL is the URL a freshly-brought-up main checkout's oCIS is
// reachable at — the same host root/playwright.config.ts falls back to when
// BASE_URL_OCIS is unset (`baseURL: process.env.BASE_URL_OCIS ??
// 'https://host.docker.internal:9200'`), and the same host waitForOCIS polls.
const localOCISURL = "https://host.docker.internal:9200"

// playwrightCaptureEnv is the environment a `pnpm playwright test` run
// against a freshly-brought-up capture oCIS instance needs, on top of
// os.Environ() — shared between runPlaywrightDirect (the orchestrator
// invoking Playwright itself) and GenerateScreenshotSpec (Claude invoking it
// via its own scoped Bash access), so both paths get identical behavior.
//
// PLAYWRIGHT_JSON_OUTPUT_FILE points Playwright's JSON reporter at
// reportPath. BASE_URL_OCIS/OCIS_URL: root playwright.config.ts falls back
// to localOCISURL itself when BASE_URL_OCIS is unset, but not every
// extension's e2e setup goes through that config's baseURL — some (verified:
// photo-addon, advanced-search, ai-sensitive-data-scanner) ship their own
// global-setup.ts that logs in with a manually-launched browser and falls
// back to a hardcoded EXTERNAL dev URL (`https://cloud.faure.ca`, checking
// BASE_URL_OCIS then OCIS_URL first) when neither var is set. Without this,
// those extensions' auth step silently hits that external host instead of
// the local stack just brought up — indistinguishable from the outside from
// a genuine oCIS-readiness timeout. Setting both var names covers both
// fallback conventions regardless of which one a given extension checks.
func playwrightCaptureEnv(reportPath string) []string {
	return []string{
		"CI=true",
		"PLAYWRIGHT_JSON_OUTPUT_FILE=" + reportPath,
		"BASE_URL_OCIS=" + localOCISURL,
		"OCIS_URL=" + localOCISURL,
	}
}

// prepareOCISForCapture brings up a completely fresh oCIS stack in
// cfg.TargetRepo.Checkout (freshOCISUp: full docker compose down + up -d, so
// it picks up the dist/ PreparePlaywrightRun already staged, and so oCIS's
// auth backend gets a real cold-start rather than racing a plain restart —
// see freshOCISUp's doc comment) and everything a Playwright run against it
// needs: dependencies installed (node_modules, including the playwright
// binary, is never committed and is missing entirely on mainCheckout — a
// bare git checkout hard-reset by EnsureCheckout on every command — until
// something installs it; gate/run-gate.sh runs `pnpm install
// --frozen-lockfile` before its own e2e stage, but this path uses
// --no-frozen-lockfile instead — the extension's package.json here has
// already been pinned to an old release's dependency versions by
// pinExtensionSourceToRelease, which the current, unpinned root
// pnpm-lock.yaml won't match for any release but the latest; no
// `pnpm build`, unlike gate — dist/ is already the exact released bytes from
// PreparePlaywrightRun and building would overwrite that with a fresh local
// build), stale auth state cleared (clearStaleAuthCache), and the
// force-screenshot config written (writeForceScreenshotConfig).
//
// Returns extDir (packages/web-app-<appID>/) and the override config's path
// — the caller removes the latter once done with it (a leftover copy would
// otherwise persist across runs, since `git reset --hard` doesn't clean up
// untracked files).
func prepareOCISForCapture(cfg *config.Config, appID string) (extDir, overridePath string, err error) {
	mainCheckout := cfg.TargetRepo.Checkout
	extDir = filepath.Join(mainCheckout, "packages", "web-app-"+appID)

	if err := freshOCISUp(mainCheckout); err != nil {
		return "", "", fmt.Errorf("bring up ocis: %w", err)
	}
	if err := waitForOCIS(); err != nil {
		return "", "", err
	}
	if err := clearStaleAuthCache(extDir); err != nil {
		return "", "", err
	}
	// --no-frozen-lockfile: pinExtensionSourceToRelease already moved this
	// package's package.json back to an old release's dependency versions,
	// but pnpm resolves against the WORKSPACE ROOT lockfile regardless of
	// installCmd.Dir — which still reflects the current default branch. For
	// any release that isn't the very latest, that manifest/lockfile
	// mismatch trips ERR_PNPM_OUTDATED_LOCKFILE under --frozen-lockfile. The
	// node_modules this produces is throwaway (never committed, wiped by the
	// next EnsureCheckout hard reset), so letting pnpm reconcile the lockfile
	// here is safe.
	installCmd := exec.Command("pnpm", "install", "--no-frozen-lockfile")
	installCmd.Dir = extDir
	// No TTY on this exec.Command, so pnpm's interactive confirmation before
	// removing a stale node_modules aborts instead of prompting
	// (ERR_PNPM_ABORTED_REMOVE_MODULES_DIR_NO_TTY) unless CI=true tells it to
	// auto-confirm, same as pnpm's own suggested fix.
	installCmd.Env = append(os.Environ(), "CI=true")
	if out, err := installCmd.CombinedOutput(); err != nil {
		return "", "", fmt.Errorf("pnpm install: %w\n%s", err, strings.TrimSpace(string(out)))
	}
	overridePath, err = writeForceScreenshotConfig(extDir)
	if err != nil {
		return "", "", fmt.Errorf("write screenshot-capture config: %w", err)
	}
	return extDir, overridePath, nil
}

// runPlaywrightDirect invokes `pnpm playwright test` against the
// already-generated spec at packages/web-app-<appID>/tests/e2e/marketplace-
// screenshots.spec.ts directly — restricted to ONE Playwright project via
// --project (ProjectForScreenshots) rather than the extension's full
// chrome/firefox/webkit matrix, since screenshots only ever use one
// project's output anyway and running the other two just to throw their
// results away triples both wall-clock time and the number of tests that
// can time out. This is a deliberate, explicit per-extension override
// (config, not automatic multi-browser fallback): every capture failure
// observed so far failed identically across all three browsers — a systemic
// issue (auth, or a bug in the generated spec's own setup code), not a
// browser-specific one.
//
// This is the FALLBACK path: GenerateScreenshotSpec's Claude session has its
// own scoped Bash access to run this same command itself and iterate on
// failures before returning (see marketplace-screenshots.md) — this
// function only runs if that session didn't leave a report behind at all
// (e.g. it wrote the spec but never got around to testing it), so a capture
// attempt still yields SOME result instead of nothing to collect. Writes the
// JSON report to reportPath and a copy of stdout/stderr to
// filepath.Dir(reportPath)/playwright.log, always, regardless of outcome —
// so a run that "succeeds" but still yields zero screenshots is diagnosable
// instead of leaving no trace.
func runPlaywrightDirect(extDir, project, reportPath string) error {
	cmd := exec.Command("pnpm", "playwright", "test",
		screenshotSpecRelPath,
		"--config", forceScreenshotConfigName,
		"--project="+project,
		"--retries=0", "--reporter=list,json")
	cmd.Dir = extDir
	cmd.Env = append(os.Environ(), playwrightCaptureEnv(reportPath)...)
	out, err := cmd.CombinedOutput()
	_ = os.WriteFile(filepath.Join(filepath.Dir(reportPath), "playwright.log"), out, 0o644)
	if err != nil {
		// Playwright exits non-zero if ANY project/test failed — a single
		// browser missing (e.g. webkit not installed via `playwright
		// install`) fails every test under that project even though
		// cfg.Publish.ScreenshotProject (chrome by default) may have run
		// fine. Only treat this as fatal if no JSON report was produced at
		// all; otherwise let the caller's CollectScreenshots pull whatever
		// usable screenshots the report does have.
		if _, statErr := os.Stat(reportPath); statErr != nil {
			return fmt.Errorf("pnpm playwright test: %w\n%s", err, strings.TrimSpace(string(out)))
		}
	}
	return nil
}

// freshOCISUp brings up a completely fresh oCIS stack in mainCheckout: down
// then up -d, matching gate/run-gate.sh's e2e stage exactly (down is
// best-effort — matching run-gate.sh's `|| true`, since there's nothing to
// tear down on a cold start). A plain `docker compose restart ocis` (this
// function's first version) was tried instead, since it's faster and the
// main checkout's oCIS is normally left running as a long-lived dev
// convenience — but it caused real, intermittent failures: restart brings
// the process back "alive" (passing waitForOCIS's health check) well before
// oCIS's auth backend (LDAP/IDP token issuance) has actually re-initialized,
// so Playwright's login step hung until its own 30s test timeout. A full
// down+up costs more wall-clock time but is the one bring-up path already
// proven correct by the gate's own e2e stage.
func freshOCISUp(mainCheckout string) error {
	if err := ensureExternalSitesManifest(mainCheckout); err != nil {
		return err
	}

	downCmd := exec.Command("docker", "compose", "down")
	downCmd.Dir = mainCheckout
	_, _ = downCmd.CombinedOutput() // best-effort, same as run-gate.sh's `|| true`

	upCmd := exec.Command("docker", "compose", "up", "-d")
	upCmd.Dir = mainCheckout
	out, err := upCmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("docker compose up -d: %w\n%s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// clearStaleAuthCache removes extDir/tests/e2e/.auth, if present, before
// every capture run. Some extensions (confirmed: photo-addon's fixtures.ts)
// cache Playwright storageState to <ext>/tests/e2e/.auth/user.json and reuse
// it purely based on file existence — no freshness/TTL check at all. That's
// fine for a normal dev workflow where oCIS stays up for a long-lived
// session, but freshOCISUp just above tore down and recreated oCIS from
// scratch, which regenerates its IDP signing key — a cached token signed by
// the PREVIOUS instance's key then fails with a real, reproduced error:
// "failed to verify access token: token signature is invalid:
// crypto/rsa: verification error", which surfaces to Playwright as a stuck
// OIDC login redirect loop and a 30s timeout, indistinguishable from an
// actual oCIS-readiness flake without reading the container's own logs.
// Best-effort and silent on a missing dir (most extensions have no .auth/ at
// all) — this only ever removes a cache extctl's own fresh bring-up already
// invalidated, never anything a human might want kept.
func clearStaleAuthCache(extDir string) error {
	dir := filepath.Join(extDir, "tests", "e2e", ".auth")
	if err := os.RemoveAll(dir); err != nil {
		return fmt.Errorf("clear stale auth cache %s: %w", dir, err)
	}
	return nil
}

// ensureExternalSitesManifest works around a Docker Desktop (macOS/virtiofs)
// bind-mount bug: docker-compose.yml mounts web-app-external-sites's whole
// dist/ dir AND separately stacks tests/config/manifest.json on top of that
// same container path (its way of injecting test-only config). mainCheckout
// is a bare git checkout hard-reset by EnsureCheckout on every command — dist/
// is build output, never committed, so on a fresh checkout it doesn't exist
// at all (unlike every other registered extension's dist/, a plain directory
// mount tolerates that fine). Docker auto-vivifies the missing path for a
// directory mount, but when a second, file-level mount then tries to stack
// onto that same just-vivified path, virtiofs fails with a confusing "mount
// point is outside of rootfs" error instead of just working — confirmed by
// reproducing it directly and confirming a real (non-empty) dist/manifest.json
// already present before `docker compose up` avoids it entirely. external-sites
// itself is irrelevant to screenshot capture; this only needs to exist so oCIS
// can start.
func ensureExternalSitesManifest(mainCheckout string) error {
	dst := filepath.Join(mainCheckout, "packages", "web-app-external-sites", "dist", "manifest.json")
	if info, err := os.Stat(dst); err == nil && info.Size() > 0 {
		return nil
	}
	src := filepath.Join(mainCheckout, "packages", "web-app-external-sites", "tests", "config", "manifest.json")
	if _, err := os.Stat(src); err != nil {
		return nil // external-sites isn't registered in this checkout at all
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return fmt.Errorf("mkdir for external-sites manifest workaround: %w", err)
	}
	if err := copyFile(src, dst); err != nil {
		return fmt.Errorf("stage external-sites manifest workaround: %w", err)
	}
	return nil
}

// waitForOCIS polls the same health endpoint gate/run-gate.sh's e2e stage
// waits on, up to 180s, then waits an additional settle period before
// returning.
//
// /health/live only confirms the gateway/proxy is answering — oCIS's
// all-in-one deployment runs many internal services (auth/IDP, graph,
// search, thumbnails, ...) that can still be finishing their own startup
// after the gateway already reports "alive". Confirmed empirically: a
// freshly-brought-up stack intermittently failed marketplace screenshot
// capture with a 30s timeout during login (auth not ready yet), then on a
// later run failed differently with a 30s timeout during data loading
// (some other service not ready yet), then succeeded with no code change —
// consistent with genuine startup timing. There is no distinct readiness
// endpoint to poll instead: /health/ready, /readyz, and /healthz all fall
// through to the SPA's index.html via Traefik rather than a real check
// (verified against a live stack this session). This settle buffer is the
// pragmatic mitigation for that specific class of failure in the absence of
// a real readiness signal.
//
// Not every observed storageState timeout is this, though: at least one case
// traced back to a completely different bug — see playwrightCaptureEnv's
// BASE_URL_OCIS/OCIS_URL env vars — where some extensions' own custom
// global-setup.ts was silently authenticating against an unrelated external
// host instead of this stack at all, which looks identical from the outside
// (a 30s timeout during login) but has nothing to do with oCIS readiness.
const ocisSettleBuffer = 15 * time.Second

func waitForOCIS() error {
	client := &http.Client{
		Timeout: 2 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec // self-signed dev cert
		},
	}
	url := localOCISURL + "/health/live"
	for range 60 {
		resp, err := client.Get(url)
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				time.Sleep(ocisSettleBuffer)
				return nil
			}
		}
		time.Sleep(3 * time.Second)
	}
	return fmt.Errorf("oCIS did not become reachable within 180s at %s", url)
}

// e2eLockPath matches gate/run-gate.sh's own lock file path exactly.
func e2eLockPath() string {
	dir := os.Getenv("TMPDIR")
	if dir == "" {
		dir = "/tmp"
	}
	return filepath.Join(dir, "extctl-gate-e2e.lock.d")
}

// withE2ELock runs fn while holding the same mkdir-based lock
// gate/run-gate.sh's e2e stage uses, including its stale-owner-pid recovery.
func withE2ELock(fn func() error) error {
	lock := e2eLockPath()
	for {
		if err := os.Mkdir(lock, 0o755); err == nil {
			break
		}
		if owner, rerr := os.ReadFile(filepath.Join(lock, "pid")); rerr == nil {
			if pid := strings.TrimSpace(string(owner)); pid != "" && !processAlive(pid) {
				_ = os.RemoveAll(lock)
				continue
			}
		}
		time.Sleep(5 * time.Second)
	}
	defer os.RemoveAll(lock) //nolint:errcheck

	_ = os.WriteFile(filepath.Join(lock, "pid"), fmt.Appendf(nil, "%d", os.Getpid()), 0o644)

	return fn()
}

func processAlive(pid string) bool {
	return exec.Command("kill", "-0", pid).Run() == nil
}
