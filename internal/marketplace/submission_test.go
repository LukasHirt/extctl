package marketplace

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func runGitCmd(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func initTestRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	runGitCmd(t, dir, "init", "-q")
	runGitCmd(t, dir, "config", "user.email", "test@example.com")
	runGitCmd(t, dir, "config", "user.name", "Test")
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGitCmd(t, dir, "add", "README.md")
	runGitCmd(t, dir, "commit", "-q", "-m", "init")
	return dir
}

func TestHasStagedChanges(t *testing.T) {
	dir := initTestRepo(t)

	changed, err := hasStagedChanges(dir)
	if err != nil {
		t.Fatalf("hasStagedChanges: %v", err)
	}
	if changed {
		t.Error("expected no staged changes on a clean repo")
	}

	if err := os.WriteFile(filepath.Join(dir, "new.txt"), []byte("data"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGitCmd(t, dir, "add", "new.txt")

	changed, err = hasStagedChanges(dir)
	if err != nil {
		t.Fatalf("hasStagedChanges: %v", err)
	}
	if !changed {
		t.Error("expected staged changes after adding a new file")
	}
}

// TestBuildSubmission_RerunWithIdenticalContentSkipsCommit reproduces the
// bug this fix addresses: a deterministically-named branch
// (publish/<appID>-v<version>) that already carries the exact submission
// content from a prior run must NOT make BuildSubmission fail — it should
// reuse the existing commit rather than erroring on `git commit`'s "nothing
// to commit".
func TestBuildSubmission_RerunWithIdenticalContentSkipsCommit(t *testing.T) {
	upstream := initTestRepo(t)
	checkout := t.TempDir()
	runGitCmd(t, checkout, "clone", "-q", upstream, ".")
	runGitCmd(t, checkout, "config", "user.email", "test@example.com")
	runGitCmd(t, checkout, "config", "user.name", "Test")

	bundleDir := t.TempDir()
	bundlePath := filepath.Join(bundleDir, "bundle.zip")
	if err := os.WriteFile(bundlePath, []byte("fake zip content"), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := testPublishConfig()
	cfg.MarketplaceRepo.DefaultBranch = "master" // git init's default in this environment
	if out, err := exec.Command("git", "-C", checkout, "branch", "-M", "master").CombinedOutput(); err != nil {
		t.Fatalf("rename branch: %v\n%s", err, out)
	}
	if out, err := exec.Command("git", "-C", upstream, "branch", "-M", "master").CombinedOutput(); err != nil {
		t.Fatalf("rename upstream branch: %v\n%s", err, out)
	}
	runGitCmd(t, checkout, "fetch", "origin")

	ext := ExtensionYAML{ID: "com.example.x", Name: "X", License: "AGPL-3.0", Version: "0.1.0", Authors: []Author{{Name: "A"}}, Tags: []string{"extension"}}

	branch1, err := BuildSubmission(cfg, checkout, "x", "0.1.0", ext, bundlePath, nil)
	if err != nil {
		t.Fatalf("first BuildSubmission: %v", err)
	}
	firstHead := gitRevParse(t, checkout, "HEAD")

	// Simulate a rerun (e.g. push/PR-create failed after the first commit
	// succeeded) — same appID/version, same content, same branch name.
	branch2, err := BuildSubmission(cfg, checkout, "x", "0.1.0", ext, bundlePath, nil)
	if err != nil {
		t.Fatalf("second BuildSubmission (rerun): %v", err)
	}
	secondHead := gitRevParse(t, checkout, "HEAD")

	if branch1 != branch2 {
		t.Errorf("branch changed between runs: %q vs %q", branch1, branch2)
	}
	if firstHead != secondHead {
		t.Errorf("expected the rerun to reuse the existing commit (%s), got a new one (%s)", firstHead, secondHead)
	}
}

// TestBranchHasCompleteSubmission distinguishes a fully staged submission
// (extension.yaml committed) from a branch that only got as far as `checkout
// -b` before a prior run crashed — the case Run's --force / auto-heal logic
// needs to tell apart from "already staged, go run approve".
func TestBranchHasCompleteSubmission(t *testing.T) {
	upstream := initTestRepo(t)
	runGitCmd(t, upstream, "branch", "-M", "master")
	checkout := t.TempDir()
	runGitCmd(t, checkout, "clone", "-q", upstream, ".")
	runGitCmd(t, checkout, "config", "user.email", "test@example.com")
	runGitCmd(t, checkout, "config", "user.name", "Test")
	runGitCmd(t, checkout, "fetch", "origin")

	// A branch that exists but never got a submission committed to it.
	runGitCmd(t, checkout, "checkout", "-b", "publish/x-v0.1.0")
	if branchHasCompleteSubmission(checkout, "publish/x-v0.1.0", "x", "0.1.0") {
		t.Error("expected an incomplete branch (no extension.yaml committed) to report false")
	}

	cfg := testPublishConfig()
	cfg.MarketplaceRepo.DefaultBranch = "master"
	bundleDir := t.TempDir()
	bundlePath := filepath.Join(bundleDir, "bundle.zip")
	if err := os.WriteFile(bundlePath, []byte("fake zip content"), 0o644); err != nil {
		t.Fatal(err)
	}
	ext := ExtensionYAML{ID: "com.example.x", License: "AGPL-3.0", Version: "0.1.0", Tags: []string{"extension"}}
	if _, err := BuildSubmission(cfg, checkout, "x", "0.1.0", ext, bundlePath, nil); err != nil {
		t.Fatalf("BuildSubmission: %v", err)
	}
	if !branchHasCompleteSubmission(checkout, "publish/x-v0.1.0", "x", "0.1.0") {
		t.Error("expected a branch with a committed extension.yaml to report true")
	}
}

func TestDeleteLocalBranch(t *testing.T) {
	checkout := initTestRepo(t)
	runGitCmd(t, checkout, "checkout", "-b", "publish/x-v0.1.0")
	runGitCmd(t, checkout, "checkout", "-")

	if !branchExistsLocally(checkout, "publish/x-v0.1.0") {
		t.Fatal("expected branch to exist before deletion")
	}
	if err := deleteLocalBranch(checkout, "publish/x-v0.1.0"); err != nil {
		t.Fatalf("deleteLocalBranch: %v", err)
	}
	if branchExistsLocally(checkout, "publish/x-v0.1.0") {
		t.Error("expected branch to be gone after deleteLocalBranch")
	}
}

func gitRevParse(t *testing.T, dir, ref string) string {
	t.Helper()
	out, err := exec.Command("git", "-C", dir, "rev-parse", ref).Output()
	if err != nil {
		t.Fatalf("git rev-parse %s: %v", ref, err)
	}
	return string(out)
}

// TestAmendSubmissionScreenshots_ReplacesContentWithoutNewCommit covers the
// core of `extctl publish retry-screenshots`: recapturing must land on the
// SAME commit BuildSubmission made (amend, not a second commit), replace
// the screenshots/ dir wholesale, and leave every other extension.yaml
// field (license, tags, ...) untouched.
func TestAmendSubmissionScreenshots_ReplacesContentWithoutNewCommit(t *testing.T) {
	upstream := initTestRepo(t)
	runGitCmd(t, upstream, "branch", "-M", "master")
	checkout := t.TempDir()
	runGitCmd(t, checkout, "clone", "-q", upstream, ".")
	runGitCmd(t, checkout, "config", "user.email", "test@example.com")
	runGitCmd(t, checkout, "config", "user.name", "Test")
	runGitCmd(t, checkout, "fetch", "origin")

	cfg := testPublishConfig()
	cfg.MarketplaceRepo.DefaultBranch = "master"

	bundleDir := t.TempDir()
	bundlePath := filepath.Join(bundleDir, "bundle.zip")
	if err := os.WriteFile(bundlePath, []byte("fake zip content"), 0o644); err != nil {
		t.Fatal(err)
	}

	ext := ExtensionYAML{ID: "com.example.x", License: "AGPL-3.0", Version: "0.1.0", Tags: []string{"extension"},
		ScreenshotCaptions: []string{"old caption"}}
	if _, err := BuildSubmission(cfg, checkout, "x", "0.1.0", ext, bundlePath, nil); err != nil {
		t.Fatalf("BuildSubmission: %v", err)
	}
	firstHead := gitRevParse(t, checkout, "HEAD")

	shotDir := t.TempDir()
	shotPath := filepath.Join(shotDir, "01.png")
	if err := os.WriteFile(shotPath, []byte("fake png"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := AmendSubmissionScreenshots(checkout, "x", "0.1.0", []string{shotPath}, []string{"new caption"}); err != nil {
		t.Fatalf("AmendSubmissionScreenshots: %v", err)
	}
	secondHead := gitRevParse(t, checkout, "HEAD")

	if firstHead == secondHead {
		t.Error("expected the amend to produce a different commit hash (content changed)")
	}
	log, err := exec.Command("git", "-C", checkout, "log", "--oneline", "publish/x-v0.1.0").Output()
	if err != nil {
		t.Fatalf("git log: %v", err)
	}
	// Amend must not add a second commit — one commit on this branch beyond
	// whatever master already had (initTestRepo's one "init" commit).
	lines := strings.Split(strings.TrimSpace(string(log)), "\n")
	if len(lines) != 2 {
		t.Errorf("expected exactly 2 commits on publish/x-v0.1.0 (init + one submission commit), got %d:\n%s", len(lines), log)
	}

	got, err := readExtensionYAMLFile(filepath.Join(checkout, "extensions/x/releases/0.1.0/extension.yaml"))
	if err != nil {
		t.Fatalf("readExtensionYAMLFile: %v", err)
	}
	if !stringSlicesEqual(got.ScreenshotCaptions, []string{"new caption"}) {
		t.Errorf("ScreenshotCaptions = %+v, want [new caption]", got.ScreenshotCaptions)
	}
	if got.License != "AGPL-3.0" || !stringSlicesEqual(got.Tags, []string{"extension"}) {
		t.Errorf("unrelated fields should be untouched, got License=%q Tags=%+v", got.License, got.Tags)
	}
	if _, err := os.Stat(filepath.Join(checkout, "extensions/x/releases/0.1.0/screenshots/01.png")); err != nil {
		t.Errorf("expected the new screenshot file to be committed: %v", err)
	}
}

// TestAmendSubmissionScreenshots_NoNewScreenshotsIsANoop covers a retry that
// still comes up empty: it must not error, and must not create a spurious
// empty commit (nothing actually changed vs. the already-committed state).
func TestAmendSubmissionScreenshots_NoNewScreenshotsIsANoop(t *testing.T) {
	upstream := initTestRepo(t)
	runGitCmd(t, upstream, "branch", "-M", "master")
	checkout := t.TempDir()
	runGitCmd(t, checkout, "clone", "-q", upstream, ".")
	runGitCmd(t, checkout, "config", "user.email", "test@example.com")
	runGitCmd(t, checkout, "config", "user.name", "Test")
	runGitCmd(t, checkout, "fetch", "origin")

	cfg := testPublishConfig()
	cfg.MarketplaceRepo.DefaultBranch = "master"

	bundleDir := t.TempDir()
	bundlePath := filepath.Join(bundleDir, "bundle.zip")
	if err := os.WriteFile(bundlePath, []byte("fake zip content"), 0o644); err != nil {
		t.Fatal(err)
	}

	ext := ExtensionYAML{ID: "com.example.x", License: "AGPL-3.0", Version: "0.1.0", Tags: []string{"extension"}}
	if _, err := BuildSubmission(cfg, checkout, "x", "0.1.0", ext, bundlePath, nil); err != nil {
		t.Fatalf("BuildSubmission: %v", err)
	}
	firstHead := gitRevParse(t, checkout, "HEAD")

	if err := AmendSubmissionScreenshots(checkout, "x", "0.1.0", nil, nil); err != nil {
		t.Fatalf("AmendSubmissionScreenshots: %v", err)
	}
	secondHead := gitRevParse(t, checkout, "HEAD")

	if firstHead != secondHead {
		t.Error("expected no new commit when nothing actually changed")
	}
}
