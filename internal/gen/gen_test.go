package gen

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/LukasHirt/extctl/internal/claude"
	"github.com/LukasHirt/extctl/internal/config"
)

// rawCandidate builds a well-formed "## CANDIDATE" block for tests.
func rawCandidate(id, title, sketch string) string {
	return fmt.Sprintf(`## CANDIDATE
id: %s
title: %s
problem: |
  Some problem statement.
extension_point: app.files.sidebar
sketch: |
  %s
why_now: |
  Because reasons.
effort: S
evidence: |
  Found supporting evidence in the repo.
`, id, title, sketch)
}

// parseOne parses exactly one candidate block, failing the test otherwise.
func parseOne(t *testing.T, raw string) claude.ParsedCandidate {
	t.Helper()
	candidates, err := claude.ParseCandidates(raw)
	if err != nil {
		t.Fatalf("parse candidate: %v", err)
	}
	if len(candidates) != 1 {
		t.Fatalf("expected 1 candidate, got %d", len(candidates))
	}
	return candidates[0]
}

// testConfig returns a minimal config sufficient for reviewCandidates,
// reviseCandidate, and generateReplacements to run against temp files.
func testConfig(t *testing.T) *config.Config {
	t.Helper()
	dir := t.TempDir()

	genSpecs := filepath.Join(dir, "gen-specs.md")
	if err := os.WriteFile(genSpecs, []byte("Generate {{N}} candidates."), 0o644); err != nil {
		t.Fatalf("write gen-specs prompt: %v", err)
	}
	revise := filepath.Join(dir, "revise.md")
	if err := os.WriteFile(revise, []byte("Spec:\n{{CANDIDATE_SPEC}}\n\nFollow-up:\n{{USER_INSTRUCTION}}"), 0o644); err != nil {
		t.Fatalf("write revise prompt: %v", err)
	}

	return &config.Config{
		RunsDir:               t.TempDir(),
		IdeaPool:              filepath.Join(dir, "idea-pool-missing.yaml"), // intentionally absent
		FreshCandidatesPerDay: 3,
		Prompts: config.Prompts{
			GenSpecs: genSpecs,
			Revise:   revise,
		},
		TargetRepo: config.TargetRepo{Checkout: dir},
	}
}

// withFakeRunClaude swaps runClaude for the duration of the test.
func withFakeRunClaude(t *testing.T, fn func(claude.RunOptions) (*claude.Result, error)) {
	t.Helper()
	orig := runClaude
	runClaude = fn
	t.Cleanup(func() { runClaude = orig })
}

func TestReviewCandidates_Approve(t *testing.T) {
	cfg := testConfig(t)
	c := parseOne(t, rawCandidate("cand-a", "Title A", "Sketch A"))
	items := []reviewItem{{Candidate: c, SessionID: "sess-1"}}

	approved, rejected, err := reviewCandidates(items, cfg, "", cfg.RunsDir, "2026-07-13", strings.NewReader("a\n"))
	if err != nil {
		t.Fatalf("reviewCandidates: %v", err)
	}
	if len(rejected) != 0 {
		t.Fatalf("expected 0 rejected, got %d", len(rejected))
	}
	if len(approved) != 1 || approved[0].ID != "cand-a" {
		t.Fatalf("expected cand-a approved, got %+v", approved)
	}
}

func TestReviewCandidates_Discard(t *testing.T) {
	cfg := testConfig(t)
	c := parseOne(t, rawCandidate("cand-b", "Title B", "Sketch B"))
	items := []reviewItem{{Candidate: c, SessionID: "sess-1"}}

	approved, rejected, err := reviewCandidates(items, cfg, "", cfg.RunsDir, "2026-07-13", strings.NewReader("d\nnot a good fit\n"))
	if err != nil {
		t.Fatalf("reviewCandidates: %v", err)
	}
	if len(approved) != 0 {
		t.Fatalf("expected 0 approved, got %d", len(approved))
	}
	if len(rejected) != 1 || rejected[0].Candidate.ID != "cand-b" || rejected[0].Reason != "not a good fit" {
		t.Fatalf("unexpected rejected: %+v", rejected)
	}
}

func TestReviewCandidates_Show(t *testing.T) {
	cfg := testConfig(t)
	c := parseOne(t, rawCandidate("cand-c", "Title C", "Sketch C"))
	items := []reviewItem{{Candidate: c, SessionID: "sess-1"}}

	// "s" should just redisplay the spec and loop back to the menu.
	approved, _, err := reviewCandidates(items, cfg, "", cfg.RunsDir, "2026-07-13", strings.NewReader("s\na\n"))
	if err != nil {
		t.Fatalf("reviewCandidates: %v", err)
	}
	if len(approved) != 1 || approved[0].ID != "cand-c" {
		t.Fatalf("expected cand-c approved after show, got %+v", approved)
	}
}

func TestReviewCandidates_ReviseQuestion(t *testing.T) {
	cfg := testConfig(t)
	original := rawCandidate("cand-d", "Title D", "Sketch D")
	c := parseOne(t, original)
	items := []reviewItem{{Candidate: c, SessionID: "sess-1"}}

	var gotResume string
	withFakeRunClaude(t, func(opts claude.RunOptions) (*claude.Result, error) {
		gotResume = opts.Resume
		return &claude.Result{
			SessionID: "sess-2",
			NumTurns:  1,
			FullText:  "## RESPONSE\nThis extension point is the best fit because X.\n\n" + original,
		}, nil
	})

	approved, _, err := reviewCandidates(items, cfg, "", cfg.RunsDir, "2026-07-13",
		strings.NewReader("r\nWhy this extension point?\na\n"))
	if err != nil {
		t.Fatalf("reviewCandidates: %v", err)
	}
	if gotResume != "sess-1" {
		t.Fatalf("expected revise call to resume sess-1, got %q", gotResume)
	}
	if len(approved) != 1 || approved[0].ID != "cand-d" || approved[0].Title != "Title D" {
		t.Fatalf("expected unchanged cand-d approved, got %+v", approved)
	}
}

func TestReviewCandidates_ReviseChange(t *testing.T) {
	cfg := testConfig(t)
	original := rawCandidate("cand-e", "Title E", "Sketch E")
	revisedRaw := rawCandidate("cand-e", "Title E Revised", "Sketch E, shortened")
	c := parseOne(t, original)
	items := []reviewItem{{Candidate: c, SessionID: "sess-1"}}

	withFakeRunClaude(t, func(opts claude.RunOptions) (*claude.Result, error) {
		return &claude.Result{
			SessionID: "sess-2",
			NumTurns:  1,
			FullText:  "## RESPONSE\nShortened the sketch as requested.\n\n" + revisedRaw,
		}, nil
	})

	approved, _, err := reviewCandidates(items, cfg, "", cfg.RunsDir, "2026-07-13",
		strings.NewReader("r\nMake the sketch shorter\na\n"))
	if err != nil {
		t.Fatalf("reviewCandidates: %v", err)
	}
	if len(approved) != 1 || approved[0].Title != "Title E Revised" {
		t.Fatalf("expected revised title, got %+v", approved)
	}

	// review-<id>.md must reflect the revision on disk.
	specPath := filepath.Join(cfg.RunsDir, "2026-07-13", "review-cand-e.md")
	data, err := os.ReadFile(specPath)
	if err != nil {
		t.Fatalf("read review file: %v", err)
	}
	if !strings.Contains(string(data), "Title E Revised") {
		t.Fatalf("expected review file to contain revised title, got:\n%s", data)
	}
}

func TestReviewCandidates_ReviseIDMismatch(t *testing.T) {
	cfg := testConfig(t)
	original := rawCandidate("cand-f", "Title F", "Sketch F")
	wrongIDRaw := rawCandidate("cand-different", "Hijacked Title", "Hijacked sketch")
	c := parseOne(t, original)
	items := []reviewItem{{Candidate: c, SessionID: "sess-1"}}

	withFakeRunClaude(t, func(opts claude.RunOptions) (*claude.Result, error) {
		return &claude.Result{
			SessionID: "sess-2",
			NumTurns:  1,
			FullText:  "## RESPONSE\nOops.\n\n" + wrongIDRaw,
		}, nil
	})

	approved, _, err := reviewCandidates(items, cfg, "", cfg.RunsDir, "2026-07-13",
		strings.NewReader("r\nDo something\na\n"))
	if err != nil {
		t.Fatalf("reviewCandidates: %v", err)
	}
	if len(approved) != 1 || approved[0].ID != "cand-f" || approved[0].Title != "Title F" {
		t.Fatalf("expected original cand-f untouched after id-mismatch response, got %+v", approved)
	}
}

func TestReviewCandidates_ReviseClaudeError(t *testing.T) {
	cfg := testConfig(t)
	original := rawCandidate("cand-g", "Title G", "Sketch G")
	c := parseOne(t, original)
	items := []reviewItem{{Candidate: c, SessionID: "sess-1"}}

	withFakeRunClaude(t, func(opts claude.RunOptions) (*claude.Result, error) {
		return nil, fmt.Errorf("boom")
	})

	approved, _, err := reviewCandidates(items, cfg, "", cfg.RunsDir, "2026-07-13",
		strings.NewReader("r\nDo something\na\n"))
	if err != nil {
		t.Fatalf("reviewCandidates: %v", err)
	}
	if len(approved) != 1 || approved[0].ID != "cand-g" || approved[0].Title != "Title G" {
		t.Fatalf("expected original cand-g untouched after claude error, got %+v", approved)
	}
}

func TestGenerateReplacements_ReturnsSessionID(t *testing.T) {
	cfg := testConfig(t)
	opts := Options{Config: cfg}

	fakeText := rawCandidate("cand-h", "Title H", "Sketch H") +
		"\n" + rawCandidate("cand-i", "Title I", "Sketch I")

	withFakeRunClaude(t, func(opts claude.RunOptions) (*claude.Result, error) {
		return &claude.Result{
			SessionID: "batch-session",
			NumTurns:  2,
			FullText:  fakeText,
		}, nil
	})

	candidates, sessionID, err := generateReplacements(opts, 2, map[string]bool{}, nil, "2026-07-13", 1)
	if err != nil {
		t.Fatalf("generateReplacements: %v", err)
	}
	if sessionID != "batch-session" {
		t.Fatalf("expected batch-session, got %q", sessionID)
	}
	if len(candidates) != 2 || candidates[0].ID != "cand-h" || candidates[1].ID != "cand-i" {
		t.Fatalf("unexpected candidates: %+v", candidates)
	}

	items := toReviewItems(candidates, sessionID)
	if len(items) != 2 || items[0].SessionID != "batch-session" || items[1].SessionID != "batch-session" {
		t.Fatalf("expected both items to carry batch-session, got %+v", items)
	}
}
