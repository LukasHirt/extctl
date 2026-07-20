package git

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// cloneCommand builds the command used to clone a fresh checkout. It shells
// out to `gh repo clone` (rather than raw `git clone` against a hand-built
// HTTPS/SSH URL) so cloning reuses whatever auth the user already has set up
// for `gh` — the same CLI extctl relies on elsewhere for PR creation.
// Overridable in tests.
var cloneCommand = exec.Command

// EnsureCheckout makes sure a usable local checkout of remote (a "owner/repo"
// GitHub slug) exists at checkoutPath: cloning it if the path has no .git
// dir yet, or fetching + fast-forwarding it onto origin/<defaultBranch>
// otherwise. checkoutPath is a fixed path extctl owns exclusively (never a
// checkout a developer works in directly), so it is always safe to reset —
// there is no local work to lose.
func EnsureCheckout(remote, checkoutPath, defaultBranch string) error {
	if _, err := os.Stat(filepath.Join(checkoutPath, ".git")); err == nil {
		if err := FetchOrigin(checkoutPath); err != nil {
			return fmt.Errorf("fetch origin in %s: %w", checkoutPath, err)
		}
		return fastForward(checkoutPath, defaultBranch)
	}

	if err := os.MkdirAll(filepath.Dir(checkoutPath), 0o755); err != nil {
		return fmt.Errorf("mkdir parent of %s: %w", checkoutPath, err)
	}
	cmd := cloneCommand("gh", "repo", "clone", remote, checkoutPath)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("gh repo clone %s %s: %w\n%s", remote, checkoutPath, err, strings.TrimSpace(string(out)))
	}
	return nil
}

// fastForward checks out defaultBranch and resets it hard onto
// origin/<defaultBranch>. Only safe because checkoutPath is extctl-owned —
// it exists solely to run docker-compose/oCIS for the gate's e2e stage and to
// seed new build worktrees, never to hold a developer's uncommitted work.
func fastForward(checkoutPath, defaultBranch string) error {
	if err := run(checkoutPath, "checkout", defaultBranch); err != nil {
		return err
	}
	return run(checkoutPath, "reset", "--hard", "origin/"+defaultBranch)
}
