package marketplace

import (
	"path/filepath"
	"testing"

	"github.com/LukasHirt/extctl/internal/config"
)

func TestGenerateScreenshotSpec_MissingPromptErrors(t *testing.T) {
	cfg := &config.Config{
		TargetRepo: config.TargetRepo{Checkout: t.TempDir()},
		Prompts:    config.Prompts{MarketplaceScreenshots: filepath.Join(t.TempDir(), "does-not-exist.md")},
	}
	if _, err := GenerateScreenshotSpec(cfg, "some-ext", filepath.Join(t.TempDir(), "e2e-report.json"), "chrome"); err == nil {
		t.Fatal("expected an error when the marketplace-screenshots prompt file is missing")
	}
}
