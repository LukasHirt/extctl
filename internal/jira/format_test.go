package jira_test

import (
	"strings"
	"testing"

	"github.com/LukasHirt/extctl/internal/claude"
	"github.com/LukasHirt/extctl/internal/jira"
)

func TestIssueSummary(t *testing.T) {
	tests := []struct {
		name  string
		title string
		want  string
	}{
		{"simple", "AI Document Summary", "[ext-candidate] AI Document Summary"},
		{"empty title", "", "[ext-candidate] "},
		{"brackets in title", "Foo [bar]", "[ext-candidate] Foo [bar]"},
		{"special chars", "Foo: Bar & Baz", "[ext-candidate] Foo: Bar & Baz"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := jira.IssueSummary(tt.title); got != tt.want {
				t.Errorf("IssueSummary(%q) = %q, want %q", tt.title, got, tt.want)
			}
		})
	}
}

func TestFormatIssueBody(t *testing.T) {
	base := claude.ParsedCandidate{
		ID:             "web-app-test",
		Title:          "Test Extension",
		Problem:        "users have a problem",
		ExtensionPoint: "foo.bar",
		Sketch:         "build a widget",
		WhyNow:         "market timing",
		Evidence:       "user surveys",
		Effort:         "M",
	}

	tests := []struct {
		name        string
		candidate   claude.ParsedCandidate
		appearances int
		origin      string
		wantContain []string
		wantAbsent  []string
	}{
		{
			name:        "all fields present",
			candidate:   base,
			appearances: 2,
			origin:      "generated",
			wantContain: []string{
				"h3. Problem",
				"users have a problem",
				"h3. Extension Point",
				"{{foo.bar}}",
				"h3. Sketch",
				"build a widget",
				"h3. Why Now",
				"market timing",
				"h3. Evidence",
				"user surveys",
				"h3. Metadata",
				"* *Effort:* M",
				"* *Appearances:* 2",
				"* *Origin:* generated",
				"* *ID:* {{web-app-test}}",
			},
		},
		{
			name: "empty problem omitted",
			candidate: claude.ParsedCandidate{
				ID: "x", Title: "T", ExtensionPoint: "ep", Sketch: "sk", Effort: "S",
			},
			appearances: 1,
			origin:      "generated",
			wantAbsent:  []string{"h3. Problem"},
			wantContain: []string{"h3. Extension Point", "h3. Metadata"},
		},
		{
			name: "empty why_now omitted",
			candidate: claude.ParsedCandidate{
				ID: "x", Title: "T", ExtensionPoint: "ep", Sketch: "sk", Effort: "L", Problem: "prob",
			},
			appearances: 1,
			origin:      "carryover",
			wantAbsent:  []string{"h3. Why Now"},
			wantContain: []string{"h3. Problem", "h3. Sketch"},
		},
		{
			name:        "effort S rendered",
			candidate:   func() claude.ParsedCandidate { c := base; c.Effort = "S"; return c }(),
			appearances: 1,
			origin:      "generated",
			wantContain: []string{"* *Effort:* S"},
		},
		{
			name:        "extension point wrapped in braces",
			candidate:   func() claude.ParsedCandidate { c := base; c.ExtensionPoint = "my.ext.point"; return c }(),
			appearances: 1,
			origin:      "generated",
			wantContain: []string{"{{my.ext.point}}"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := jira.FormatIssueBody(tt.candidate, tt.appearances, tt.origin)
			for _, want := range tt.wantContain {
				if !strings.Contains(got, want) {
					t.Errorf("body missing %q\nfull body:\n%s", want, got)
				}
			}
			for _, absent := range tt.wantAbsent {
				if strings.Contains(got, absent) {
					t.Errorf("body should not contain %q\nfull body:\n%s", absent, got)
				}
			}
		})
	}
}

func TestFormatComments(t *testing.T) {
	tests := []struct {
		name        string
		comments    []jira.Comment
		want        string
		wantContain []string
	}{
		{
			name:     "empty slice",
			comments: []jira.Comment{},
			want:     "(none)",
		},
		{
			name:     "nil slice",
			comments: nil,
			want:     "(none)",
		},
		{
			name: "single comment",
			comments: []jira.Comment{
				{Author: "Alice", Body: "hello", Created: "2026-01-01"},
			},
			wantContain: []string{
				"1. **Alice** (2026-01-01):",
				"   hello",
			},
		},
		{
			name: "multiline body indented",
			comments: []jira.Comment{
				{Author: "Bob", Body: "line1\nline2", Created: "2026-01-02"},
			},
			wantContain: []string{
				"   line1\n   line2",
			},
		},
		{
			name: "two comments separated by blank line",
			comments: []jira.Comment{
				{Author: "A", Body: "first", Created: "2026-01-01"},
				{Author: "B", Body: "second", Created: "2026-01-02"},
			},
			wantContain: []string{
				"1. **A**",
				"\n\n",
				"2. **B**",
			},
		},
		{
			name: "last comment no trailing blank line",
			comments: []jira.Comment{
				{Author: "A", Body: "first", Created: "2026-01-01"},
				{Author: "B", Body: "second", Created: "2026-01-02"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := jira.FormatComments(tt.comments)
			if tt.want != "" {
				if got != tt.want {
					t.Errorf("FormatComments = %q, want %q", got, tt.want)
				}
				return
			}
			for _, sub := range tt.wantContain {
				if !strings.Contains(got, sub) {
					t.Errorf("output missing %q\nfull output:\n%s", sub, got)
				}
			}
			if tt.name == "last comment no trailing blank line" {
				if strings.HasSuffix(got, "\n\n") {
					t.Errorf("output has trailing blank line: %q", got)
				}
			}
		})
	}
}
