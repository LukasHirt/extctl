// Package media generates demo screenshots and a demo video from the
// artifacts Playwright leaves behind after the gate's e2e stage. It is
// best-effort: every failure is soft — callers should log the returned
// error, if any, and never let it block a build or publish.
package media

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/LukasHirt/extctl/internal/config"
)

// Result holds the local paths of generated demo media. Fields are empty
// when that kind of media couldn't be produced (e.g. no ffmpeg for video).
type Result struct {
	VideoPath   string
	Screenshots []string
}

type artifact struct {
	path    string
	modTime time.Time
}

// Generate walks packages/web-app-<extID>/test-results/ under worktreePath
// for Playwright's recorded video.webm and screenshot PNGs (produced when
// scaffold/playwright.config.ts enables full capture under CI=true), then
// writes up to cfg.Media.MaxScreenshots screenshots and one concatenated
// demo.mp4 into outDir.
//
// Returns (nil, nil) when media generation is disabled or nothing was
// recorded (e2e stage skipped, no test-results). A non-nil error alongside
// a non-nil Result means partial success — treat it as a warning to log,
// not a reason to fail the build.
func Generate(cfg *config.Config, worktreePath, extID, outDir string) (*Result, error) {
	if cfg.Media.Enabled == nil || !*cfg.Media.Enabled {
		return nil, nil
	}

	testResultsDir := filepath.Join(worktreePath, "packages", "web-app-"+extID, "test-results")
	videos, screenshots, err := collectArtifacts(testResultsDir)
	if err != nil {
		return nil, fmt.Errorf("collect test-results artifacts: %w", err)
	}
	if len(videos) == 0 && len(screenshots) == 0 {
		return nil, nil
	}

	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return nil, fmt.Errorf("mkdir media output dir: %w", err)
	}

	result := &Result{}
	var warnings []string

	for i, src := range pickScreenshots(screenshots, cfg.Media.MaxScreenshots) {
		dst := filepath.Join(outDir, fmt.Sprintf("screenshot-%d.png", i+1))
		if err := copyFile(src, dst); err != nil {
			warnings = append(warnings, fmt.Sprintf("copy screenshot %s: %v", src, err))
			continue
		}
		result.Screenshots = append(result.Screenshots, dst)
	}

	if len(videos) > 0 {
		videoPath, vErr := buildVideo(videos, outDir, cfg.Media.MaxVideoMB)
		if videoPath != "" {
			result.VideoPath = videoPath
		}
		if vErr != nil {
			warnings = append(warnings, vErr.Error())
		}
	}

	if len(warnings) > 0 {
		return result, fmt.Errorf("media: %s", strings.Join(warnings, "; "))
	}
	return result, nil
}

// collectArtifacts finds every video.webm and *.png under dir, ordered by
// modification time (reflects test execution order regardless of
// Playwright's directory-naming scheme). Returns (nil, nil, nil) if dir
// doesn't exist.
func collectArtifacts(dir string) (videos, screenshots []string, err error) {
	if _, statErr := os.Stat(dir); statErr != nil {
		if os.IsNotExist(statErr) {
			return nil, nil, nil
		}
		return nil, nil, statErr
	}

	var videoArtifacts, screenshotArtifacts []artifact
	walkErr := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		switch {
		case strings.EqualFold(filepath.Ext(path), ".png"):
			screenshotArtifacts = append(screenshotArtifacts, artifact{path, info.ModTime()})
		case filepath.Base(path) == "video.webm":
			videoArtifacts = append(videoArtifacts, artifact{path, info.ModTime()})
		}
		return nil
	})
	if walkErr != nil {
		return nil, nil, walkErr
	}

	sort.Slice(videoArtifacts, func(i, j int) bool { return videoArtifacts[i].modTime.Before(videoArtifacts[j].modTime) })
	sort.Slice(screenshotArtifacts, func(i, j int) bool { return screenshotArtifacts[i].modTime.Before(screenshotArtifacts[j].modTime) })

	for _, a := range videoArtifacts {
		videos = append(videos, a.path)
	}
	for _, a := range screenshotArtifacts {
		screenshots = append(screenshots, a.path)
	}
	return videos, screenshots, nil
}

// pickScreenshots selects up to max paths, evenly spread across the
// available set so the result represents distinct steps rather than a
// cluster from one part of the run.
func pickScreenshots(paths []string, max int) []string {
	if max <= 0 || len(paths) == 0 {
		return nil
	}
	if len(paths) <= max {
		return paths
	}
	if max == 1 {
		return []string{paths[len(paths)/2]}
	}
	picked := make([]string, 0, max)
	for i := range max {
		idx := i * (len(paths) - 1) / (max - 1)
		picked = append(picked, paths[idx])
	}
	return picked
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close() //nolint:errcheck
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}

// buildVideo concatenates all recorded test videos (ffmpeg concat demuxer,
// stream copy — all inputs are the same vp8/webm codec) and transcodes the
// result to H.264 mp4, retrying once at harsher settings if it's still over
// maxMB. Requires ffmpeg on PATH.
func buildVideo(videos []string, outDir string, maxMB int) (string, error) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		return "", fmt.Errorf("build demo video: ffmpeg not found on PATH: %w", err)
	}
	if maxMB <= 0 {
		maxMB = 8
	}

	concatInput := videos[0]
	if len(videos) > 1 {
		listPath := filepath.Join(outDir, "concat-list.txt")
		var sb strings.Builder
		for _, v := range videos {
			abs, err := filepath.Abs(v)
			if err != nil {
				return "", fmt.Errorf("build demo video: resolve path %s: %w", v, err)
			}
			fmt.Fprintf(&sb, "file '%s'\n", strings.ReplaceAll(abs, "'", `'\''`))
		}
		if err := os.WriteFile(listPath, []byte(sb.String()), 0o644); err != nil {
			return "", fmt.Errorf("build demo video: write concat list: %w", err)
		}
		defer os.Remove(listPath) //nolint:errcheck

		concatOut := filepath.Join(outDir, "concat.webm")
		if out, err := exec.Command("ffmpeg", "-y", "-f", "concat", "-safe", "0",
			"-i", listPath, "-c", "copy", concatOut).CombinedOutput(); err != nil {
			return "", fmt.Errorf("build demo video: ffmpeg concat: %w\n%s", err, string(out))
		}
		defer os.Remove(concatOut) //nolint:errcheck
		concatInput = concatOut
	}

	outPath := filepath.Join(outDir, "demo.mp4")
	if err := transcode(concatInput, outPath, 720, 28); err != nil {
		return "", fmt.Errorf("build demo video: %w", err)
	}

	if info, err := os.Stat(outPath); err == nil && info.Size() > int64(maxMB)*1024*1024 {
		if err := transcode(concatInput, outPath, 480, 32); err != nil {
			return "", fmt.Errorf("build demo video: retry transcode: %w", err)
		}
		if info, err := os.Stat(outPath); err == nil && info.Size() > int64(maxMB)*1024*1024 {
			return outPath, fmt.Errorf("demo video is %dMB, still exceeds max_video_mb=%d after compression retry",
				info.Size()/(1024*1024), maxMB)
		}
	}

	return outPath, nil
}

func transcode(inPath, outPath string, height, crf int) error {
	out, err := exec.Command("ffmpeg", "-y", "-i", inPath,
		"-vf", fmt.Sprintf("scale=-2:%d", height),
		"-c:v", "libx264", "-crf", strconv.Itoa(crf), "-preset", "veryfast",
		"-an", outPath).CombinedOutput()
	if err != nil {
		return fmt.Errorf("ffmpeg transcode: %w\n%s", err, string(out))
	}
	return nil
}
