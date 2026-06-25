package claude

import (
	"strings"
	"testing"
)

func TestParseCandidates(t *testing.T) {
	minimalBlock := func(id, title, ep, sketch, effort string) string {
		return "## CANDIDATE\nid: " + id + "\ntitle: " + title +
			"\nextension_point: " + ep + "\nsketch: " + sketch +
			"\neffort: " + effort + "\n"
	}

	tests := []struct {
		name      string
		input     string
		wantCount int
		wantIDs   []string
		wantErr   bool
		errSubstr string
	}{
		{
			name:      "empty input",
			input:     "",
			wantCount: 0,
		},
		{
			name:      "preamble before first marker",
			input:     "some preamble\n" + minimalBlock("a", "A", "ep", "sk", "M"),
			wantCount: 1,
			wantIDs:   []string{"a"},
		},
		{
			name: "single complete block",
			input: `## CANDIDATE
id: my-ext
title: My Extension
extension_point: foo.bar
sketch: does something
effort: M
`,
			wantCount: 1,
			wantIDs:   []string{"my-ext"},
		},
		{
			name:      "multiple blocks",
			input:     minimalBlock("a", "A", "ep1", "sk1", "S") + minimalBlock("b", "B", "ep2", "sk2", "L"),
			wantCount: 2,
			wantIDs:   []string{"a", "b"},
		},
		{
			name:      "missing id",
			input:     "## CANDIDATE\ntitle: T\nextension_point: ep\nsketch: sk\neffort: M\n",
			wantErr:   true,
			errSubstr: `"id"`,
		},
		{
			name:      "missing title",
			input:     "## CANDIDATE\nid: x\nextension_point: ep\nsketch: sk\neffort: M\n",
			wantErr:   true,
			errSubstr: `"title"`,
		},
		{
			name:      "missing extension_point",
			input:     "## CANDIDATE\nid: x\ntitle: T\nsketch: sk\neffort: M\n",
			wantErr:   true,
			errSubstr: `"extension_point"`,
		},
		{
			name:      "missing sketch",
			input:     "## CANDIDATE\nid: x\ntitle: T\nextension_point: ep\neffort: M\n",
			wantErr:   true,
			errSubstr: `"sketch"`,
		},
		{
			name:      "missing effort",
			input:     "## CANDIDATE\nid: x\ntitle: T\nextension_point: ep\nsketch: sk\n",
			wantErr:   true,
			errSubstr: `"effort"`,
		},
		{
			name:      "invalid effort XL",
			input:     "## CANDIDATE\nid: x\ntitle: T\nextension_point: ep\nsketch: sk\neffort: XL\n",
			wantErr:   true,
			errSubstr: "effort must be S, M, or L",
		},
		{
			name: "effort lowercase s normalized",
			input: `## CANDIDATE
id: a
title: A
extension_point: ep
sketch: sk
effort: s
`,
			wantCount: 1,
		},
		{
			name: "effort lowercase m normalized",
			input: `## CANDIDATE
id: a
title: A
extension_point: ep
sketch: sk
effort: m
`,
			wantCount: 1,
		},
		{
			name: "effort lowercase l normalized",
			input: `## CANDIDATE
id: a
title: A
extension_point: ep
sketch: sk
effort: l
`,
			wantCount: 1,
		},
		{
			name: "Raw field preserved",
			input: `## CANDIDATE
id: raw-test
title: Raw Test
extension_point: ep
sketch: sk
effort: M
`,
			wantCount: 1,
		},
		{
			name: "extension_points alias",
			input: `## CANDIDATE
id: a
title: A
extension_points: foo.bar
sketch: sk
effort: M
`,
			wantCount: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseCandidates(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil")
				}
				if tt.errSubstr != "" && !strings.Contains(err.Error(), tt.errSubstr) {
					t.Errorf("error %q does not contain %q", err.Error(), tt.errSubstr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(got) != tt.wantCount {
				t.Errorf("got %d candidates, want %d", len(got), tt.wantCount)
			}
			for i, wantID := range tt.wantIDs {
				if i >= len(got) {
					t.Errorf("missing candidate at index %d (want id=%q)", i, wantID)
					continue
				}
				if got[i].ID != wantID {
					t.Errorf("candidate[%d].ID = %q, want %q", i, got[i].ID, wantID)
				}
			}
		})
	}

	t.Run("effort normalized to uppercase", func(t *testing.T) {
		for _, effort := range []string{"s", "m", "l"} {
			block := "## CANDIDATE\nid: x\ntitle: T\nextension_point: ep\nsketch: sk\neffort: " + effort + "\n"
			got, err := ParseCandidates(block)
			if err != nil {
				t.Fatalf("effort=%q unexpected error: %v", effort, err)
			}
			want := strings.ToUpper(effort)
			if got[0].Effort != want {
				t.Errorf("effort=%q: got %q, want %q", effort, got[0].Effort, want)
			}
		}
	})

	t.Run("Raw starts with ## CANDIDATE", func(t *testing.T) {
		block := "## CANDIDATE\nid: x\ntitle: T\nextension_point: ep\nsketch: sk\neffort: M\n"
		got, err := ParseCandidates(block)
		if err != nil || len(got) == 0 {
			t.Fatal("unexpected error or empty result")
		}
		if !strings.HasPrefix(got[0].Raw, "## CANDIDATE\n") {
			t.Errorf("Raw = %q, want prefix '## CANDIDATE\\n'", got[0].Raw)
		}
	})

	t.Run("extension_points alias sets ExtensionPoint", func(t *testing.T) {
		block := "## CANDIDATE\nid: x\ntitle: T\nextension_points: foo.bar\nsketch: sk\neffort: M\n"
		got, err := ParseCandidates(block)
		if err != nil || len(got) == 0 {
			t.Fatal("unexpected error or empty result")
		}
		if got[0].ExtensionPoint != "foo.bar" {
			t.Errorf("ExtensionPoint = %q, want %q", got[0].ExtensionPoint, "foo.bar")
		}
	})
}

func TestParseBlock(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		want      ParsedCandidate
		wantErr   bool
		errSubstr string
	}{
		{
			name: "all fields inline",
			input: `id: my-ext
title: My Extension
extension_point: foo.bar
sketch: does something
effort: M
problem: the problem
why_now: now
evidence: some evidence
`,
			want: ParsedCandidate{
				ID:             "my-ext",
				Title:          "My Extension",
				ExtensionPoint: "foo.bar",
				Sketch:         "does something",
				Effort:         "M",
				Problem:        "the problem",
				WhyNow:         "now",
				Evidence:       "some evidence",
			},
		},
		{
			name: "block scalar problem",
			input: `id: x
title: T
extension_point: ep
sketch: sk
effort: S
problem: |
  line one
  line two
`,
			want: ParsedCandidate{
				ID:             "x",
				Title:          "T",
				ExtensionPoint: "ep",
				Sketch:         "sk",
				Effort:         "S",
				Problem:        "line one\nline two",
			},
		},
		{
			name: "block scalar trailing blank lines trimmed",
			input: `id: x
title: T
extension_point: ep
sketch: sk
effort: S
problem: |
  content

`,
			want: ParsedCandidate{
				ID:             "x",
				Title:          "T",
				ExtensionPoint: "ep",
				Sketch:         "sk",
				Effort:         "S",
				Problem:        "content",
			},
		},
		{
			name: "unknown keys ignored",
			input: `id: x
title: T
extension_point: ep
sketch: sk
effort: M
foo: bar
baz: qux
`,
			want: ParsedCandidate{
				ID:             "x",
				Title:          "T",
				ExtensionPoint: "ep",
				Sketch:         "sk",
				Effort:         "M",
			},
		},
		{
			name: "colon in value",
			input: `id: x
title: Foo: Bar: Baz
extension_point: ep
sketch: sk
effort: M
`,
			want: ParsedCandidate{
				ID:             "x",
				Title:          "Foo: Bar: Baz",
				ExtensionPoint: "ep",
				Sketch:         "sk",
				Effort:         "M",
			},
		},
		{
			name:      "missing required field id",
			input:     "title: T\nextension_point: ep\nsketch: sk\neffort: M\n",
			wantErr:   true,
			errSubstr: `"id"`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseBlock(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil")
				}
				if tt.errSubstr != "" && !strings.Contains(err.Error(), tt.errSubstr) {
					t.Errorf("error %q does not contain %q", err.Error(), tt.errSubstr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got.ID != tt.want.ID {
				t.Errorf("ID = %q, want %q", got.ID, tt.want.ID)
			}
			if got.Title != tt.want.Title {
				t.Errorf("Title = %q, want %q", got.Title, tt.want.Title)
			}
			if got.ExtensionPoint != tt.want.ExtensionPoint {
				t.Errorf("ExtensionPoint = %q, want %q", got.ExtensionPoint, tt.want.ExtensionPoint)
			}
			if got.Sketch != tt.want.Sketch {
				t.Errorf("Sketch = %q, want %q", got.Sketch, tt.want.Sketch)
			}
			if got.Effort != tt.want.Effort {
				t.Errorf("Effort = %q, want %q", got.Effort, tt.want.Effort)
			}
			if got.Problem != tt.want.Problem {
				t.Errorf("Problem = %q, want %q", got.Problem, tt.want.Problem)
			}
			if got.WhyNow != tt.want.WhyNow {
				t.Errorf("WhyNow = %q, want %q", got.WhyNow, tt.want.WhyNow)
			}
			if got.Evidence != tt.want.Evidence {
				t.Errorf("Evidence = %q, want %q", got.Evidence, tt.want.Evidence)
			}
		})
	}
}
