package marketplace

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/LukasHirt/extctl/internal/config"
)

func TestCompareVersions(t *testing.T) {
	cases := []struct {
		a, b string
		want int // sign only
	}{
		{"0.1.0", "0.2.0", -1},
		{"0.2.0", "0.10.0", -1}, // numeric, not lexicographic — "0.10.0" > "0.2.0"
		{"1.0.0", "1.0.0", 0},
		{"1.2.0", "1.1.9", 1},
		{"1.0.0-beta", "1.0.0-alpha", 1}, // non-numeric segment falls back to string compare
	}
	for _, c := range cases {
		got := compareVersions(c.a, c.b)
		switch {
		case c.want < 0 && got >= 0:
			t.Errorf("compareVersions(%q, %q) = %d, want negative", c.a, c.b, got)
		case c.want > 0 && got <= 0:
			t.Errorf("compareVersions(%q, %q) = %d, want positive", c.a, c.b, got)
		case c.want == 0 && got != 0:
			t.Errorf("compareVersions(%q, %q) = %d, want 0", c.a, c.b, got)
		}
	}
}

// TestPreviousRelease_ReusesRealPriorRelease reproduces the exact scenario
// the user asked for: an extension that already has a published marketplace
// release must have its tags (and minOCIS) reused verbatim for a new
// version, not re-guessed.
func TestPreviousRelease_ReusesRealPriorRelease(t *testing.T) {
	upstream := initTestRepo(t)
	runGitCmd(t, upstream, "branch", "-M", "master")

	relDir := filepath.Join(upstream, "extensions", "draw-io", "releases", "0.1.0")
	if err := os.MkdirAll(relDir, 0o755); err != nil {
		t.Fatal(err)
	}
	yamlContent := "id: com.github.owncloud.web-extensions.draw-io\n" +
		"minOCIS: 6.2.0\n" +
		"tags:\n  - editor\n  - viewer\n  - diagram\n"
	if err := os.WriteFile(filepath.Join(relDir, "extension.yaml"), []byte(yamlContent), 0o644); err != nil {
		t.Fatal(err)
	}
	runGitCmd(t, upstream, "add", ".")
	runGitCmd(t, upstream, "commit", "-q", "-m", "add draw-io 0.1.0")

	checkout := t.TempDir()
	runGitCmd(t, checkout, "clone", "-q", upstream, ".")

	prev, err := PreviousRelease(checkout, "master", "draw-io")
	if err != nil {
		t.Fatalf("PreviousRelease: %v", err)
	}
	if prev == nil {
		t.Fatal("expected a prior release to be found")
	}
	if !stringSlicesEqual(prev.Tags, []string{"editor", "viewer", "diagram"}) {
		t.Errorf("tags = %+v, want the exact tags from the prior release", prev.Tags)
	}

	// ResolveMinOCIS returns from the previous-release branch immediately
	// without touching cfg, so a zero-value config is fine here.
	minOCIS, source := ResolveMinOCIS(&config.Config{}, "draw-io", prev, func(string, ...any) {})
	if source != MinOCISSourcePreviousRelease {
		t.Errorf("source = %q, want %q", source, MinOCISSourcePreviousRelease)
	}
	if minOCIS != "6.2.0" {
		t.Errorf("minOCIS = %q, want the exact value from the prior release", minOCIS)
	}
}

func TestPreviousRelease_NoPriorRelease(t *testing.T) {
	upstream := initTestRepo(t)
	runGitCmd(t, upstream, "branch", "-M", "master")
	checkout := t.TempDir()
	runGitCmd(t, checkout, "clone", "-q", upstream, ".")

	prev, err := PreviousRelease(checkout, "master", "never-published-ext")
	if err != nil {
		t.Fatalf("PreviousRelease: %v", err)
	}
	if prev != nil {
		t.Errorf("expected nil for an extension with no prior marketplace release, got %+v", prev)
	}

	// prev is nil, so ResolveMinOCIS falls through to InferMinOCISFromHistory
	// — reusing this same checkout as a stand-in for cfg.TargetRepo.Checkout
	// works fine: it has no packages/web-app-never-published-ext/ path at
	// all, so FirstCommitDate errors ("no commits found"), which
	// ResolveMinOCIS logs as a warning and falls through to source "none".
	cfg := &config.Config{TargetRepo: config.TargetRepo{Checkout: checkout}, DefaultBranch: "master"}
	minOCIS, source := ResolveMinOCIS(cfg, "never-published-ext", prev, func(string, ...any) {})
	if source != MinOCISSourceNone || minOCIS != "" {
		t.Errorf("ResolveMinOCIS(nil) = (%q, %q), want (\"\", %q)", minOCIS, source, MinOCISSourceNone)
	}
}

func TestPreviousCoverBytes_NoPriorRelease(t *testing.T) {
	got, err := previousCoverBytes(t.TempDir(), "master", "x", nil)
	if err != nil {
		t.Fatalf("previousCoverBytes: %v", err)
	}
	if got != nil {
		t.Errorf("got %v, want nil (no prior release)", got)
	}
}

func TestPreviousCoverBytes_PriorReleaseHasNoCover(t *testing.T) {
	prev := &ExtensionYAML{Version: "0.1.0", Cover: false}
	got, err := previousCoverBytes(t.TempDir(), "master", "x", prev)
	if err != nil {
		t.Fatalf("previousCoverBytes: %v", err)
	}
	if got != nil {
		t.Errorf("got %v, want nil (prev.Cover is false)", got)
	}
}

// TestPreviousCoverBytes_SmudgesRealLFSContent reproduces the exact bug
// this function exists to avoid: reading a prior release's cover.png must
// yield real image bytes even when the file is stored as a Git LFS
// pointer, never the ~130-byte pointer text itself — which is exactly what
// a naive `git show` (no smudge step) would return, and exactly the class
// of bug owncloud/marketplace#238 reported for bundle.zip/screenshots.
func TestPreviousCoverBytes_SmudgesRealLFSContent(t *testing.T) {
	upstream := initTestRepo(t)
	runGitCmd(t, upstream, "branch", "-M", "master")
	runGitCmd(t, upstream, "lfs", "install", "--local")
	if err := os.WriteFile(filepath.Join(upstream, ".gitattributes"), []byte("*.png filter=lfs diff=lfs merge=lfs -text\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	relDir := filepath.Join(upstream, "extensions", "draw-io", "releases", "0.1.0")
	if err := os.MkdirAll(relDir, 0o755); err != nil {
		t.Fatal(err)
	}
	realContent := []byte("not actually a PNG, just needs to survive the round trip intact")
	if err := os.WriteFile(filepath.Join(relDir, "cover.png"), realContent, 0o644); err != nil {
		t.Fatal(err)
	}
	runGitCmd(t, upstream, "add", ".")
	runGitCmd(t, upstream, "commit", "-q", "-m", "add draw-io 0.1.0 cover")

	// Confirm the setup actually produced an LFS pointer, not a raw blob —
	// otherwise this test would pass without ever exercising the smudge
	// path it's meant to cover.
	committed, err := exec.Command("git", "-C", upstream, "show", "HEAD:extensions/draw-io/releases/0.1.0/cover.png").Output()
	if err != nil {
		t.Fatalf("git show (setup check): %v", err)
	}
	if !bytes.HasPrefix(committed, []byte("version https://git-lfs.github.com/spec/v1")) {
		t.Fatalf("test setup did not produce an LFS pointer — got %q", committed)
	}

	checkout := t.TempDir()
	runGitCmd(t, checkout, "clone", "-q", upstream, ".")
	runGitCmd(t, checkout, "lfs", "install", "--local")

	prev := &ExtensionYAML{Version: "0.1.0", Cover: true}
	got, err := previousCoverBytes(checkout, "master", "draw-io", prev)
	if err != nil {
		t.Fatalf("previousCoverBytes: %v", err)
	}
	if string(got) != string(realContent) {
		t.Errorf("got %q, want the real smudged content %q (not the LFS pointer text)", got, realContent)
	}
}
