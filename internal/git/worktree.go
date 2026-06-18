package git

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// FetchOrigin fetches origin in the given repo so the base branch is current
// before creating a worktree.
func FetchOrigin(repoPath string) error {
	return run(repoPath, "fetch", "origin")
}

// CreateWorktree creates (or reuses) a git worktree at worktreePath on branch.
// Handles three cases from interrupted prior runs:
//   - worktree dir exists → reuse as-is
//   - branch exists but worktree dir doesn't → add without -b
//   - neither exists → create branch and worktree fresh
func CreateWorktree(repoPath, worktreePath, branch, baseBranch string) error {
	if _, err := os.Stat(worktreePath); err == nil {
		return nil // worktree already set up from a prior run
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
