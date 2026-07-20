package git

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestEnsureCheckout_ClonesWhenMissing(t *testing.T) {
	remote := initBareRemote(t)
	seed := filepath.Join(t.TempDir(), "seed")
	mustGit(t, t.TempDir(), "clone", remote, seed)
	commitFile(t, seed, "a.txt", "init", "initial commit")
	base := defaultBranch(t, seed)
	mustGit(t, seed, "push", "origin", base)

	// Swap in a real `git clone` against the local bare "remote" instead of
	// shelling out to `gh`, which needs real auth and network access.
	orig := cloneCommand
	defer func() { cloneCommand = orig }()
	cloneCommand = func(name string, args ...string) *exec.Cmd {
		return exec.Command("git", "clone", remote, args[len(args)-1])
	}

	checkoutPath := filepath.Join(t.TempDir(), "checkout")
	if err := EnsureCheckout("owner/repo", checkoutPath, base); err != nil {
		t.Fatalf("EnsureCheckout: %v", err)
	}
	if _, err := os.Stat(filepath.Join(checkoutPath, ".git")); err != nil {
		t.Errorf("expected checkout to be cloned: %v", err)
	}
	if _, err := os.Stat(filepath.Join(checkoutPath, "a.txt")); err != nil {
		t.Errorf("expected cloned checkout to contain remote's files: %v", err)
	}
}

func TestEnsureCheckout_FetchesAndResetsWhenPresent(t *testing.T) {
	remote := initBareRemote(t)
	checkout := initRepo(t)
	commitFile(t, checkout, "a.txt", "init", "initial commit")
	mustGit(t, checkout, "remote", "add", "origin", remote)
	base := defaultBranch(t, checkout)
	mustGit(t, checkout, "push", "origin", base)

	// Simulate local drift that origin never saw.
	commitFile(t, checkout, "local-only.txt", "local", "local: should be discarded")

	// Advance origin independently, as if another process/PR moved it on.
	otherClone := filepath.Join(t.TempDir(), "other")
	mustGit(t, t.TempDir(), "clone", remote, otherClone)
	commitFile(t, otherClone, "b.txt", "remote", "remote: advance origin")
	mustGit(t, otherClone, "push", "origin", base)

	if err := EnsureCheckout("owner/repo", checkout, base); err != nil {
		t.Fatalf("EnsureCheckout: %v", err)
	}
	if _, err := os.Stat(filepath.Join(checkout, "b.txt")); err != nil {
		t.Errorf("expected checkout fast-forwarded to include origin's new commit: %v", err)
	}
	if _, err := os.Stat(filepath.Join(checkout, "local-only.txt")); err == nil {
		t.Error("expected local-only commit to be discarded by reset --hard")
	}
}
