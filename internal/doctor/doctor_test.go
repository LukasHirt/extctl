package doctor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeConfig(t *testing.T, dir, content string) string {
	t.Helper()
	p := filepath.Join(dir, "extctl.yaml")
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func findingsIn(r *Report, sev Severity) []Finding {
	var out []Finding
	for _, f := range r.Findings {
		if f.Severity == sev {
			out = append(out, f)
		}
	}
	return out
}

func TestCollectYAMLKeys(t *testing.T) {
	schema := buildSchema()

	for _, want := range []string{
		"timezone", "jira.base_url", "jira.project", "prompts.gen_specs",
		"media.enabled", "target_repo.remote", "target_repo.checkout",
	} {
		if !schema[want] {
			t.Errorf("schema missing expected key %q", want)
		}
	}
	if schema["scaffold"] {
		t.Error("schema should not contain \"scaffold\" — it has no Config field")
	}
	if schema["scaffold.source"] {
		t.Error("schema should not contain \"scaffold.source\"")
	}
}

func TestFindUnknownKeys(t *testing.T) {
	schema := map[string]bool{
		"jira":              true,
		"jira.base_url":     true,
		"prompts":           true,
		"prompts.gen_specs": true,
	}

	tests := []struct {
		name string
		raw  map[string]any
		want []string
	}{
		{
			name: "all known",
			raw: map[string]any{
				"jira": map[string]any{"base_url": "x"},
			},
			want: nil,
		},
		{
			name: "unknown top-level block",
			raw: map[string]any{
				"scaffold": map[string]any{"source": "x", "exclude": []any{}},
			},
			want: []string{"scaffold"},
		},
		{
			name: "unknown nested key",
			raw: map[string]any{
				"jira": map[string]any{"base_url": "x", "typo_field": "y"},
			},
			want: []string{"jira.typo_field"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := findUnknownKeys(tt.raw, schema, "")
			if len(got) != len(tt.want) {
				t.Fatalf("findUnknownKeys() = %v, want %v", got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("findUnknownKeys()[%d] = %q, want %q", i, got[i], tt.want[i])
				}
			}
		})
	}
}

const minimalValidConfig = `
jira:
  base_url: https://example.atlassian.net
  project: TEST
target_repo:
  remote: owner/repo
`

func TestRun_UnknownScaffoldBlock(t *testing.T) {
	dir := t.TempDir()
	p := writeConfig(t, dir, minimalValidConfig+"\nscaffold:\n  source: 'https://example.com/skeleton'\n  exclude: []\n")

	r := Run(p)

	var found bool
	for _, f := range findingsIn(r, WARN) {
		if f.Section == SectionConfig && strings.Contains(f.Message, "scaffold") {
			found = true
		}
	}
	if !found {
		t.Error("expected a WARN finding mentioning \"scaffold\"")
	}
}

func TestRun_MissingRequiredJiraFields(t *testing.T) {
	dir := t.TempDir()
	p := writeConfig(t, dir, "jira:\n  project: ''\n")

	r := Run(p)

	if !r.HasErrors() {
		t.Fatal("expected HasErrors() to be true")
	}
	var foundProject bool
	for _, f := range findingsIn(r, ERROR) {
		if strings.Contains(f.Message, "jira.project") {
			foundProject = true
		}
	}
	if !foundProject {
		t.Error("expected an ERROR finding for empty jira.project")
	}
}

func TestRun_ConfigFileMissing(t *testing.T) {
	r := Run("/nonexistent/extctl.yaml")

	configErrors := 0
	for _, f := range findingsIn(r, ERROR) {
		if f.Section == SectionConfig {
			configErrors++
		}
	}
	if configErrors != 1 {
		t.Errorf("expected exactly 1 Config-section ERROR, got %d", configErrors)
	}
	if !r.HasErrors() {
		t.Error("expected HasErrors() to be true")
	}
}

func TestCheckTools_MediaDisabledSkipsFfmpeg(t *testing.T) {
	origLookPath := lookPath
	defer func() { lookPath = origLookPath }()

	var checkedFfmpeg bool
	lookPath = func(name string) error {
		if name == "ffmpeg" {
			checkedFfmpeg = true
		}
		return nil
	}

	dir := t.TempDir()
	p := writeConfig(t, dir, minimalValidConfig+"\nmedia:\n  enabled: false\n")
	r := &Report{}
	cfg := checkConfig(r, p)

	r2 := &Report{}
	checkTools(r2, cfg)

	if checkedFfmpeg {
		t.Error("ffmpeg should not be checked when media.enabled is false")
	}
	for _, f := range r2.Findings {
		if strings.Contains(f.Message, "media disabled") {
			return
		}
	}
	t.Error("expected an informational finding about media being disabled")
}

func TestCheckTools_MediaEnabledMissingFfmpegIsWarn(t *testing.T) {
	origLookPath := lookPath
	defer func() { lookPath = origLookPath }()

	lookPath = func(name string) error {
		if name == "ffmpeg" {
			return os.ErrNotExist
		}
		return nil
	}

	dir := t.TempDir()
	p := writeConfig(t, dir, minimalValidConfig)
	r := &Report{}
	cfg := checkConfig(r, p)

	r2 := &Report{}
	checkTools(r2, cfg)

	var gotWarn bool
	for _, f := range findingsIn(r2, WARN) {
		if strings.Contains(f.Message, "ffmpeg") {
			gotWarn = true
		}
	}
	if !gotWarn {
		t.Error("expected a WARN finding for missing ffmpeg when media.enabled is true")
	}
	if r2.HasErrors() {
		t.Error("missing ffmpeg with media enabled must not be an ERROR")
	}
}
