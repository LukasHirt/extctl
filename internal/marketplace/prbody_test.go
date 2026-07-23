package marketplace

import (
	"strings"
	"testing"
)

func TestFormatPRBody(t *testing.T) {
	r := Result{AppID: "draw-io", Version: "0.2.0", Tag: "draw-io-v0.2.0"}
	ext := ExtensionYAML{
		ID:                 "com.github.owncloud.web-extensions.draw-io",
		Name:               "Draw Io",
		Subtitle:           "View and edit draw.io diagram files.",
		License:            "AGPL-3.0",
		Version:            "0.2.0",
		Tags:               []string{"editor", "viewer"},
		MinOCIS:            "6.2.0",
		ScreenshotCaptions: []string{"one", "two"},
	}

	body := FormatPRBody(r, ext)

	for _, want := range []string{
		"- [x] An **oCIS web extension**",
		"com.github.owncloud.web-extensions.draw-io",
		"`draw-io`",
		"`0.2.0`",
		"extensions/draw-io/releases/0.2.0/",
		"draw-io-v0.2.0",
		// Every checklist item from the real template must be checked —
		// extctl guarantees each one by construction (tags is non-empty here).
		"- [x] `bundle.zip` and `extension.yaml` are committed",
		"- [x] The `version` in `extension.yaml` matches",
		"- [x] The reverse-DNS `id` is the same across every release",
		"- [x] This is a **new** release",
		"- [x] `extension.yaml` has at least one `authors` entry",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("PR body missing %q\nbody:\n%s", want, body)
		}
	}
}

func TestFormatPRBody_NoTagsAtAll(t *testing.T) {
	body := FormatPRBody(Result{AppID: "x", Version: "1.0.0", Tag: "x-v1.0.0"}, ExtensionYAML{Name: "X", Tags: nil})
	if !strings.Contains(body, "- [ ] `extension.yaml` has at least one `authors` entry and at least one `tags` entry — **missing tags") {
		t.Errorf("expected the template checklist item to be left UNCHECKED when tags is empty, body:\n%s", body)
	}
}

// TestFormatPRBody_NoReviewNotesSection confirms the old "extctl notes —
// please double-check" section is really gone: that context now goes to the
// terminal at staging time (printReviewNotes), not into the PR, since a
// human already reviewed before `extctl publish approve` ever runs.
func TestFormatPRBody_NoReviewNotesSection(t *testing.T) {
	body := FormatPRBody(Result{AppID: "x", Version: "1.0.0", Tag: "x-v1.0.0"}, ExtensionYAML{Name: "X", Tags: []string{"a"}})
	for _, unwanted := range []string{
		"extctl notes",
		"please double-check",
		"was humanized from the app id",
		"sanity-check",
	} {
		if strings.Contains(body, unwanted) {
			t.Errorf("PR body should no longer contain %q, body:\n%s", unwanted, body)
		}
	}
}
