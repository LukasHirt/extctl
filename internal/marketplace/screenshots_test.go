package marketplace

import (
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"testing"
)

// writeTestPNG writes a visually varied width×height PNG (a diagonal
// gradient) — not a single flat color — so tests exercising
// validateScreenshot's size/dimension checks aren't accidentally tripped by
// isVisuallyDegenerate too. Use writeFlatPNG for tests that specifically
// want a degenerate (near-single-color) image.
func writeTestPNG(t *testing.T, path string, width, height int) {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			img.Set(x, y, color.RGBA{
				R: uint8((x * 255 / max(width, 1)) % 256),
				G: uint8((y * 255 / max(height, 1)) % 256),
				B: uint8(((x + y) * 255 / max(width+height, 1)) % 256),
				A: 255,
			})
		}
	}
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close() //nolint:errcheck
	if err := png.Encode(f, img); err != nil {
		t.Fatal(err)
	}
}

// writeFlatPNG writes a width×height PNG that's entirely a single flat
// color — simulating a blank/still-loading/unrendered capture (e.g. Leaflet
// map tiles that never finished painting).
func writeFlatPNG(t *testing.T, path string, width, height int) {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	flat := color.RGBA{R: 220, G: 220, B: 220, A: 255}
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			img.Set(x, y, flat)
		}
	}
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close() //nolint:errcheck
	if err := png.Encode(f, img); err != nil {
		t.Fatal(err)
	}
}

func TestValidateScreenshot_WithinLimits(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "shot.png")
	writeTestPNG(t, path, 100, 100)

	if reason := validateScreenshot(path); reason != "" {
		t.Errorf("expected a small valid PNG to pass, got reason: %s", reason)
	}
}

func TestValidateScreenshot_ExceedsMaxEdge(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "shot.png")
	writeTestPNG(t, path, maxScreenshotEdgePx+1, 10)

	if reason := validateScreenshot(path); reason == "" {
		t.Error("expected an oversized-edge image to be rejected")
	}
}

func TestValidateScreenshot_MissingFile(t *testing.T) {
	if reason := validateScreenshot(filepath.Join(t.TempDir(), "does-not-exist.png")); reason == "" {
		t.Error("expected a missing file to be rejected")
	}
}

// TestValidateScreenshot_FlatColorRejected covers the class of bug this
// session found twice in practice under different disguises (a 1x1 mocked
// photo stretched to fill the frame; Leaflet map tiles that hadn't finished
// painting when the screenshot was taken) — a test whose own assertions
// pass without ever confirming the screenshot shows real content.
func TestValidateScreenshot_FlatColorRejected(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "shot.png")
	writeFlatPNG(t, path, 200, 200)

	if reason := validateScreenshot(path); reason == "" {
		t.Error("expected a flat single-color image to be rejected as visually degenerate")
	}
}

func TestIsVisuallyDegenerate(t *testing.T) {
	dir := t.TempDir()

	flatPath := filepath.Join(dir, "flat.png")
	writeFlatPNG(t, flatPath, 200, 200)
	degenerate, err := isVisuallyDegenerate(flatPath)
	if err != nil {
		t.Fatalf("isVisuallyDegenerate(flat): %v", err)
	}
	if !degenerate {
		t.Error("expected a flat single-color image to be flagged degenerate")
	}

	variedPath := filepath.Join(dir, "varied.png")
	writeTestPNG(t, variedPath, 200, 200)
	degenerate, err = isVisuallyDegenerate(variedPath)
	if err != nil {
		t.Fatalf("isVisuallyDegenerate(varied): %v", err)
	}
	if degenerate {
		t.Error("expected a gradient image with real color variety to NOT be flagged degenerate")
	}
}
