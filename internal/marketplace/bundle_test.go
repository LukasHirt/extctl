package marketplace

import (
	"archive/zip"
	"os"
	"path/filepath"
	"testing"
)

func writeZip(t *testing.T, path string, files map[string]string) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	w := zip.NewWriter(f)
	for name, content := range files {
		fw, err := w.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := fw.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestZipRootIsAppID(t *testing.T) {
	dir := t.TempDir()

	clean := filepath.Join(dir, "clean.zip")
	writeZip(t, clean, map[string]string{
		"draw-io/manifest.json": "{}",
		"draw-io/draw-io.js":    "console.log(1)",
	})
	ok, err := zipRootIsAppID(clean, "draw-io")
	if err != nil {
		t.Fatalf("zipRootIsAppID: %v", err)
	}
	if !ok {
		t.Error("expected a single top-level <appID>/ folder to be recognized as clean")
	}

	messy := filepath.Join(dir, "messy.zip")
	writeZip(t, messy, map[string]string{
		"draw-io/manifest.json": "{}",
		"README.md":             "unexpected top-level file",
	})
	ok, err = zipRootIsAppID(messy, "draw-io")
	if err != nil {
		t.Fatalf("zipRootIsAppID: %v", err)
	}
	if ok {
		t.Error("expected an extra top-level entry to be rejected")
	}
}

func TestExtractAndRezipAppSubtree(t *testing.T) {
	dir := t.TempDir()

	src := filepath.Join(dir, "messy.zip")
	writeZip(t, src, map[string]string{
		"draw-io/manifest.json": `{"name":"draw-io"}`,
		"draw-io/draw-io.js":    "console.log(1)",
		"README.md":             "unexpected top-level file",
	})

	dst := filepath.Join(dir, "bundle.zip")
	if err := extractAndRezipAppSubtree(src, "draw-io", dst); err != nil {
		t.Fatalf("extractAndRezipAppSubtree: %v", err)
	}

	r, err := zip.OpenReader(dst)
	if err != nil {
		t.Fatalf("open re-zipped bundle: %v", err)
	}
	defer r.Close() //nolint:errcheck

	names := map[string]bool{}
	for _, f := range r.File {
		names[f.Name] = true
	}
	if !names["draw-io/manifest.json"] || !names["draw-io/draw-io.js"] {
		t.Errorf("expected draw-io/* entries preserved, got %v", names)
	}
	if names["README.md"] {
		t.Error("expected the unrelated top-level file to be dropped")
	}
}

func TestExtractAndRezipAppSubtree_NothingUnderPrefix(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "wrong.zip")
	writeZip(t, src, map[string]string{"other-app/manifest.json": "{}"})

	if err := extractAndRezipAppSubtree(src, "draw-io", filepath.Join(dir, "bundle.zip")); err == nil {
		t.Error("expected an error when no entries match the app-id prefix")
	}
}
