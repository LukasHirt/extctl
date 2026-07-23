package marketplace

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/LukasHirt/extctl/internal/config"
)

// BuildSubmission assembles extensions/<appID>/releases/<version>/ under
// checkout (cfg.MarketplaceRepo.Checkout), writing bundle.zip,
// extension.yaml, and an optional screenshots/ dir, then commits (signed
// off, per CLAUDE.md's DCO convention) on branch publish/<appID>-v<version>.
//
// checkout is reset onto origin/<default branch> before branching so each
// extension in a batch starts from a clean base — necessary since a prior
// extension's submission in the same publish run leaves the checkout on its
// own branch.
func BuildSubmission(cfg *config.Config, checkout, appID, version string, ext ExtensionYAML, bundlePath string, screenshotPaths []string) (branch string, err error) {
	relDir := filepath.Join("extensions", appID, "releases", version)
	absDir := filepath.Join(checkout, relDir)

	branch = "publish/" + appID + "-v" + version
	if err := checkoutBranch(checkout, branch, cfg.MarketplaceRepo.DefaultBranch); err != nil {
		return "", err
	}

	if err := os.MkdirAll(absDir, 0o755); err != nil {
		return "", fmt.Errorf("mkdir %s: %w", absDir, err)
	}

	if err := copyFile(bundlePath, filepath.Join(absDir, "bundle.zip")); err != nil {
		return "", fmt.Errorf("copy bundle.zip: %w", err)
	}

	yamlBytes, err := yaml.Marshal(ext)
	if err != nil {
		return "", fmt.Errorf("marshal extension.yaml: %w", err)
	}
	if err := os.WriteFile(filepath.Join(absDir, "extension.yaml"), yamlBytes, 0o644); err != nil {
		return "", fmt.Errorf("write extension.yaml: %w", err)
	}

	if len(screenshotPaths) > 0 {
		shotsDir := filepath.Join(absDir, "screenshots")
		if err := os.MkdirAll(shotsDir, 0o755); err != nil {
			return "", fmt.Errorf("mkdir screenshots: %w", err)
		}
		for _, src := range screenshotPaths {
			dst := filepath.Join(shotsDir, filepath.Base(src))
			if err := copyFile(src, dst); err != nil {
				return "", fmt.Errorf("copy screenshot %s: %w", src, err)
			}
		}
	}

	if err := runGit(checkout, "add", relDir); err != nil {
		return "", err
	}

	// branch is deterministically named after appID+version, so if it
	// already exists locally (e.g. a prior run committed successfully but
	// failed at push or PR creation) with byte-identical content, there is
	// nothing new to stage here — `git commit` would hard-fail on "nothing
	// to commit". Reuse the existing commit and proceed to push/PR creation
	// instead of treating that as an error.
	changed, err := hasStagedChanges(checkout)
	if err != nil {
		return "", err
	}
	if changed {
		msg := fmt.Sprintf("feat(%s): add %s to the marketplace", appID, version)
		if err := runGit(checkout, "commit", "-s", "-m", msg); err != nil {
			return "", err
		}
	}
	return branch, nil
}

// AmendSubmissionScreenshots rewrites screenshotCaptions in the ALREADY
// CHECKED OUT branch's extension.yaml and replaces its screenshots/ dir
// with newly captured ones, then amends that onto the existing commit
// (rather than creating a new one) so `extctl publish approve` still pushes
// a single, clean commit per submission. Used by RetryScreenshots — every
// other field in extension.yaml (license, tags, minOCIS, authors, ...) is
// left untouched, read back from the file as-is.
func AmendSubmissionScreenshots(checkout, appID, version string, screenshotPaths, captions []string) error {
	relDir := filepath.Join("extensions", appID, "releases", version)
	absDir := filepath.Join(checkout, relDir)

	extPath := filepath.Join(absDir, "extension.yaml")
	ext, err := readExtensionYAMLFile(extPath)
	if err != nil {
		return err
	}
	ext.ScreenshotCaptions = captions

	yamlBytes, err := yaml.Marshal(ext)
	if err != nil {
		return fmt.Errorf("marshal extension.yaml: %w", err)
	}
	if err := os.WriteFile(extPath, yamlBytes, 0o644); err != nil {
		return fmt.Errorf("write extension.yaml: %w", err)
	}

	shotsDir := filepath.Join(absDir, "screenshots")
	if err := os.RemoveAll(shotsDir); err != nil {
		return fmt.Errorf("clear stale screenshots: %w", err)
	}
	if len(screenshotPaths) > 0 {
		if err := os.MkdirAll(shotsDir, 0o755); err != nil {
			return fmt.Errorf("mkdir screenshots: %w", err)
		}
		for _, src := range screenshotPaths {
			dst := filepath.Join(shotsDir, filepath.Base(src))
			if err := copyFile(src, dst); err != nil {
				return fmt.Errorf("copy screenshot %s: %w", src, err)
			}
		}
	}

	if err := runGit(checkout, "add", relDir); err != nil {
		return err
	}
	changed, err := hasStagedChanges(checkout)
	if err != nil {
		return err
	}
	if !changed {
		return nil // identical to what's already committed (still no screenshots, most likely)
	}
	return runGit(checkout, "commit", "-s", "--amend", "--no-edit")
}

// hasStagedChanges reports whether checkout has any staged (index vs HEAD)
// changes, using `git diff --cached --quiet` rather than string-matching
// `git commit`'s "nothing to commit" message.
func hasStagedChanges(checkout string) (bool, error) {
	err := exec.Command("git", "-C", checkout, "diff", "--cached", "--quiet").Run()
	if err == nil {
		return false, nil
	}
	if ee, ok := err.(*exec.ExitError); ok && ee.ExitCode() == 1 {
		return true, nil
	}
	return false, fmt.Errorf("git diff --cached --quiet: %w", err)
}

// checkoutBranch resets checkout onto origin/<baseBranch>, then switches to
// branch — creating it if it doesn't exist yet locally.
func checkoutBranch(checkout, branch, baseBranch string) error {
	if err := runGit(checkout, "checkout", baseBranch); err != nil {
		return err
	}
	if err := runGit(checkout, "reset", "--hard", "origin/"+baseBranch); err != nil {
		return err
	}
	if branchExistsLocally(checkout, branch) {
		return runGit(checkout, "checkout", branch)
	}
	return runGit(checkout, "checkout", "-b", branch)
}

func branchExistsLocally(checkout, branch string) bool {
	out, err := exec.Command("git", "-C", checkout, "branch", "--list", branch).Output()
	return err == nil && len(strings.TrimSpace(string(out))) > 0
}

func runGit(checkout string, args ...string) error {
	cmd := exec.Command("git", append([]string{"-C", checkout}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git %s: %w\n%s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return nil
}
