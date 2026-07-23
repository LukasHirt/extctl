package marketplace

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseIDArg(t *testing.T) {
	cases := []struct {
		in          string
		wantID      string
		wantVersion string
	}{
		{"draw-io", "draw-io", ""},
		{"draw-io@0.2.0", "draw-io", "0.2.0"},
		{"web-app-v-thing@1.0.0", "web-app-v-thing", "1.0.0"}, // "@" split, not "-v" — no ambiguity with splitTag's concern
	}
	for _, c := range cases {
		id, version := parseIDArg(c.in)
		if id != c.wantID || version != c.wantVersion {
			t.Errorf("parseIDArg(%q) = (%q, %q), want (%q, %q)", c.in, id, version, c.wantID, c.wantVersion)
		}
	}
}

// newPendingBranch creates a publish/<appID>-v<version> branch in checkout
// with a trivial commit, mirroring the shape BuildSubmission leaves behind
// (a branch off the default branch with one commit), without needing the
// full BuildSubmission machinery.
func newPendingBranch(t *testing.T, checkout, baseBranch, appID, version string) {
	t.Helper()
	// A cloned repo doesn't inherit its source's local git config (identity
	// set via `git config` without --global lives in .git/config, which
	// `git clone` doesn't copy) — commit fails here on any machine/CI
	// runner without a global user.email/user.name configured.
	runGitCmd(t, checkout, "config", "user.email", "test@example.com")
	runGitCmd(t, checkout, "config", "user.name", "Test")
	runGitCmd(t, checkout, "checkout", baseBranch)
	branch := "publish/" + appID + "-v" + version
	runGitCmd(t, checkout, "checkout", "-b", branch)
	relDir := filepath.Join(checkout, "extensions", appID, "releases", version)
	if err := os.MkdirAll(relDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(relDir, "extension.yaml"), []byte("id: x\nversion: "+version+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGitCmd(t, checkout, "add", ".")
	runGitCmd(t, checkout, "commit", "-q", "-m", "stage "+appID+"@"+version)
	runGitCmd(t, checkout, "checkout", baseBranch)
}

func TestFindPendingVersions(t *testing.T) {
	upstream := initTestRepo(t)
	runGitCmd(t, upstream, "branch", "-M", "master")
	checkout := t.TempDir()
	runGitCmd(t, checkout, "clone", "-q", upstream, ".")

	newPendingBranch(t, checkout, "master", "my-ext", "0.2.0")
	newPendingBranch(t, checkout, "master", "my-ext", "0.10.0")
	newPendingBranch(t, checkout, "master", "my-ext", "0.1.0")
	newPendingBranch(t, checkout, "master", "other-ext", "1.0.0")

	versions, err := findPendingVersions(checkout, "my-ext")
	if err != nil {
		t.Fatalf("findPendingVersions: %v", err)
	}
	want := []string{"0.1.0", "0.2.0", "0.10.0"} // numeric sort, not lexicographic
	if !stringSlicesEqual(versions, want) {
		t.Errorf("findPendingVersions = %+v, want %+v", versions, want)
	}

	none, err := findPendingVersions(checkout, "never-staged")
	if err != nil {
		t.Fatalf("findPendingVersions: %v", err)
	}
	if len(none) != 0 {
		t.Errorf("findPendingVersions(never-staged) = %+v, want none", none)
	}
}

func TestResolvePendingBranch(t *testing.T) {
	upstream := initTestRepo(t)
	runGitCmd(t, upstream, "branch", "-M", "master")
	checkout := t.TempDir()
	runGitCmd(t, checkout, "clone", "-q", upstream, ".")

	newPendingBranch(t, checkout, "master", "single-ext", "0.1.0")
	newPendingBranch(t, checkout, "master", "multi-ext", "0.1.0")
	newPendingBranch(t, checkout, "master", "multi-ext", "0.2.0")

	t.Run("unambiguous bare id", func(t *testing.T) {
		appID, version, branch, err := ResolvePendingBranch(checkout, "single-ext")
		if err != nil {
			t.Fatalf("ResolvePendingBranch: %v", err)
		}
		if appID != "single-ext" || version != "0.1.0" || branch != "publish/single-ext-v0.1.0" {
			t.Errorf("got (%q, %q, %q)", appID, version, branch)
		}
	})

	t.Run("ambiguous bare id errors", func(t *testing.T) {
		_, _, _, err := ResolvePendingBranch(checkout, "multi-ext")
		if err == nil {
			t.Fatal("expected an error when multiple versions are pending")
		}
	})

	t.Run("explicit version disambiguates", func(t *testing.T) {
		appID, version, branch, err := ResolvePendingBranch(checkout, "multi-ext@0.2.0")
		if err != nil {
			t.Fatalf("ResolvePendingBranch: %v", err)
		}
		if appID != "multi-ext" || version != "0.2.0" || branch != "publish/multi-ext-v0.2.0" {
			t.Errorf("got (%q, %q, %q)", appID, version, branch)
		}
	})

	t.Run("nothing pending errors", func(t *testing.T) {
		_, _, _, err := ResolvePendingBranch(checkout, "never-staged")
		if err == nil {
			t.Fatal("expected an error when nothing is pending for this app-id")
		}
	})

	t.Run("explicit version that doesn't exist errors", func(t *testing.T) {
		_, _, _, err := ResolvePendingBranch(checkout, "single-ext@9.9.9")
		if err == nil {
			t.Fatal("expected an error for a version with no pending branch")
		}
	})
}
