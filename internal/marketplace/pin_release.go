package marketplace

import (
	"fmt"
	"path/filepath"

	gitpkg "github.com/LukasHirt/extctl/internal/git"
)

// pinExtensionSourceToRelease switches packages/web-app-<appID> in
// cfg.TargetRepo.Checkout to the exact tree it had at tag — the release
// actually being screenshotted — without moving HEAD or touching anything
// else. The rest of the checkout stays wherever EnsureCheckout's hard reset
// onto origin/<defaultBranch> left it.
//
// GenerateScreenshotSpec has Claude read that package's own src/ and
// tests/e2e/acceptance.spec.ts to pick selectors for the screenshot spec it
// writes, but the dist/ actually served to Playwright (PreparePlaywrightRun)
// is that OLD release's build. Without this, Claude always reads whatever
// source currently lives on the default branch — if the extension's markup
// has moved on since the release being published (a renamed class, a
// component swapped for a design-system one), every selector it writes
// targets DOM the release's dist/ doesn't have, and no amount of
// fix-and-rerun cycling can ever make it pass: the bug is the version skew
// itself, not anything in the generated spec.
//
// Returns a restore func the caller must call (even when err != nil — a
// failed fetch/checkout may have partially applied) to put the path back to
// HEAD's content before returning.
func pinExtensionSourceToRelease(mainCheckout, appID, tag string) (restore func() error, err error) {
	return pinPathsToRelease(mainCheckout, tag, []string{filepath.Join("packages", "web-app-"+appID)})
}

// pinExtensionAndSupportToRelease is pinExtensionSourceToRelease plus the
// shared support/ dir (page objects, auth helpers — everything the
// extension's own tests/e2e/*.spec.ts imports via relative paths like
// "../../../../support/pages/..."). Screenshot capture writes a BRAND NEW
// spec at staging time, so it only needs the extension's own current
// source pinned. checkExtensionAgainstOCIS instead runs that release's
// ORIGINAL, historical test file — and support/ is not part of
// packages/web-app-<appID>, so pinExtensionSourceToRelease alone leaves it
// on whatever HEAD currently has. Confirmed the hard way: unzip-v0.4.0's
// extractZip.spec.ts failed identically against every candidate oCIS
// version, because support/pages/filesAppBarActions.ts had genuinely
// changed (different selectors, different upload flow) since that
// release — the same version-skew problem pinExtensionSourceToRelease's
// doc comment already describes for an extension's own source, just one
// directory over.
func pinExtensionAndSupportToRelease(mainCheckout, appID, tag string) (restore func() error, err error) {
	return pinPathsToRelease(mainCheckout, tag, []string{
		filepath.Join("packages", "web-app-"+appID),
		"support",
	})
}

// pinPathsToRelease switches every path in paths, in mainCheckout, to the
// exact tree each had at tag — without moving HEAD or touching anything
// else. The rest of the checkout stays wherever EnsureCheckout's hard reset
// onto origin/<defaultBranch> left it.
//
// Returns a restore func the caller must call (even when err != nil — a
// failed fetch/checkout may have partially applied) to put every path back
// to HEAD's content before returning.
func pinPathsToRelease(mainCheckout, tag string, paths []string) (restore func() error, err error) {
	restore = func() error {
		var firstErr error
		for _, p := range paths {
			if err := gitpkg.CheckoutPath(mainCheckout, "HEAD", p); err != nil && firstErr == nil {
				firstErr = err
			}
		}
		return firstErr
	}

	if err := gitpkg.FetchTag(mainCheckout, tag); err != nil {
		return restore, fmt.Errorf("fetch tag %s: %w", tag, err)
	}
	for _, p := range paths {
		if err := gitpkg.CheckoutPath(mainCheckout, tag, p); err != nil {
			return restore, fmt.Errorf("checkout %s at %s: %w", p, tag, err)
		}
	}
	return restore, nil
}
