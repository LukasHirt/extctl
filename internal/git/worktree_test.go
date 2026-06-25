package git

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// initRepo creates a minimal git repo in a temp dir and returns its path.
func initRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	mustGit(t, dir, "init")
	mustGit(t, dir, "config", "user.email", "test@test.com")
	mustGit(t, dir, "config", "user.name", "Test")
	return dir
}

func mustGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// commitFile creates a file and commits it in dir.
func commitFile(t *testing.T, dir, name, content, msg string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	mustGit(t, dir, "add", name)
	mustGit(t, dir, "commit", "-m", msg)
}

// initBareRemote creates a bare remote repo and returns its path.
func initBareRemote(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	mustGit(t, dir, "init", "--bare")
	return dir
}

func TestCreateWorktree_New(t *testing.T) {
	repo := initRepo(t)
	commitFile(t, repo, "a.txt", "init", "initial commit")

	worktreePath := filepath.Join(t.TempDir(), "wt")
	if err := CreateWorktree(repo, worktreePath, "feature/test", "master"); err != nil {
		// Try "main" as the default branch name.
		if err2 := CreateWorktree(repo, worktreePath, "feature/test", "main"); err2 != nil {
			t.Fatalf("CreateWorktree: %v (also tried main: %v)", err, err2)
		}
	}

	if _, err := os.Stat(worktreePath); err != nil {
		t.Errorf("worktree dir not created: %v", err)
	}
	if _, err := os.Stat(filepath.Join(worktreePath, ".git")); err != nil {
		t.Errorf("worktree .git not found: %v", err)
	}
}

func TestCreateWorktree_Idempotent(t *testing.T) {
	repo := initRepo(t)
	commitFile(t, repo, "a.txt", "init", "initial commit")

	worktreePath := filepath.Join(t.TempDir(), "wt")
	branch := "feature/idempotent"
	baseBranch := defaultBranch(t, repo)

	if err := CreateWorktree(repo, worktreePath, branch, baseBranch); err != nil {
		t.Fatalf("first CreateWorktree: %v", err)
	}
	// Call again — should succeed without error.
	if err := CreateWorktree(repo, worktreePath, branch, baseBranch); err != nil {
		t.Fatalf("second CreateWorktree (idempotent): %v", err)
	}
}

func TestCreateWorktree_StaleDir(t *testing.T) {
	repo := initRepo(t)
	commitFile(t, repo, "a.txt", "init", "initial commit")

	worktreePath := filepath.Join(t.TempDir(), "wt")
	// Create directory without .git — simulates a stale unregistered dir.
	if err := os.MkdirAll(worktreePath, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(worktreePath, "stale.txt"), []byte("stale"), 0o644); err != nil {
		t.Fatal(err)
	}

	branch := "feature/stale"
	baseBranch := defaultBranch(t, repo)
	if err := CreateWorktree(repo, worktreePath, branch, baseBranch); err != nil {
		t.Fatalf("CreateWorktree with stale dir: %v", err)
	}
	// Stale dir should have been replaced with a real worktree.
	if _, err := os.Stat(filepath.Join(worktreePath, ".git")); err != nil {
		t.Errorf("worktree .git not found after stale cleanup: %v", err)
	}
}

func TestRemoveWorktree(t *testing.T) {
	repo := initRepo(t)
	commitFile(t, repo, "a.txt", "init", "initial commit")

	worktreePath := filepath.Join(t.TempDir(), "wt")
	branch := "feature/remove"
	baseBranch := defaultBranch(t, repo)
	if err := CreateWorktree(repo, worktreePath, branch, baseBranch); err != nil {
		t.Fatalf("CreateWorktree: %v", err)
	}

	if err := RemoveWorktree(repo, worktreePath); err != nil {
		t.Fatalf("RemoveWorktree: %v", err)
	}
	if _, err := os.Stat(worktreePath); err == nil {
		t.Error("worktree path still exists after remove")
	}
}

func TestPushBranch(t *testing.T) {
	remote := initBareRemote(t)
	repo := initRepo(t)
	commitFile(t, repo, "a.txt", "init", "initial commit")
	mustGit(t, repo, "remote", "add", "origin", remote)

	baseBranch := defaultBranch(t, repo)
	// Push base branch first so remote has at least one commit.
	mustGit(t, repo, "push", "origin", baseBranch)

	worktreePath := filepath.Join(t.TempDir(), "wt")
	branch := "feature/push"
	if err := CreateWorktree(repo, worktreePath, branch, baseBranch); err != nil {
		t.Fatalf("CreateWorktree: %v", err)
	}
	commitFile(t, worktreePath, "b.txt", "feature", "feat: add feature")

	if err := PushBranch(repo, branch); err != nil {
		t.Fatalf("PushBranch: %v", err)
	}

	// Verify branch exists in remote.
	cmd := exec.Command("git", "branch", "--list", branch)
	cmd.Dir = remote
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git branch --list in remote: %v", err)
	}
	if len(out) == 0 {
		t.Errorf("branch %q not found in remote after push", branch)
	}
}

func TestFetchOrigin(t *testing.T) {
	remote := initBareRemote(t)
	// Add initial commit to remote by cloning and pushing.
	cloneDir := t.TempDir()
	mustGit(t, t.TempDir(), "clone", remote, cloneDir)
	// That doesn't work easily; use a different setup.
	// Just test FetchOrigin doesn't error when remote is empty.
	repo := initRepo(t)
	commitFile(t, repo, "a.txt", "init", "initial commit")
	mustGit(t, repo, "remote", "add", "origin", remote)

	// Should not error — remote is empty but accessible.
	if err := FetchOrigin(repo); err != nil {
		t.Fatalf("FetchOrigin: %v", err)
	}
}

// defaultBranch returns the current HEAD branch name (master or main).
func defaultBranch(t *testing.T, repo string) string {
	t.Helper()
	cmd := exec.Command("git", "rev-parse", "--abbrev-ref", "HEAD")
	cmd.Dir = repo
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("get default branch: %v", err)
	}
	branch := string(out)
	for len(branch) > 0 && (branch[len(branch)-1] == '\n' || branch[len(branch)-1] == '\r') {
		branch = branch[:len(branch)-1]
	}
	return branch
}
