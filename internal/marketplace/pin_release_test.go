package marketplace

import (
	"os"
	"path/filepath"
	"testing"
)

// TestPinExtensionSourceToRelease_SwitchesThenRestores reproduces the bug
// this fix addresses: GenerateScreenshotSpec must see the extension's
// source as it was AT THE RELEASE being screenshotted, not whatever the
// default branch has moved on to since — otherwise it writes selectors
// against markup the release's dist/ never had.
func TestPinExtensionSourceToRelease_SwitchesThenRestores(t *testing.T) {
	upstream := initTestRepo(t)
	extDir := filepath.Join(upstream, "packages", "web-app-x")
	if err := os.MkdirAll(extDir, 0o755); err != nil {
		t.Fatal(err)
	}
	srcPath := filepath.Join(extDir, "marker.txt")

	if err := os.WriteFile(srcPath, []byte("old-markup"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGitCmd(t, upstream, "add", "packages/web-app-x/marker.txt")
	runGitCmd(t, upstream, "commit", "-q", "-m", "release commit")
	runGitCmd(t, upstream, "-c", "tag.gpgsign=false", "tag", "x-v0.1.0")

	if err := os.WriteFile(srcPath, []byte("new-markup"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGitCmd(t, upstream, "add", "packages/web-app-x/marker.txt")
	runGitCmd(t, upstream, "commit", "-q", "-m", "refactor after release")

	checkout := t.TempDir()
	runGitCmd(t, checkout, "clone", "-q", upstream, ".")

	restore, err := pinExtensionSourceToRelease(checkout, "x", "x-v0.1.0")
	if err != nil {
		t.Fatalf("pinExtensionSourceToRelease: %v", err)
	}

	got, err := os.ReadFile(filepath.Join(checkout, "packages", "web-app-x", "marker.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "old-markup" {
		t.Errorf("after pinning to x-v0.1.0, marker.txt = %q, want %q", got, "old-markup")
	}

	if err := restore(); err != nil {
		t.Fatalf("restore: %v", err)
	}
	got, err = os.ReadFile(filepath.Join(checkout, "packages", "web-app-x", "marker.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "new-markup" {
		t.Errorf("after restore, marker.txt = %q, want %q", got, "new-markup")
	}
}
