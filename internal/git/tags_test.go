package git

import (
	"strings"
	"testing"
)

func TestOutput(t *testing.T) {
	repo := initRepo(t)
	commitFile(t, repo, "a.txt", "hi", "initial commit")

	out, err := Output(repo, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		t.Fatalf("Output: %v", err)
	}
	if strings.TrimSpace(string(out)) == "" {
		t.Error("expected branch name, got empty output")
	}
}

func TestOutput_Error(t *testing.T) {
	repo := initRepo(t)
	if _, err := Output(repo, "show", "definitely-not-a-ref"); err == nil {
		t.Error("expected error for bad ref, got nil")
	}
}

func TestPushTags_NoOp(t *testing.T) {
	repo := initRepo(t)
	// No tags passed — must be a no-op and not invoke git push (no remote here).
	if err := PushTags(repo); err != nil {
		t.Fatalf("PushTags with no tags should be a no-op: %v", err)
	}
}

func TestFetchTags(t *testing.T) {
	remote := initBareRemote(t)
	repo := initRepo(t)
	commitFile(t, repo, "a.txt", "init", "initial commit")
	mustGit(t, repo, "remote", "add", "origin", remote)

	if err := FetchTags(repo); err != nil {
		t.Fatalf("FetchTags: %v", err)
	}
}

func TestPushTags(t *testing.T) {
	remote := initBareRemote(t)
	repo := initRepo(t)
	commitFile(t, repo, "a.txt", "init", "initial commit")
	mustGit(t, repo, "remote", "add", "origin", remote)
	mustGit(t, repo, "push", "origin", defaultBranch(t, repo))

	// Lightweight (unsigned) tag so the test needs no signing key, even when the
	// host git config defaults to signed/annotated tags.
	mustGit(t, repo, "-c", "tag.gpgsign=false", "tag", "alpha-v0.1.0")
	if err := PushTags(repo, "alpha-v0.1.0"); err != nil {
		t.Fatalf("PushTags: %v", err)
	}

	out, err := Output(remote, "tag", "-l", "alpha-v0.1.0")
	if err != nil {
		t.Fatalf("list remote tags: %v", err)
	}
	if strings.TrimSpace(string(out)) != "alpha-v0.1.0" {
		t.Errorf("tag not pushed to remote, got %q", strings.TrimSpace(string(out)))
	}
}
