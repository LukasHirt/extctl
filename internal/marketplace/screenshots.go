package marketplace

import (
	"fmt"
	"image"
	_ "image/png" // screenshots are always PNG per scaffold/playwright.config.ts's screenshot:'on' setting
	"os"
	"path/filepath"

	"github.com/LukasHirt/extctl/internal/config"
	"github.com/LukasHirt/extctl/internal/gate"
)

// Image limits mirror owncloud/marketplace's own validation
// (tools/src/image-validate.ts, verified this session): max 4096px per
// edge, max 25,000,000 total pixels, max 5MB file size.
const (
	maxScreenshotEdgePx = 4096
	maxScreenshotPixels = 25_000_000
	maxScreenshotBytes  = 5 * 1024 * 1024
)

// CollectScreenshots reads the Playwright JSON report at jsonReportPath,
// caps the result at cfg.Publish.MaxScreenshots, copies each screenshot into
// destDir as zero-padded 01.png, 02.png, ... (marketplace requires
// deterministic sort order), and returns the paths plus each spec's title as
// the caption — verbatim, not paraphrased. Screenshots that fail
// marketplace's own image limits are dropped with a warning rather than
// failing the whole capture, mirroring internal/media's best-effort
// philosophy: screenshots/cover are optional fields in extension.yaml, so a
// dropped image should never block the submission.
//
// appID resolves the same project (ProjectForScreenshots) the capture
// attempt actually ran — since only that one project's tests exist in the
// report at all now, AllScreenshots's "preferred project" preference is
// really just confirming the report contains what was asked for, not
// choosing among several that ran.
func CollectScreenshots(cfg *config.Config, appID, jsonReportPath, destDir string) (paths []string, captions []string, warnings []string, err error) {
	// Cleared unconditionally, even if this run ends up with zero valid
	// shots — every call must reflect exactly what THIS run captured, never
	// leftovers from an earlier attempt (e.g. a retry that captures fewer
	// screenshots than last time, or none at all, must not leave a prior
	// run's images looking like part of the current one).
	if err := os.RemoveAll(destDir); err != nil {
		return nil, nil, nil, fmt.Errorf("clear stale screenshots in %s: %w", destDir, err)
	}

	shots, err := gate.AllScreenshots(jsonReportPath, ProjectForScreenshots(cfg, appID))
	if err != nil {
		return nil, nil, nil, err
	}
	if len(shots) == 0 {
		return nil, nil, nil, nil
	}

	max := cfg.Publish.MaxScreenshots
	if max > 0 && len(shots) > max {
		shots = shots[:max]
	}

	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return nil, nil, nil, fmt.Errorf("mkdir %s: %w", destDir, err)
	}

	n := 0
	for _, s := range shots {
		if w := validateScreenshot(s.Path); w != "" {
			warnings = append(warnings, fmt.Sprintf("%s: %s — dropped", s.Path, w))
			continue
		}
		n++
		dst := filepath.Join(destDir, fmt.Sprintf("%02d.png", n))
		if err := copyFile(s.Path, dst); err != nil {
			warnings = append(warnings, fmt.Sprintf("copy %s: %v", s.Path, err))
			n--
			continue
		}
		paths = append(paths, dst)
		captions = append(captions, s.Title)
	}
	return paths, captions, warnings, nil
}

// validateScreenshot returns a non-empty reason string if path violates
// marketplace's image limits, "" if the image is fine.
func validateScreenshot(path string) string {
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Sprintf("stat failed: %v", err)
	}
	if info.Size() > maxScreenshotBytes {
		return fmt.Sprintf("%.1fMB exceeds 5MB limit", float64(info.Size())/(1024*1024))
	}

	f, err := os.Open(path)
	if err != nil {
		return fmt.Sprintf("open failed: %v", err)
	}
	defer f.Close() //nolint:errcheck

	imgCfg, _, err := image.DecodeConfig(f)
	if err != nil {
		return fmt.Sprintf("decode failed: %v", err)
	}
	if imgCfg.Width > maxScreenshotEdgePx || imgCfg.Height > maxScreenshotEdgePx {
		return fmt.Sprintf("%dx%d exceeds %dpx max edge", imgCfg.Width, imgCfg.Height, maxScreenshotEdgePx)
	}
	if imgCfg.Width*imgCfg.Height > maxScreenshotPixels {
		return fmt.Sprintf("%dx%d exceeds %d max pixels", imgCfg.Width, imgCfg.Height, maxScreenshotPixels)
	}

	degenerate, err := isVisuallyDegenerate(path)
	if err != nil {
		return fmt.Sprintf("decode failed: %v", err)
	}
	if degenerate {
		return "image is almost entirely a single flat color — likely a blank/still-loading/unrendered state, not real content"
	}
	return ""
}

// minDistinctColorBuckets is isVisuallyDegenerate's threshold. Deliberately
// conservative (biased toward false negatives, not false positives): a
// wrongly-dropped real screenshot costs a whole extra fresh-spec attempt
// (~2-4+ min) for nothing, so this only needs to catch the extreme,
// unambiguous case — genuinely varied content (a map with terrain/roads, a
// photo, a document, a UI with more than one panel color) clears this by a
// wide margin even accounting for large legitimate uniform regions (a solid
// navy header, a white background).
const minDistinctColorBuckets = 6

// isVisuallyDegenerate reports whether path's image is so close to a single
// flat color that it's more likely a blank/still-loading/unrendered state —
// an unpainted map (real bug hit this session: Leaflet tiles not finished
// loading when the screenshot was taken, test passed anyway since its
// assertion only checked marker visibility), a broken image icon, an empty
// canvas — than real content.
//
// This generalizes a specific bug found earlier this session (mocked photos
// as a 1x1-pixel placeholder stretched to fill the frame, rendering as a
// flat block) into a class-level check: rather than hand-writing prompt
// guidance against every specific way a test can pass while its screenshot
// shows nothing real, this catches the whole class at the source,
// regardless of what future extension or async-content pattern causes it.
//
// Samples a coarse grid of pixels (every 12th in each dimension — cheap,
// and screenshot resolution makes finer sampling unnecessary), quantizes
// each to a 4-bit-per-channel color bucket (4096 possible buckets), and
// flags the image if fewer than minDistinctColorBuckets distinct buckets
// appear across the whole sample.
func isVisuallyDegenerate(path string) (bool, error) {
	f, err := os.Open(path)
	if err != nil {
		return false, err
	}
	defer f.Close() //nolint:errcheck

	img, _, err := image.Decode(f)
	if err != nil {
		return false, err
	}

	const gridStep = 12
	bounds := img.Bounds()
	buckets := make(map[[3]uint8]struct{})
	for y := bounds.Min.Y; y < bounds.Max.Y; y += gridStep {
		for x := bounds.Min.X; x < bounds.Max.X; x += gridStep {
			r, g, b, _ := img.At(x, y).RGBA()
			buckets[[3]uint8{uint8(r >> 12), uint8(g >> 12), uint8(b >> 12)}] = struct{}{}
			if len(buckets) >= minDistinctColorBuckets {
				return false, nil // early exit — already proven not degenerate
			}
		}
	}
	return len(buckets) < minDistinctColorBuckets, nil
}
