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

	// Lightweight (unsigned) tags so the test needs no signing key, even when the
	// host git config defaults to signed/annotated tags. Push more than three at
	// once to cover the case that motivated per-tag pushes (GitHub drops the push
	// event when >3 tags arrive in a single push).
	want := []string{"alpha-v0.1.0", "beta-v0.1.0", "gamma-v0.1.0", "delta-v0.1.0"}
	for _, tag := range want {
		mustGit(t, repo, "-c", "tag.gpgsign=false", "tag", tag)
	}
	if err := PushTags(repo, want...); err != nil {
		t.Fatalf("PushTags: %v", err)
	}

	for _, tag := range want {
		out, err := Output(remote, "tag", "-l", tag)
		if err != nil {
			t.Fatalf("list remote tags: %v", err)
		}
		if strings.TrimSpace(string(out)) != tag {
			t.Errorf("tag %s not pushed to remote, got %q", tag, strings.TrimSpace(string(out)))
		}
	}
}
