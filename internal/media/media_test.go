package media

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/LukasHirt/extctl/internal/config"
)

func TestPickScreenshots(t *testing.T) {
	paths := []string{"a", "b", "c", "d", "e"}

	tests := []struct {
		name string
		max  int
		want []string
	}{
		{"max <= 0 returns nil", 0, nil},
		{"fewer than max returns all", 10, paths},
		{"max == 1 picks the middle", 1, []string{"c"}},
		{"spreads evenly across the set", 3, []string{"a", "c", "e"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := pickScreenshots(paths, tt.max)
			if len(got) != len(tt.want) {
				t.Fatalf("pickScreenshots(%v, %d) = %v, want %v", paths, tt.max, got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Fatalf("pickScreenshots(%v, %d) = %v, want %v", paths, tt.max, got, tt.want)
				}
			}
		})
	}

	if got := pickScreenshots(nil, 3); got != nil {
		t.Fatalf("pickScreenshots(nil, 3) = %v, want nil", got)
	}
}

func TestCollectArtifacts_MissingDir(t *testing.T) {
	videos, screenshots, err := collectArtifacts(filepath.Join(t.TempDir(), "does-not-exist"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if videos != nil || screenshots != nil {
		t.Fatalf("expected nil, nil for a missing dir, got videos=%v screenshots=%v", videos, screenshots)
	}
}

func TestCollectArtifacts_OrdersByModTime(t *testing.T) {
	dir := t.TempDir()
	testDirs := []string{"test-a", "test-b", "test-c"}
	base := time.Now().Add(-time.Hour)

	// Write in reverse chronological order so a naive lexical/creation-order
	// read would get it wrong; only explicit ModTime ordering is correct.
	for i := len(testDirs) - 1; i >= 0; i-- {
		sub := filepath.Join(dir, testDirs[i])
		if err := os.MkdirAll(sub, 0o755); err != nil {
			t.Fatal(err)
		}
		png := filepath.Join(sub, "test-finished-1.png")
		if err := os.WriteFile(png, []byte("png"), 0o644); err != nil {
			t.Fatal(err)
		}
		video := filepath.Join(sub, "video.webm")
		if err := os.WriteFile(video, []byte("webm"), 0o644); err != nil {
			t.Fatal(err)
		}
		mt := base.Add(time.Duration(i) * time.Minute)
		if err := os.Chtimes(png, mt, mt); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(video, mt, mt); err != nil {
			t.Fatal(err)
		}
	}

	videos, screenshots, err := collectArtifacts(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(videos) != 3 || len(screenshots) != 3 {
		t.Fatalf("expected 3 videos and 3 screenshots, got %d videos, %d screenshots", len(videos), len(screenshots))
	}
	wantOrder := []string{"test-a", "test-b", "test-c"}
	for i, want := range wantOrder {
		if filepath.Base(filepath.Dir(videos[i])) != want {
			t.Errorf("videos[%d] = %s, want dir %s", i, videos[i], want)
		}
		if filepath.Base(filepath.Dir(screenshots[i])) != want {
			t.Errorf("screenshots[%d] = %s, want dir %s", i, screenshots[i], want)
		}
	}
}

func TestGenerate_Disabled(t *testing.T) {
	disabled := false
	cfg := &config.Config{Media: config.Media{Enabled: &disabled}}
	result, err := Generate(cfg, t.TempDir(), "foo", t.TempDir())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != nil {
		t.Fatalf("expected nil result when disabled, got %v", result)
	}
}

func TestGenerate_NoArtifacts(t *testing.T) {
	enabled := true
	cfg := &config.Config{Media: config.Media{Enabled: &enabled, MaxScreenshots: 3, MaxVideoMB: 8}}
	worktree := t.TempDir()
	result, err := Generate(cfg, worktree, "foo", filepath.Join(worktree, "media"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != nil {
		t.Fatalf("expected nil result when no test-results exist, got %v", result)
	}
}

func TestGenerate_ScreenshotsOnly(t *testing.T) {
	enabled := true
	cfg := &config.Config{Media: config.Media{Enabled: &enabled, MaxScreenshots: 2, MaxVideoMB: 8}}
	worktree := t.TempDir()

	testResults := filepath.Join(worktree, "packages", "web-app-foo", "test-results")
	for i, name := range []string{"test-a", "test-b", "test-c"} {
		sub := filepath.Join(testResults, name)
		if err := os.MkdirAll(sub, 0o755); err != nil {
			t.Fatal(err)
		}
		png := filepath.Join(sub, "test-finished-1.png")
		if err := os.WriteFile(png, []byte("png"), 0o644); err != nil {
			t.Fatal(err)
		}
		mt := time.Now().Add(time.Duration(i) * time.Minute)
		if err := os.Chtimes(png, mt, mt); err != nil {
			t.Fatal(err)
		}
	}

	outDir := filepath.Join(worktree, "media")
	result, err := Generate(cfg, worktree, "foo", outDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected a non-nil result")
	}
	if len(result.Screenshots) != 2 {
		t.Fatalf("expected 2 screenshots (MaxScreenshots), got %d: %v", len(result.Screenshots), result.Screenshots)
	}
	if result.VideoPath != "" {
		t.Fatalf("expected no video (no video.webm files present), got %q", result.VideoPath)
	}
	for _, s := range result.Screenshots {
		if _, err := os.Stat(s); err != nil {
			t.Errorf("expected screenshot file to exist: %v", err)
		}
	}
}
