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
	if _, err := BuildExtensionYAML(cfg, "some-ext", "0.1.0", pkg, []string{"extension"}, "", nil); err == nil {
		t.Fatal("expected an error when package.json has no license field, got nil")
	}
}

func TestBuildExtensionYAML_Fields(t *testing.T) {
	cfg := testPublishConfig()
	pkg := PackageJSON{Description: "View and edit draw.io diagram files.", License: "AGPL-3.0"}

	ext, err := BuildExtensionYAML(cfg, "draw-io", "0.2.0", pkg, []string{"editor", "viewer"}, "6.2.0", []string{"caption one"})
	if err != nil {
		t.Fatalf("BuildExtensionYAML: %v", err)
	}

	if ext.ID != "com.github.owncloud.web-extensions.draw-io" {
		t.Errorf("ID = %q", ext.ID)
	}
	if ext.Name != "Draw Io" {
		t.Errorf("Name = %q", ext.Name)
	}
	if ext.Subtitle != pkg.Description {
		t.Errorf("Subtitle = %q, want package.json description verbatim", ext.Subtitle)
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
		t.Error("Cover should default to false — no cover-image selection logic exists yet")
	}
}

func TestBuildExtensionYAML_EmptyTagsPassThrough(t *testing.T) {
	// BuildExtensionYAML does not itself enforce a non-empty tags list —
	// ResolveTags may legitimately return none (no prior release, Claude
	// inference failed), and FormatPRBody is what flags that for a human
	// rather than this function inventing a placeholder tag.
	cfg := testPublishConfig()
	pkg := PackageJSON{License: "AGPL-3.0"}

	ext, err := BuildExtensionYAML(cfg, "some-ext", "0.1.0", pkg, nil, "", nil)
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
