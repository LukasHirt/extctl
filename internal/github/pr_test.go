package github_test

import (
	"strings"
	"testing"

	"github.com/LukasHirt/extctl/internal/github"
)

func TestStageIcon(t *testing.T) {
	// stageIcon is unexported; test via FormatBody output which calls it.
	// We verify the icons appear in gate table rows.
	tests := []struct {
		name    string
		verdict string
		want    string
	}{
		{"ok", "ok", "✅ ok"},
		{"fail", "fail", "❌ fail"},
		{"skip", "skip", "⏭️ skip"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opts := github.BodyOptions{
				SpecMD:      validSpec(),
				GateScore:   1.0,
				GateHygiene: tt.verdict,
			}
			got := github.FormatBody(opts)
			if !strings.Contains(got, tt.want) {
				t.Errorf("FormatBody with GateHygiene=%q: body does not contain %q\nbody:\n%s",
					tt.verdict, tt.want, got)
			}
		})
	}

	t.Run("unknown passthrough", func(t *testing.T) {
		opts := github.BodyOptions{
			SpecMD:      validSpec(),
			GateScore:   1.0,
			GateHygiene: "warning",
		}
		got := github.FormatBody(opts)
		if !strings.Contains(got, "warning") {
			t.Errorf("unknown verdict 'warning' not passed through in body:\n%s", got)
		}
	})
}

func TestFormatBody(t *testing.T) {
	tests := []struct {
		name        string
		opts        github.BodyOptions
		wantContain []string
		wantAbsent  []string
	}{
		{
			name: "gate passed score 1.0",
			opts: github.BodyOptions{SpecMD: validSpec(), GateScore: 1.0},
			wantContain: []string{"✅"},
		},
		{
			name: "gate warning score < 1.0",
			opts: github.BodyOptions{SpecMD: validSpec(), GateScore: 0.8},
			wantContain: []string{"⚠️"},
		},
		{
			name: "jira link with key and url",
			opts: github.BodyOptions{
				SpecMD:    validSpec(),
				GateScore: 1.0,
				JiraKey:   "OSPO-46",
				JiraURL:   "https://jira.example.com/browse/OSPO-46",
			},
			wantContain: []string{"[OSPO-46](https://jira.example.com/browse/OSPO-46)"},
		},
		{
			name: "jira key without url",
			opts: github.BodyOptions{
				SpecMD:    validSpec(),
				GateScore: 1.0,
				JiraKey:   "OSPO-46",
				JiraURL:   "",
			},
			wantContain: []string{"OSPO-46"},
			wantAbsent:  []string{"[OSPO-46]("},
		},
		{
			name: "spec parsed shows problem and sketch",
			opts: github.BodyOptions{SpecMD: validSpec(), GateScore: 1.0},
			wantContain: []string{
				"## Problem",
				"the problem",
				"## Solution",
				"the sketch",
			},
		},
		{
			name: "malformed spec fallback",
			opts: github.BodyOptions{SpecMD: "not a valid spec", GateScore: 1.0},
			wantContain: []string{"## Spec"},
		},
		{
			name: "empty WhatWasBuilt",
			opts: github.BodyOptions{SpecMD: validSpec(), GateScore: 1.0, WhatWasBuilt: ""},
			wantContain: []string{"_(build summary unavailable)_"},
		},
		{
			name: "WhatWasBuilt present",
			opts: github.BodyOptions{SpecMD: validSpec(), GateScore: 1.0, WhatWasBuilt: "built 5 files"},
			wantContain: []string{"built 5 files"},
			wantAbsent:  []string{"_(build summary unavailable)_"},
		},
		{
			name: "all gate stages present",
			opts: github.BodyOptions{
				SpecMD:      validSpec(),
				GateScore:   0.9,
				GateHygiene: "ok",
				GateBuild:   "ok",
				GateLint:    "ok",
				GateUnit:    "ok",
				GateE2E:     "skip",
			},
			wantContain: []string{
				"| Hygiene |",
				"| Build |",
				"| Lint |",
				"| Unit tests |",
				"| E2E tests |",
				"| **Score** |",
			},
		},
		{
			name: "empty gate fields omitted from table",
			opts: github.BodyOptions{
				SpecMD:      validSpec(),
				GateScore:   1.0,
				GateHygiene: "ok",
			},
			wantContain: []string{"| Hygiene |"},
			wantAbsent:  []string{"| Build |", "| Lint |", "| Unit tests |"},
		},
		{
			name: "extension point in body",
			opts: github.BodyOptions{SpecMD: validSpec(), GateScore: 1.0},
			wantContain: []string{"`foo.bar`"},
		},
		{
			name: "multiple extension points",
			opts: github.BodyOptions{
				SpecMD:    specWithExtensionPoints("foo.bar,baz.qux"),
				GateScore: 1.0,
			},
			wantContain: []string{"`foo.bar`", "`baz.qux`", "·"},
		},
		{
			name: "effort in footer from spec",
			opts: github.BodyOptions{SpecMD: validSpec(), GateScore: 1.0},
			wantContain: []string{"Effort: M"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := github.FormatBody(tt.opts)
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

func validSpec() string {
	return `## CANDIDATE
id: web-app-test
title: Test Extension
extension_point: foo.bar
sketch: the sketch
effort: M
problem: the problem
why_now: the why
`
}

func specWithExtensionPoints(eps string) string {
	return `## CANDIDATE
id: web-app-test
title: Test Extension
extension_point: ` + eps + `
sketch: the sketch
effort: L
problem: the problem
`
}
