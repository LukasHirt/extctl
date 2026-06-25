package github

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"strconv"
	"strings"

	"github.com/LukasHirt/extctl/internal/claude"
)

// PR represents a GitHub pull request.
type PR struct {
	Number int
	URL    string
}

// PROptions configures a pull request to create.
type PROptions struct {
	RepoSlug string   // e.g. "owncloud/web-extensions"
	Branch   string   // source branch name
	Title    string   // PR title
	Body     string   // PR body (markdown)
	Labels   []string // label names
	Draft    bool     // open as a draft PR
}

// execCommand is the exec.Command function used to invoke the gh CLI.
// Replaced in tests to avoid calling the real binary.
var execCommand = exec.Command

// Create opens a pull request via the gh CLI.
// Requires GH_TOKEN or GITHUB_TOKEN in the environment (or gh auth login).
// The `gh` CLI must be installed and authenticated.
func Create(opts PROptions) (*PR, error) {
	args := []string{
		"pr", "create",
		"--repo", opts.RepoSlug,
		"--head", opts.Branch,
		"--title", opts.Title,
		"--body", opts.Body,
	}
	for _, label := range opts.Labels {
		args = append(args, "--label", label)
	}
	if opts.Draft {
		args = append(args, "--draft")
	}

	cmd := execCommand("gh", args...)
	out, err := cmd.Output()
	if err != nil {
		stderr := ""
		if ee, ok := err.(*exec.ExitError); ok {
			stderr = string(ee.Stderr)
		}
		return nil, fmt.Errorf("gh pr create: %w\nstderr: %s", err, strings.TrimSpace(stderr))
	}

	// gh pr create outputs the PR URL as the last line of stdout
	prURL := strings.TrimSpace(string(out))
	if lines := strings.Split(prURL, "\n"); len(lines) > 0 {
		prURL = strings.TrimSpace(lines[len(lines)-1])
	}

	// extract PR number from URL (e.g. https://github.com/org/repo/pull/42)
	parts := strings.Split(prURL, "/")
	if len(parts) == 0 {
		return nil, fmt.Errorf("parse gh pr create output: unexpected URL %q", prURL)
	}
	prNumber, err := strconv.Atoi(parts[len(parts)-1])
	if err != nil {
		return nil, fmt.Errorf("parse gh pr create output: non-numeric PR number in URL %q", prURL)
	}
	return &PR{Number: prNumber, URL: prURL}, nil
}

// IsMerged reports whether the given PR number has been merged.
func IsMerged(repoSlug string, number int) (bool, error) {
	cmd := execCommand("gh", "pr", "view",
		fmt.Sprintf("%d", number),
		"--repo", repoSlug,
		"--json", "merged",
	)
	out, err := cmd.Output()
	if err != nil {
		stderr := ""
		if ee, ok := err.(*exec.ExitError); ok {
			stderr = string(ee.Stderr)
		}
		return false, fmt.Errorf("gh pr view: %w\nstderr: %s", err, strings.TrimSpace(stderr))
	}
	var result struct {
		Merged bool `json:"merged"`
	}
	if err := json.Unmarshal(out, &result); err != nil {
		return false, fmt.Errorf("parse gh pr view output: %w", err)
	}
	return result.Merged, nil
}

// AddComment posts a comment on a pull request.
func AddComment(repoSlug string, number int, body string) error {
	cmd := execCommand("gh", "pr", "comment",
		fmt.Sprintf("%d", number),
		"--repo", repoSlug,
		"--body", body,
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("gh pr comment: %w\n%s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// SetReady marks a draft PR as ready for review.
func SetReady(repoSlug string, number int) error {
	cmd := execCommand("gh", "pr", "ready",
		fmt.Sprintf("%d", number),
		"--repo", repoSlug,
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("gh pr ready: %w\n%s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// BodyOptions configures the rendered PR body.
type BodyOptions struct {
	SpecMD       string // full ## CANDIDATE block
	WhatWasBuilt string // Claude's build summary from the last stage JSONL
	JiraKey      string // e.g. "OSPO-46"
	JiraURL      string // full Jira issue URL for linking
	GateScore    float64
	GateHygiene  string // "ok" | "fail" | ""
	GateBuild    string
	GateLint     string
	GateUnit     string
	GateE2E      string // "ok" | "fail" | "skip" | ""
}

// FormatBody renders the PR body for an AI-generated extension.
// Parses the ## CANDIDATE block into structured sections when possible;
// falls back to rendering the raw block if parsing fails.
func FormatBody(opts BodyOptions) string {
	var spec *claude.ParsedCandidate
	if candidates, err := claude.ParseCandidates(opts.SpecMD); err == nil && len(candidates) > 0 {
		c := candidates[0]
		spec = &c
	}

	var b strings.Builder

	// Compact metadata header — one line, stays out of the way.
	jiraRef := opts.JiraKey
	if opts.JiraURL != "" && opts.JiraKey != "" {
		jiraRef = fmt.Sprintf("[%s](%s)", opts.JiraKey, opts.JiraURL)
	}
	gateIndicator := "✅"
	if opts.GateScore < 1.0 {
		gateIndicator = "⚠️"
	}
	fmt.Fprintf(&b, "> **AI-generated** · %s · Gate: %s %.2f\n\n",
		jiraRef, gateIndicator, opts.GateScore)

	if spec != nil {
		writeSection(&b, "Problem", spec.Problem)
		writeSection(&b, "Solution", spec.Sketch)

		if spec.ExtensionPoint != "" {
			b.WriteString("## Extension points\n\n")
			points := strings.Split(spec.ExtensionPoint, ",")
			rendered := make([]string, 0, len(points))
			for _, p := range points {
				if t := strings.TrimSpace(p); t != "" {
					rendered = append(rendered, "`"+t+"`")
				}
			}
			b.WriteString(strings.Join(rendered, " · "))
			b.WriteString("\n\n")
		}

		writeSection(&b, "Why ship this now", spec.WhyNow)
	} else {
		// Fallback: render the raw spec block if parsing failed.
		b.WriteString("## Spec\n\n")
		b.WriteString(opts.SpecMD)
		b.WriteString("\n\n")
	}

	b.WriteString("## What was built\n\n")
	if opts.WhatWasBuilt != "" {
		b.WriteString(opts.WhatWasBuilt)
	} else {
		b.WriteString("_(build summary unavailable)_")
	}
	b.WriteString("\n\n")

	b.WriteString("## Gate\n\n")
	b.WriteString("| Check | Result |\n|---|---|\n")
	if opts.GateHygiene != "" {
		fmt.Fprintf(&b, "| Hygiene | %s |\n", stageIcon(opts.GateHygiene))
	}
	if opts.GateBuild != "" {
		fmt.Fprintf(&b, "| Build | %s |\n", stageIcon(opts.GateBuild))
	}
	if opts.GateLint != "" {
		fmt.Fprintf(&b, "| Lint | %s |\n", stageIcon(opts.GateLint))
	}
	if opts.GateUnit != "" {
		fmt.Fprintf(&b, "| Unit tests | %s |\n", stageIcon(opts.GateUnit))
	}
	if opts.GateE2E != "" {
		fmt.Fprintf(&b, "| E2E tests | %s |\n", stageIcon(opts.GateE2E))
	}
	fmt.Fprintf(&b, "| **Score** | **%.2f** |\n\n", opts.GateScore)

	effort := ""
	if spec != nil && spec.Effort != "" {
		effort = "Effort: " + spec.Effort + " · "
	}
	fmt.Fprintf(&b, "---\n\n<sub>%s🤖 Generated by [extctl](https://github.com/LukasHirt/extctl)</sub>\n", effort)

	return b.String()
}

func writeSection(b *strings.Builder, heading, content string) {
	if content == "" {
		return
	}
	fmt.Fprintf(b, "## %s\n\n%s\n\n", heading, content)
}

func stageIcon(verdict string) string {
	switch verdict {
	case "ok":
		return "✅ ok"
	case "fail":
		return "❌ fail"
	case "skip":
		return "⏭️ skip"
	default:
		return verdict
	}
}
