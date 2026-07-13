package gate

import (
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"
)

// Digest returns a version of rawLog suitable for feeding into a repair
// prompt: stages that passed are collapsed to their one-line PASS marker,
// and — if the e2e stage failed and a Playwright JSON report is available
// at jsonReportPath — its console output is replaced with a digest that
// deduplicates the same failure across browser projects and embeds each
// distinct failure's error-context.md snapshot inline.
//
// Digest never errors. If the log doesn't match the expected [gate] marker
// format, or the JSON report is missing/unparseable, it degrades gracefully
// — worst case it returns rawLog unchanged.
func Digest(rawLog string, jsonReportPath string) string {
	lines := strings.Split(rawLog, "\n")
	spans := findStageSpans(lines)
	if len(spans) == 0 {
		return rawLog
	}

	var out []string
	prevEnd := -1
	for _, span := range spans {
		// Everything since the previous span's end, up to and including this
		// span's "--- Stage N: name ---" line, is preserved verbatim.
		out = append(out, lines[prevEnd+1:span.startLine+1]...)

		switch {
		case span.passed:
			out = append(out, fmt.Sprintf("[gate] PASS %s", span.name))
		case span.name == "e2e":
			out = append(out, digestE2E(lines[span.startLine+1:span.endLine], jsonReportPath)...)
			out = append(out, fmt.Sprintf("[gate] FAIL %s: %s", span.name, span.reason))
		default:
			// A failing non-e2e stage has no JSON report to dedupe against —
			// keep its body verbatim.
			out = append(out, lines[span.startLine+1:span.endLine+1]...)
		}
		prevEnd = span.endLine
	}
	out = append(out, lines[prevEnd+1:]...)
	return strings.Join(out, "\n")
}

// --- stage span detection -------------------------------------------------

type stageSpan struct {
	name      string
	startLine int // index of the "--- Stage N: name ---" line
	endLine   int // index of the PASS/FAIL line
	passed    bool
	reason    string
}

var (
	stageStartRE = regexp.MustCompile(`^\[gate\] --- Stage \d+: (\S+) ---$`)
	stagePassRE  = regexp.MustCompile(`^\[gate\] PASS (\S+)$`)
	stageFailRE  = regexp.MustCompile(`^\[gate\] FAIL (\S+): (.*)$`)
)

// findStageSpans locates every "[gate] --- Stage N: name ---" ... "[gate]
// PASS/FAIL name" block written by gate/run-gate.sh's log/stage_ok/stage_fail
// helpers. Text outside any span (e.g. the "=== Gate for X ===" header) is
// left untouched by the caller.
func findStageSpans(lines []string) []stageSpan {
	var spans []stageSpan
	var current *stageSpan
	for i, line := range lines {
		if m := stageStartRE.FindStringSubmatch(line); m != nil {
			current = &stageSpan{name: m[1], startLine: i}
			continue
		}
		if current == nil {
			continue
		}
		if m := stagePassRE.FindStringSubmatch(line); m != nil && m[1] == current.name {
			current.endLine = i
			current.passed = true
			spans = append(spans, *current)
			current = nil
		} else if m := stageFailRE.FindStringSubmatch(line); m != nil && m[1] == current.name {
			current.endLine = i
			current.reason = m[2]
			spans = append(spans, *current)
			current = nil
		}
	}
	return spans
}

// --- Playwright JSON reporter parsing --------------------------------------
//
// Mirrors the subset of https://playwright.dev/docs/test-reporters#json-reporter
// this package needs, verified against real output from Playwright 1.61.1
// rather than assumed from the docs alone.

type jsonReport struct {
	Suites []jsonSuite `json:"suites"`
}

type jsonSuite struct {
	Suites []jsonSuite `json:"suites"`
	Specs  []jsonSpec  `json:"specs"`
}

type jsonSpec struct {
	Title  string     `json:"title"`
	File   string     `json:"file"`
	Line   int        `json:"line"`
	Column int        `json:"column"`
	Tests  []jsonTest `json:"tests"`
}

// jsonTest is one (spec, project) pairing — Playwright's JSON reporter gives
// each project its own spec entry rather than nesting all projects under one.
type jsonTest struct {
	ProjectName string       `json:"projectName"`
	Results     []jsonResult `json:"results"`
}

type jsonResult struct {
	Status      string           `json:"status"`
	Error       *jsonResultError `json:"error"`
	Attachments []jsonAttachment `json:"attachments"`
}

type jsonResultError struct {
	Message string `json:"message"`
}

type jsonAttachment struct {
	Name string `json:"name"`
	Path string `json:"path"`
}

func isFailingStatus(status string) bool {
	switch status {
	case "failed", "timedOut", "interrupted":
		return true
	default:
		return false
	}
}

// failure is one flattened, non-passing (spec, project) outcome.
type failure struct {
	title        string
	file         string
	line         int
	column       int
	project      string
	errorMsg     string // ANSI-stripped
	snapshotPath string // path to error-context.md, if any
}

// flatten walks the suite tree and returns one failure per failing (spec,
// project) pair.
func flatten(suites []jsonSuite) []failure {
	var out []failure
	var walk func([]jsonSuite)
	walk = func(suites []jsonSuite) {
		for _, s := range suites {
			for _, spec := range s.Specs {
				for _, t := range spec.Tests {
					if len(t.Results) == 0 {
						continue
					}
					r := t.Results[len(t.Results)-1] // last attempt (retries=0 in gate, but be defensive)
					if !isFailingStatus(r.Status) {
						continue
					}
					f := failure{
						title:   spec.Title,
						file:    spec.File,
						line:    spec.Line,
						column:  spec.Column,
						project: t.ProjectName,
					}
					if r.Error != nil {
						f.errorMsg = renderANSI(r.Error.Message)
					}
					for _, a := range r.Attachments {
						if a.Name == "error-context" {
							f.snapshotPath = a.Path
							break
						}
					}
					out = append(out, f)
				}
			}
			walk(s.Suites)
		}
	}
	walk(suites)
	return out
}

// failureGroup is one distinct failure plus every project it occurred in.
type failureGroup struct {
	rep      failure
	projects []string
}

// groupFailures collapses failures that are "the same" across browser
// projects — same file, line, title, and first line of the error message —
// into a single group, in first-seen order.
func groupFailures(fails []failure) []failureGroup {
	var order []string
	groups := map[string]*failureGroup{}
	for _, f := range fails {
		k := groupKey(f)
		g, ok := groups[k]
		if !ok {
			g = &failureGroup{rep: f}
			groups[k] = g
			order = append(order, k)
		}
		g.projects = append(g.projects, f.project)
	}
	result := make([]failureGroup, 0, len(order))
	for _, k := range order {
		result = append(result, *groups[k])
	}
	return result
}

// groupKey identifies failures that are "the same" across browser projects.
// Playwright's assertion errors (e.g. "expect(locator).toBeVisible() failed")
// share an identical generic first line regardless of which locator or
// assertion actually failed — the distinguishing detail (locator, expected
// value) is on the lines that follow, up to the "Call log:" section, which
// is dropped since it can vary slightly between browsers even for the same
// underlying failure.
func groupKey(f failure) string {
	digest := f.errorMsg
	if i := strings.Index(digest, "\nCall log:"); i >= 0 {
		digest = digest[:i]
	}
	digest = strings.TrimSpace(digest)
	return fmt.Sprintf("%s:%d:%s:%s", f.file, f.line, f.title, digest)
}

func uniqueSorted(items []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, it := range items {
		if !seen[it] {
			seen[it] = true
			out = append(out, it)
		}
	}
	sort.Strings(out)
	return out
}

// digestE2E replaces raw Playwright console output with a grouped,
// deduplicated failure digest. On any problem loading or parsing the JSON
// report — missing file, malformed JSON, no failures found despite the
// stage having failed — it falls back to returning body unchanged.
func digestE2E(body []string, jsonReportPath string) []string {
	report, err := loadJSONReport(jsonReportPath)
	if err != nil {
		return body
	}
	fails := flatten(report.Suites)
	if len(fails) == 0 {
		return body
	}
	groups := groupFailures(fails)

	var out []string
	out = append(out, fmt.Sprintf("%d distinct e2e failure(s) across %d browser project(s):",
		len(groups), len(uniqueSorted(projectsOf(fails)))))

	for i, g := range groups {
		out = append(out, "")
		out = append(out, fmt.Sprintf("--- Failure %d of %d ---", i+1, len(groups)))
		out = append(out, fmt.Sprintf("Test: %s", g.rep.title))
		out = append(out, fmt.Sprintf("Location: %s:%d:%d", g.rep.file, g.rep.line, g.rep.column))
		out = append(out, fmt.Sprintf("Failed in: %s", strings.Join(uniqueSorted(g.projects), ", ")))
		out = append(out, "")
		out = append(out, "Error:")
		out = append(out, indent(g.rep.errorMsg)...)

		if g.rep.snapshotPath != "" {
			if content, err := os.ReadFile(g.rep.snapshotPath); err == nil {
				out = append(out, "")
				out = append(out, "Page state at failure (accessibility tree):")
				out = append(out, string(content))
			}
		}
	}
	return out
}

func projectsOf(fails []failure) []string {
	out := make([]string, len(fails))
	for i, f := range fails {
		out[i] = f.project
	}
	return out
}

func indent(s string) []string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	out := make([]string, len(lines))
	for i, l := range lines {
		out[i] = "  " + l
	}
	return out
}

func loadJSONReport(path string) (*jsonReport, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var r jsonReport
	if err := json.Unmarshal(data, &r); err != nil {
		return nil, err
	}
	return &r, nil
}
