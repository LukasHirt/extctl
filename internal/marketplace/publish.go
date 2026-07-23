package marketplace

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/LukasHirt/extctl/internal/config"
	"github.com/LukasHirt/extctl/internal/gate"
)

// Options configures a publish (stage) run.
type Options struct {
	Config *config.Config
	DryRun bool
	OnlyID string // if non-empty, stage only this app-id (no web-app- prefix)
}

// StagedResult is one extension whose submission was built and committed
// locally — bundle, extension.yaml, screenshots (if capture succeeded) — on
// branch Branch in cfg.MarketplaceRepo.Checkout, but not yet pushed or
// opened as a PR. Review ReviewDir (screenshots, playwright.log, the
// generated spec if capture failed) and run `extctl publish approve` when
// satisfied, or `extctl publish retry-screenshots` to recapture first.
type StagedResult struct {
	AppID          string
	Version        string
	Branch         string
	ReviewDir      string
	HasScreenshots bool
}

// FailedResult is one extension that could not be staged this run.
type FailedResult struct {
	AppID   string
	Version string
	Err     error
}

// Summary is the outcome of a publish (stage) run.
type Summary struct {
	Scanned int
	Staged  []StagedResult
	Skipped []string // "<appID>@<version>" for already-published extensions or ones with an open PR already
	Failed  []FailedResult
}

// Run scans for web-extensions releases not yet published to
// cfg.MarketplaceRepo.Remote, and for each one: downloads its release
// bundle, captures screenshots (best-effort), resolves tags/minOCIS,
// generates extension.yaml, and commits the assembled submission to a local
// publish/<app-id>-v<version> branch in cfg.MarketplaceRepo.Checkout —
// deliberately stopping there rather than pushing and opening a PR
// automatically. Review the printed summary (and ReviewDir's screenshots)
// for each staged extension, then run `extctl publish approve <id>` to push
// and open its PR, or `extctl publish retry-screenshots <id>` to recapture
// screenshots first if they came up empty.
//
// Every run is a fresh, stateless scan+diff against the marketplace
// checkout — same model as `release`. Per-extension failures don't abort
// the batch; Run only returns a non-nil error if every attempted extension
// failed.
//
// When multiple unpublished versions of the SAME extension are attempted in
// one Run (e.g. v0.1.0, v0.2.0, v0.3.0 all missing), they'd otherwise each
// independently re-derive tags/minOCIS: PreviousRelease only sees what's
// actually merged into the marketplace repo, and a staged-but-unapproved
// submission doesn't count as merged. metadata caches whichever version
// processes first (oldest, per Scan's sort) and lets later versions of the
// same extension reuse it instead of independently re-guessing — keeping
// all submissions in one batch consistent.
func Run(opts Options, w io.Writer) (*Summary, error) {
	cfg := opts.Config
	printf := func(format string, a ...any) { _, _ = fmt.Fprintf(w, format, a...) }

	results, err := Scan(cfg)
	if err != nil {
		return nil, err
	}

	summary := &Summary{Scanned: len(results)}
	attempted := 0
	metadata := newRunMetadataCache()

	for _, r := range results {
		if opts.OnlyID != "" && r.AppID != opts.OnlyID {
			continue
		}
		if r.Published {
			printf("skip    %s@%s (already published)\n", r.AppID, r.Version)
			summary.Skipped = append(summary.Skipped, r.AppID+"@"+r.Version)
			continue
		}

		branch := "publish/" + r.AppID + "-v" + r.Version
		// gh permanently associates a PR with its head branch name even
		// after that branch is deleted — a CLOSED/MERGED result here does
		// NOT mean anything is still in flight. Only an OPEN PR means
		// there's nothing new to stage.
		if existing, err := findExistingPR(cfg.MarketplaceRepo.Remote, branch); err != nil {
			printf("  warning: could not check for an existing PR for %s@%s: %v\n", r.AppID, r.Version, err)
		} else if existing != nil && existing.State == "OPEN" {
			printf("skip    %s@%s (already has an open PR: %s)\n", r.AppID, r.Version, existing.URL)
			summary.Skipped = append(summary.Skipped, r.AppID+"@"+r.Version)
			continue
		}

		if opts.DryRun {
			printf("would   stage %s@%s\n", r.AppID, r.Version)
			continue
		}

		attempted++
		printf("stage   %s@%s…\n", r.AppID, r.Version)
		staged, err := stageOne(cfg, r, metadata, printf)
		if err != nil {
			printf("failed  %s@%s: %v\n", r.AppID, r.Version, err)
			summary.Failed = append(summary.Failed, FailedResult{AppID: r.AppID, Version: r.Version, Err: err})
			continue
		}
		screenshotNote := " (no screenshots — see review dir, or `extctl publish retry-screenshots`)"
		if staged.HasScreenshots {
			screenshotNote = ""
		}
		printf("staged  %s@%s — branch %s, review at %s%s\n", r.AppID, r.Version, staged.Branch, staged.ReviewDir, screenshotNote)
		summary.Staged = append(summary.Staged, *staged)
	}

	if attempted > 0 && len(summary.Staged) == 0 {
		return summary, fmt.Errorf("publish: all %d attempted extension(s) failed to stage", attempted)
	}
	return summary, nil
}

// maxFreshSpecAttempts bounds how many times captureScreenshotsWithRetry
// will generate an entirely NEW screenshot spec (a fresh Claude call, not a
// re-run of a previous one) and capture against it, stopping as soon as one
// attempt has every test in that attempt's spec passing.
//
// This differs from an earlier, since-removed same-spec retry (paused at 1
// attempt): retrying an IDENTICAL spec against a content bug can never
// succeed, since the bug travels with the spec. A fresh Claude call writes
// genuinely different test code each time (different locators, different
// assertion bounds, sometimes different mocked data entirely) — real content
// bugs observed in practice (an exact-count assertion that didn't match the
// live DOM, a test photo missing the tags/AI-caption fields its own
// assertions checked for) do NOT reliably recur across independent
// generations, so retrying with fresh content is a meaningfully different
// bet than retrying the same one. Each attempt costs a full Claude call plus
// an oCIS teardown/bring-up/Playwright cycle (~2-4 minutes) — 3 is a
// deliberately modest cap, not a guarantee every extension converges to a
// full pass within it.
const maxFreshSpecAttempts = 3

// captureScreenshotsWithRetry generates a screenshot spec via Claude and
// runs it against a freshly-brought-up oCIS, retrying with an entirely new
// spec (see maxFreshSpecAttempts) until one attempt has zero test failures,
// or the attempt cap is exhausted — in which case the last attempt's
// (partial, or empty) result is returned rather than discarded, since some
// screenshots are still better than none for a best-effort feature.
//
// Best-effort throughout: any failure (spec generation, staging, capture,
// collection) is logged via printf; only a hard failure preparing dist/
// aborts early. Every other failure just moves on to the next attempt.
func captureScreenshotsWithRetry(cfg *config.Config, r Result, bundlePath, reviewDir string, printf func(string, ...any)) (paths, captions []string) {
	if err := PreparePlaywrightRun(cfg.TargetRepo.Checkout, r.AppID, bundlePath); err != nil {
		printf("  warning: could not stage dist for screenshot capture: %v\n", err)
		return nil, nil
	}

	// Pinned once for every attempt below — all of them target the same
	// release (r.Tag), so the source only needs to move and move back once,
	// not per attempt. See pinExtensionSourceToRelease's doc comment for why
	// this matters: without it, Claude writes selectors against whatever the
	// default branch looks like NOW, which for anything but the newest
	// release routinely no longer matches the dist/ actually being served.
	restoreExtensionSource, pinErr := pinExtensionSourceToRelease(cfg.TargetRepo.Checkout, r.AppID, r.Tag)
	if pinErr != nil {
		printf("  warning: could not pin extension source to %s — generated selectors may target markup this release doesn't have: %v\n", r.Tag, pinErr)
	}
	defer func() {
		if err := restoreExtensionSource(); err != nil {
			printf("  warning: could not restore extension source after screenshot capture: %v\n", err)
		}
	}()

	reportPath := filepath.Join(reviewDir, "e2e-report.json")
	logPath := filepath.Join(reviewDir, "playwright.log")

	for attempt := 1; attempt <= maxFreshSpecAttempts; attempt++ {
		if attempt > 1 {
			printf("  not every test passed — generating a fresh spec and retrying (attempt %d/%d)\n", attempt, maxFreshSpecAttempts)
		}

		// Cleared unconditionally before every attempt: GenerateScreenshotSpec's
		// Claude session runs `pnpm playwright test` itself via its own scoped
		// Bash access now (see prompts/marketplace-screenshots.md), which
		// writes reportPath (via the PLAYWRIGHT_JSON_OUTPUT_FILE env var
		// GenerateScreenshotSpec sets) but leaves no text log anywhere the
		// orchestrator controls — logPath only gets (re)written by the
		// runPlaywrightDirect fallback below, which doesn't run at all when
		// Claude's own session already succeeded. Without this, a stale log
		// from an earlier attempt (or an earlier `retry-screenshots` call
		// entirely) would silently keep describing a run that isn't the one
		// reportPath now reflects — actively misleading, worse than absent.
		os.Remove(reportPath) //nolint:errcheck
		os.Remove(logPath)    //nolint:errcheck

		project := ProjectForScreenshots(cfg, r.AppID)
		var specPath string
		attemptErr := withE2ELock(func() error {
			extDir, overridePath, err := prepareOCISForCapture(cfg, r.AppID)
			if err != nil {
				return err
			}
			defer os.Remove(overridePath) //nolint:errcheck

			specPath, err = GenerateScreenshotSpec(cfg, r.AppID, reportPath, project)
			if err != nil {
				return fmt.Errorf("generate screenshot spec: %w", err)
			}
			if _, statErr := os.Stat(reportPath); statErr == nil {
				return nil // Claude's own self-test session already produced a report
			}
			// Claude's session wrote the spec but never got around to
			// running it itself (or ran out of its own self-limited
			// fix-and-rerun budget before doing so) — run it directly so
			// this attempt still yields SOME result to collect.
			printf("  note: spec-writing session left no test report — running the spec directly\n")
			return runPlaywrightDirect(extDir, project, reportPath)
		})
		if specPath != "" {
			// Preserved alongside playwright.log/e2e-report.json in reviewDir
			// so a failure is actually diagnosable — without this, whatever
			// Claude wrote is gone by the time anyone looks. Overwritten each
			// attempt; only the last attempt's spec survives, matching the
			// last attempt's report/log it's diagnosing.
			if err := copyFile(specPath, filepath.Join(reviewDir, filepath.Base(specPath))); err != nil {
				printf("  warning: could not preserve generated screenshot spec for diagnosis: %v\n", err)
			}
			os.Remove(specPath) //nolint:errcheck
		}
		if attemptErr != nil {
			printf("  warning: screenshot capture failed: %v\n", attemptErr)
			continue
		}

		p, c, warnings, err := CollectScreenshots(cfg, r.AppID, reportPath, filepath.Join(reviewDir, "screenshots"))
		if err != nil {
			printf("  warning: could not collect screenshots: %v\n", err)
			continue
		}
		for _, w := range warnings {
			printf("  warning: %s\n", w)
		}
		if len(p) == 0 {
			printf("  note: playwright ran but the JSON report has no passing screenshot attachments — see %s/playwright.log and %s\n", reviewDir, reportPath)
			continue
		}
		// Kept in case every attempt runs out without a full pass — note this
		// is the LAST non-empty attempt's result, not necessarily the best
		// one seen: CollectScreenshots clears reviewDir/screenshots/ on
		// every call, so an earlier attempt with more passing screenshots
		// than a later, worse one is not preserved. Not tracked/compared
		// across attempts — a human reviews the result either way before
		// approving, and the common case (attempts of roughly similar
		// quality) doesn't warrant the extra bookkeeping this would need.
		paths, captions = p, c

		allPassed, err := gate.AllTestsPassed(reportPath)
		if err != nil {
			printf("  warning: could not verify every test passed: %v\n", err)
			continue
		}
		// A dropped screenshot (len(warnings) > 0 — e.g. isVisuallyDegenerate
		// caught a blank/still-loading capture) means this attempt is NOT a
		// full success even if every Playwright test technically passed: the
		// test's own assertions didn't catch that its screenshot shows
		// nothing real. Treating that the same as a failed test is what
		// makes the fresh-spec retry actually trigger for this class of
		// problem instead of silently shipping fewer screenshots than asked
		// for.
		if allPassed && len(warnings) == 0 {
			return paths, captions
		}
	}
	if len(paths) > 0 {
		printf("  warning: exhausted %d attempt(s) without every test passing cleanly — using the last attempt's %d screenshot(s)\n", maxFreshSpecAttempts, len(paths))
	}
	return paths, captions
}

// stageOne downloads appID@r.Version's release bundle, captures screenshots
// (best-effort), resolves tags/minOCIS, and commits the assembled
// submission to a local branch in cfg.MarketplaceRepo.Checkout — but does
// NOT push or create a PR. reviewDir (cfg.RunsDir/publish/<appID>-<version>)
// is durable (not an OS temp dir) precisely so it survives until a human
// gets around to reviewing it and running `extctl publish approve` or
// `retry-screenshots`, possibly much later or in a separate process.
func stageOne(cfg *config.Config, r Result, metadata *runMetadataCache, printf func(string, ...any)) (*StagedResult, error) {
	reviewDir := filepath.Join(cfg.RunsDir, "publish", r.AppID+"-"+r.Version)

	bundlePath, err := DownloadBundle(cfg.TargetRepo.Remote, r.Tag, r.AppID, r.AssetName, reviewDir)
	if err != nil {
		return nil, fmt.Errorf("download bundle: %w", err)
	}

	pkg, err := ReadPackageJSON(cfg.TargetRepo.Checkout, cfg.DefaultBranch, r.AppID)
	if err != nil {
		return nil, fmt.Errorf("read package.json: %w", err)
	}

	screenshotPaths, captions := captureScreenshotsWithRetry(cfg, r, bundlePath, reviewDir, printf)

	prev, err := PreviousRelease(cfg.MarketplaceRepo.Checkout, cfg.MarketplaceRepo.DefaultBranch, r.AppID)
	if err != nil {
		printf("  warning: could not check for a previous release: %v\n", err)
	}
	tags, tagSource := metadata.resolveTags(cfg, r.AppID, prev, printf)
	minOCIS, minOCISSource := metadata.resolveMinOCIS(cfg, r.AppID, prev, printf)
	printReviewNotes(printf, r.AppID, tags, tagSource, minOCIS, minOCISSource)

	ext, err := BuildExtensionYAML(cfg, r.AppID, r.Version, pkg, tags, minOCIS, captions)
	if err != nil {
		return nil, err
	}

	branch, err := BuildSubmission(cfg, cfg.MarketplaceRepo.Checkout, r.AppID, r.Version, ext, bundlePath, screenshotPaths)
	if err != nil {
		return nil, fmt.Errorf("build submission: %w", err)
	}

	return &StagedResult{
		AppID:          r.AppID,
		Version:        r.Version,
		Branch:         branch,
		ReviewDir:      reviewDir,
		HasScreenshots: len(screenshotPaths) > 0,
	}, nil
}

// printReviewNotes prints, to the terminal at staging time, what used to be
// posted as an "extctl notes — please double-check" section in the PR body
// itself. Now that a human reviews before ever running `approve`, there's
// no need to carry that context into the PR for a marketplace maintainer
// who has no way to act on it anyway — see FormatPRBody.
func printReviewNotes(printf func(string, ...any), appID string, tags []string, tagSource TagSource, minOCIS string, minOCISSource MinOCISSource) {
	printf("  name: %q (humanized from the app id — not authored, check it reads well)\n", humanizeAppID(appID))
	switch tagSource {
	case TagSourcePreviousRelease:
		printf("  tags: %s (reused from a previously-published release)\n", strings.Join(tags, ", "))
	case TagSourceThisRun:
		printf("  tags: %s (reused from another version staged in this same run)\n", strings.Join(tags, ", "))
	case TagSourceClaude:
		printf("  tags: %s (inferred by claude — sanity-check them)\n", strings.Join(tags, ", "))
	default:
		printf("  tags: none — marketplace CI will reject this until at least one is added\n")
	}
	switch minOCISSource {
	case MinOCISSourcePreviousRelease:
		printf("  minOCIS: %s (reused from a previously-published release)\n", minOCIS)
	case MinOCISSourceThisRun:
		printf("  minOCIS: %s (reused from another version staged in this same run)\n", minOCIS)
	case MinOCISSourceHistory:
		printf("  minOCIS: %s (inferred from oCIS release history — not a verified compatibility check)\n", minOCIS)
	default:
		printf("  minOCIS: unset (optional)\n")
	}
}
