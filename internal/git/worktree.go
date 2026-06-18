package git

import (
	"fmt"
	"os/exec"
	"strings"
)

// FetchOrigin fetches origin in the given repo so the base branch is current
// before creating a worktree.
func FetchOrigin(repoPath string) error {
	return run(repoPath, "fetch", "origin")
}

// CreateWorktree creates a git worktree at worktreePath on a new branch
// branched from baseBranch (e.g. "origin/main").
// Branch name convention: ext/<date>-<id>
func CreateWorktree(repoPath, worktreePath, branch, baseBranch string) error {
	return run(repoPath, "worktree", "add", worktreePath, "-b", branch, baseBranch)
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
