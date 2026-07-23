package marketplace

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func mustParseTime(t *testing.T, s string) time.Time {
	t.Helper()
	ts, err := time.Parse(time.RFC3339, s)
	if err != nil {
		t.Fatal(err)
	}
	return ts
}

func TestPickLatestStableBefore(t *testing.T) {
	// Mirrors the real oCIS release shape verified this session: v8.0.4
	// (stable) published 2026-05-22, v8.0.5 (stable) four days after a
	// hypothetical cutoff, plus a prerelease that must be ignored even
	// though it's chronologically closer to the cutoff.
	releases := []ocisRelease{
		{TagName: "v8.0.4", PublishedAt: "2026-05-22T08:59:28Z", Draft: false, Prerelease: false},
		{TagName: "v8.0.5", PublishedAt: "2026-06-19T10:06:37Z", Draft: false, Prerelease: false},
		{TagName: "v8.1.0-rc.1", PublishedAt: "2026-06-14T00:00:00Z", Draft: false, Prerelease: true},
		{TagName: "v8.0.3", PublishedAt: "2026-05-11T15:54:01Z", Draft: false, Prerelease: false},
		{TagName: "v8.0.6-draft", PublishedAt: "2026-06-01T00:00:00Z", Draft: true, Prerelease: false},
	}

	cutoff := mustParseTime(t, "2026-06-15T22:01:45+02:00") // ai-doc-summary's real first commit
	if got := pickLatestStableBefore(releases, cutoff); got != "8.0.4" {
		t.Errorf("pickLatestStableBefore = %q, want 8.0.4 (v8.0.5 is after cutoff, prerelease/draft excluded)", got)
	}
}

func TestPickLatestStableBefore_NoneQualify(t *testing.T) {
	releases := []ocisRelease{
		{TagName: "v8.0.4", PublishedAt: "2026-05-22T08:59:28Z", Draft: false, Prerelease: false},
	}
	cutoff := mustParseTime(t, "2020-01-01T00:00:00Z")
	if got := pickLatestStableBefore(releases, cutoff); got != "" {
		t.Errorf("pickLatestStableBefore = %q, want empty (cutoff predates every release)", got)
	}
}

func TestPickLatestStableBefore_PicksHighestNotLatestPublished(t *testing.T) {
	// Two stable releases both before cutoff — must pick the higher
	// version, not just whichever appears first in the slice.
	releases := []ocisRelease{
		{TagName: "v7.3.2", PublishedAt: "2026-02-05T15:02:07Z", Draft: false, Prerelease: false},
		{TagName: "v8.0.0", PublishedAt: "2026-02-16T09:24:55Z", Draft: false, Prerelease: false},
	}
	cutoff := mustParseTime(t, "2026-03-01T00:00:00Z")
	if got := pickLatestStableBefore(releases, cutoff); got != "8.0.0" {
		t.Errorf("pickLatestStableBefore = %q, want 8.0.0", got)
	}
}

func TestFirstCommitDate(t *testing.T) {
	upstream := initTestRepo(t)
	runGitCmd(t, upstream, "branch", "-M", "master")

	pkgDir := filepath.Join(upstream, "packages", "web-app-some-ext")
	if err := os.MkdirAll(pkgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pkgDir, "package.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGitCmd(t, upstream, "add", ".")
	// --date sets the AUTHOR date, which is what FirstCommitDate reads (%aI).
	runGitCmd(t, upstream, "commit", "-q", "-m", "add some-ext", "--date=2026-06-15T22:01:45+02:00")

	// A later, unrelated commit must not affect the result.
	if err := os.WriteFile(filepath.Join(upstream, "other.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGitCmd(t, upstream, "add", "other.txt")
	runGitCmd(t, upstream, "commit", "-q", "-m", "unrelated")

	checkout := t.TempDir()
	runGitCmd(t, checkout, "clone", "-q", upstream, ".")

	got, err := FirstCommitDate(checkout, "master", "some-ext")
	if err != nil {
		t.Fatalf("FirstCommitDate: %v", err)
	}
	want := mustParseTime(t, "2026-06-15T22:01:45+02:00")
	if !got.Equal(want) {
		t.Errorf("FirstCommitDate = %v, want %v", got, want)
	}
}

func TestFirstCommitDate_NoCommits(t *testing.T) {
	upstream := initTestRepo(t)
	runGitCmd(t, upstream, "branch", "-M", "master")
	checkout := t.TempDir()
	runGitCmd(t, checkout, "clone", "-q", upstream, ".")

	if _, err := FirstCommitDate(checkout, "master", "never-existed"); err == nil {
		t.Error("expected an error when the extension's path has no commits at all")
	}
}
