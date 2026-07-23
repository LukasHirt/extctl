package marketplace

import (
	"path/filepath"
	"testing"

	"github.com/LukasHirt/extctl/internal/config"
)

func TestParseTagLine(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"editor, viewer, diagram", []string{"editor", "viewer", "diagram"}},
		{"  Editor ,VIEWER  ", []string{"editor", "viewer"}},
		{"", nil},
		{"   \n  \n", nil},
		// Model added preamble despite instructions — take the last non-empty line.
		{"Sure, here are the tags:\neditor, viewer", []string{"editor", "viewer"}},
		{"editor,,viewer", []string{"editor", "viewer"}}, // empty segments dropped
	}
	for _, c := range cases {
		got := parseTagLine(c.in)
		if !stringSlicesEqual(got, c.want) {
			t.Errorf("parseTagLine(%q) = %+v, want %+v", c.in, got, c.want)
		}
	}
}

func stringSlicesEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestResolveTags_ReusesPreviousRelease(t *testing.T) {
	cfg := &config.Config{Prompts: config.Prompts{InferTags: filepath.Join(t.TempDir(), "does-not-exist.md")}}
	prev := &ExtensionYAML{Tags: []string{"editor", "viewer"}}

	tags, source := ResolveTags(cfg, "some-ext", prev, func(string, ...any) {})
	if source != TagSourcePreviousRelease {
		t.Errorf("source = %q, want %q", source, TagSourcePreviousRelease)
	}
	if !stringSlicesEqual(tags, prev.Tags) {
		t.Errorf("tags = %+v, want prev.Tags reused verbatim", tags)
	}
}

// TestResolveTags_NoPreviousReleaseNoClaudeYieldsNoTags is the behavior the
// user explicitly asked for: if there's no prior release AND Claude
// inference fails, ResolveTags must NOT invent a placeholder tag — it
// returns nothing, and it's FormatPRBody's job to flag that for a human.
func TestResolveTags_NoPreviousReleaseNoClaudeYieldsNoTags(t *testing.T) {
	cfg := &config.Config{Prompts: config.Prompts{InferTags: filepath.Join(t.TempDir(), "does-not-exist.md")}}

	tags, source := ResolveTags(cfg, "some-ext", nil, func(string, ...any) {})
	if source != TagSourceFallback {
		t.Errorf("source = %q, want %q", source, TagSourceFallback)
	}
	if len(tags) != 0 {
		t.Errorf("tags = %+v, want none — no placeholder tag should be invented", tags)
	}
}

func TestResolveTags_PreviousReleaseWithNoTagsFallsThrough(t *testing.T) {
	cfg := &config.Config{Prompts: config.Prompts{InferTags: filepath.Join(t.TempDir(), "does-not-exist.md")}}
	prev := &ExtensionYAML{Tags: nil} // a prior release existed but somehow had no tags

	tags, source := ResolveTags(cfg, "some-ext", prev, func(string, ...any) {})
	if source != TagSourceFallback {
		t.Errorf("source = %q, want %q (prev.Tags was empty, should fall through)", source, TagSourceFallback)
	}
	if len(tags) != 0 {
		t.Errorf("tags = %+v, want none", tags)
	}
}
