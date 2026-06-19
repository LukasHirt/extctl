package git

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// FetchOrigin fetches origin in the given repo so the base branch is current
// before creating a worktree.
func FetchOrigin(repoPath string) error {
	return run(repoPath, "fetch", "origin")
}

// CreateWorktree creates (or reuses) a git worktree at worktreePath on branch.
// Handles three cases from interrupted prior runs:
//   - valid worktree dir exists (.git present) → reuse as-is
//   - dir exists but no .git (stale from a removed worktree) → remove and recreate
//   - branch exists but worktree dir doesn't → add without -b
//   - neither exists → create branch and worktree fresh
func CreateWorktree(repoPath, worktreePath, branch, baseBranch string) error {
	if _, err := os.Stat(worktreePath); err == nil {
		if _, gitErr := os.Stat(filepath.Join(worktreePath, ".git")); gitErr == nil {
			return nil // valid worktree from a prior run
		}
		// Directory exists but has no .git — stale from git worktree remove.
		if err := os.RemoveAll(worktreePath); err != nil {
			return fmt.Errorf("remove stale worktree directory: %w", err)
		}
	}
	if branchExists(repoPath, branch) {
		return run(repoPath, "worktree", "add", worktreePath, branch)
	}
	return run(repoPath, "worktree", "add", worktreePath, "-b", branch, baseBranch)
}

func branchExists(repoPath, branch string) bool {
	out, err := exec.Command("git", "-C", repoPath, "branch", "--list", branch).Output()
	return err == nil && len(strings.TrimSpace(string(out))) > 0
}

// RemoveWorktree removes the worktree at worktreePath from the given repo.
func RemoveWorktree(repoPath, worktreePath string) error {
	return run(repoPath, "worktree", "remove", "--force", worktreePath)
}

// PushBranch pushes a branch to origin.
func PushBranch(repoPath, branch string) error {
	return run(repoPath, "push", "-u", "origin", branch)
}

func run(repoPath string, args ...string) error {
	cmd := exec.Command("git", append([]string{"-C", repoPath}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git %s: %w\n%s", strings.Join(args[:1], ""), err, strings.TrimSpace(string(out)))
	}
	return nil
}
