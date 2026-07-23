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
	extPath := filepath.Join("packages", "web-app-"+appID)
	restore = func() error {
		return gitpkg.CheckoutPath(mainCheckout, "HEAD", extPath)
	}

	if err := gitpkg.FetchTag(mainCheckout, tag); err != nil {
		return restore, fmt.Errorf("fetch tag %s: %w", tag, err)
	}
	if err := gitpkg.CheckoutPath(mainCheckout, tag, extPath); err != nil {
		return restore, fmt.Errorf("checkout %s at %s: %w", extPath, tag, err)
	}
	return restore, nil
}
