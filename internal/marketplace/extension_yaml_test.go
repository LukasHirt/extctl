package marketplace

import (
	"testing"

	"github.com/LukasHirt/extctl/internal/config"
)

func TestHumanizeAppID(t *testing.T) {
	cases := map[string]string{
		"draw-io":        "Draw Io",
		"json-viewer":    "Json Viewer",
		"ai-doc-summary": "Ai Doc Summary",
		"unzip":          "Unzip",
	}
	for in, want := range cases {
		if got := humanizeAppID(in); got != want {
			t.Errorf("humanizeAppID(%q) = %q, want %q", in, got, want)
		}
	}
}

func testPublishConfig() *config.Config {
	return &config.Config{
		Publish: config.Publish{
			Authors: []config.PublishAuthor{{Name: "ownCloud GmbH", URL: "https://owncloud.com"}},
		},
	}
}

func TestBuildExtensionYAML_MissingLicenseErrors(t *testing.T) {
	cfg := testPublishConfig()
	pkg := PackageJSON{Description: "does things", License: ""}
	if _, err := BuildExtensionYAML(cfg, "some-ext", "0.1.0", pkg, []string{"extension"}, "", nil, nil, false); err == nil {
		t.Fatal("expected an error when package.json has no license field, got nil")
	}
}

// TestBuildExtensionYAML_Fields covers a FIRST-EVER submission (prev ==
// nil): name/subtitle fall through to the generated fallback, cover stays
// false since no cover bytes were fetched.
func TestBuildExtensionYAML_Fields(t *testing.T) {
	cfg := testPublishConfig()
	pkg := PackageJSON{Description: "View and edit draw.io diagram files.", License: "AGPL-3.0"}

	ext, err := BuildExtensionYAML(cfg, "draw-io", "0.2.0", pkg, []string{"editor", "viewer"}, "6.2.0", []string{"caption one"}, nil, false)
	if err != nil {
		t.Fatalf("BuildExtensionYAML: %v", err)
	}

	if ext.ID != "com.github.owncloud.web-extensions.draw-io" {
		t.Errorf("ID = %q", ext.ID)
	}
	if ext.Name != "Draw Io" {
		t.Errorf("Name = %q, want the humanized app-id fallback (no prior release to reuse a curated name from)", ext.Name)
	}
	if ext.Subtitle != pkg.Description {
		t.Errorf("Subtitle = %q, want package.json description verbatim (no prior release to reuse a curated subtitle from)", ext.Subtitle)
	}
	if ext.License != "AGPL-3.0" {
		t.Errorf("License = %q, want the package.json value verbatim", ext.License)
	}
	if ext.Version != "0.2.0" {
		t.Errorf("Version = %q", ext.Version)
	}
	if len(ext.Authors) != 1 || ext.Authors[0].Name != "ownCloud GmbH" {
		t.Errorf("Authors = %+v", ext.Authors)
	}
	if ext.MinOCIS != "6.2.0" {
		t.Errorf("MinOCIS = %q", ext.MinOCIS)
	}
	if len(ext.Resources) != 1 || ext.Resources[0].URL != "https://github.com/owncloud/web-extensions/tree/main/packages/web-app-draw-io" {
		t.Errorf("Resources = %+v", ext.Resources)
	}
	if len(ext.Tags) != 2 || ext.Tags[0] != "editor" || ext.Tags[1] != "viewer" {
		t.Errorf("Tags = %+v, want the tags passed in verbatim (tag resolution is ResolveTags's job, not BuildExtensionYAML's)", ext.Tags)
	}
	if len(ext.ScreenshotCaptions) != 1 || ext.ScreenshotCaptions[0] != "caption one" {
		t.Errorf("ScreenshotCaptions = %+v", ext.ScreenshotCaptions)
	}
	if ext.Cover {
		t.Error("Cover should be false when hasCoverBytes is false, regardless of anything else")
	}
}

// TestBuildExtensionYAML_ReusesPrevNameAndSubtitle reproduces the bug this
// fix addresses: BuildExtensionYAML used to always overwrite name/subtitle
// with its own generated guesses on every republish, clobbering whatever a
// human had already curated for this extension's earlier releases — the
// same pattern owncloud/marketplace#240 reported for minOCIS.
func TestBuildExtensionYAML_ReusesPrevNameAndSubtitle(t *testing.T) {
	cfg := testPublishConfig()
	pkg := PackageJSON{Description: "ownCloud web draw.io integration", License: "Apache-2.0"}
	prev := &ExtensionYAML{Name: "Draw.io", Subtitle: "View and edit draw.io diagram files (.drawio).", Cover: true}

	ext, err := BuildExtensionYAML(cfg, "draw-io", "0.3.0", pkg, nil, "", nil, prev, false)
	if err != nil {
		t.Fatalf("BuildExtensionYAML: %v", err)
	}
	if ext.Name != prev.Name {
		t.Errorf("Name = %q, want the curated prev.Name %q reused verbatim", ext.Name, prev.Name)
	}
	if ext.Subtitle != prev.Subtitle {
		t.Errorf("Subtitle = %q, want the curated prev.Subtitle %q reused verbatim", ext.Subtitle, prev.Subtitle)
	}
}

// TestBuildExtensionYAML_CoverNeverTrueWithoutBytes reproduces a bug this
// fix is careful NOT to introduce: cover must never be true unless the
// caller actually has real cover.png bytes to write alongside this
// extension.yaml, even when prev.Cover is true — otherwise a submission
// whose cover fetch failed would commit a dangling `cover: true` with no
// file behind it, reproducing owncloud/marketplace#238's class of bug for
// cover art specifically.
func TestBuildExtensionYAML_CoverNeverTrueWithoutBytes(t *testing.T) {
	cfg := testPublishConfig()
	pkg := PackageJSON{License: "Apache-2.0"}
	prev := &ExtensionYAML{Name: "X", Subtitle: "Y", Cover: true}

	ext, err := BuildExtensionYAML(cfg, "x", "0.2.0", pkg, nil, "", nil, prev, false)
	if err != nil {
		t.Fatalf("BuildExtensionYAML: %v", err)
	}
	if ext.Cover {
		t.Error("Cover = true, want false: hasCoverBytes was false, prev.Cover alone must not set it")
	}

	ext, err = BuildExtensionYAML(cfg, "x", "0.2.0", pkg, nil, "", nil, prev, true)
	if err != nil {
		t.Fatalf("BuildExtensionYAML: %v", err)
	}
	if !ext.Cover {
		t.Error("Cover = false, want true: hasCoverBytes was true")
	}
}

func TestBuildExtensionYAML_EmptyTagsPassThrough(t *testing.T) {
	// BuildExtensionYAML does not itself enforce a non-empty tags list —
	// ResolveTags may legitimately return none (no prior release, Claude
	// inference failed), and FormatPRBody is what flags that for a human
	// rather than this function inventing a placeholder tag.
	cfg := testPublishConfig()
	pkg := PackageJSON{License: "AGPL-3.0"}

	ext, err := BuildExtensionYAML(cfg, "some-ext", "0.1.0", pkg, nil, "", nil, nil, false)
	if err != nil {
		t.Fatalf("BuildExtensionYAML: %v", err)
	}
	if len(ext.Tags) != 0 {
		t.Errorf("Tags = %+v, want empty", ext.Tags)
	}
	if ext.MinOCIS != "" {
		t.Errorf("MinOCIS = %q, want empty", ext.MinOCIS)
	}
}
