package marketplace

import (
	"testing"

	"github.com/LukasHirt/extctl/internal/config"
)

func TestProjectForScreenshots(t *testing.T) {
	cfg := &config.Config{
		Publish: config.Publish{
			ScreenshotProject: "chrome",
			ScreenshotProjectOverrides: map[string]string{
				"cast": "firefox",
			},
		},
	}

	if got := ProjectForScreenshots(cfg, "photo-addon"); got != "chrome" {
		t.Errorf("ProjectForScreenshots(no override) = %q, want the org-wide default %q", got, "chrome")
	}
	if got := ProjectForScreenshots(cfg, "cast"); got != "firefox" {
		t.Errorf("ProjectForScreenshots(overridden) = %q, want the per-extension override %q", got, "firefox")
	}
}

func TestProjectForScreenshots_NilOverridesMap(t *testing.T) {
	cfg := &config.Config{Publish: config.Publish{ScreenshotProject: "chrome"}}
	if got := ProjectForScreenshots(cfg, "any-ext"); got != "chrome" {
		t.Errorf("ProjectForScreenshots with nil overrides map = %q, want %q", got, "chrome")
	}
}
