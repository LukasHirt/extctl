package marketplace

import (
	"archive/zip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// DownloadBundle downloads the release zip asset for tag from remote via
// `gh release download`, verifies its internal layout already matches what
// marketplace's bundle.zip needs (a single top-level "<appID>/" folder —
// verified this session against a real web-extensions release asset,
// group-management-0.1.0.zip, which unzips to exactly
// group-management/manifest.json + group-management/*.js), and writes the
// result to destDir/bundle.zip.
func DownloadBundle(remote, tag, appID, assetName, destDir string) (string, error) {
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return "", fmt.Errorf("mkdir %s: %w", destDir, err)
	}

	cmd := execCommand("gh", "release", "download", tag,
		"-R", remote,
		"-p", assetName,
		"-D", destDir,
		"--clobber",
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("gh release download %s: %w\n%s", tag, err, strings.TrimSpace(string(out)))
	}

	downloaded := filepath.Join(destDir, assetName)
	bundlePath := filepath.Join(destDir, "bundle.zip")

	ok, err := zipRootIsAppID(downloaded, appID)
	if err != nil {
		return "", fmt.Errorf("inspect %s: %w", downloaded, err)
	}
	if ok {
		if err := copyFile(downloaded, bundlePath); err != nil {
			return "", fmt.Errorf("copy %s to bundle.zip: %w", downloaded, err)
		}
		return bundlePath, nil
	}

	// Defensive fallback: the asset has extra top-level entries beyond
	// <appID>/ — extract just that subtree and re-zip it.
	if err := extractAndRezipAppSubtree(downloaded, appID, bundlePath); err != nil {
		return "", fmt.Errorf("re-zip %s subtree from %s: %w", appID, downloaded, err)
	}
	return bundlePath, nil
}

// zipRootIsAppID reports whether every entry in the zip at path sits under a
// single top-level "<appID>/" directory.
func zipRootIsAppID(path, appID string) (bool, error) {
	r, err := zip.OpenReader(path)
	if err != nil {
		return false, err
	}
	defer r.Close() //nolint:errcheck

	prefix := appID + "/"
	for _, f := range r.File {
		if f.Name != appID && !strings.HasPrefix(f.Name, prefix) {
			return false, nil
		}
	}
	return true, nil
}

func extractAndRezipAppSubtree(srcZip, appID, dstZip string) error {
	r, err := zip.OpenReader(srcZip)
	if err != nil {
		return err
	}
	defer r.Close() //nolint:errcheck

	out, err := os.Create(dstZip)
	if err != nil {
		return err
	}
	w := zip.NewWriter(out)

	prefix := appID + "/"
	wrote := false
	for _, f := range r.File {
		if f.Name != prefix && !strings.HasPrefix(f.Name, prefix) {
			continue
		}
		if err := copyZipEntry(w, f); err != nil {
			_ = w.Close()
			_ = out.Close()
			return err
		}
		wrote = true
	}
	if err := w.Close(); err != nil {
		_ = out.Close()
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	if !wrote {
		return fmt.Errorf("no entries under %s in %s", prefix, srcZip)
	}
	return nil
}

func copyZipEntry(w *zip.Writer, f *zip.File) error {
	dst, err := w.CreateHeader(&f.FileHeader)
	if err != nil {
		return err
	}
	if f.FileInfo().IsDir() {
		return nil
	}
	src, err := f.Open()
	if err != nil {
		return err
	}
	defer src.Close() //nolint:errcheck
	_, err = io.Copy(dst, src)
	return err
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
