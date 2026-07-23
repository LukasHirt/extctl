package gate

import (
	"strings"
	"testing"
)

func TestAllScreenshots(t *testing.T) {
	shots, err := AllScreenshots("testdata/e2e-report-screenshots.json", "chrome")
	if err != nil {
		t.Fatalf("AllScreenshots: %v", err)
	}
	if len(shots) != 2 {
		t.Fatalf("expected 2 shots (one spec has no attachment), got %d: %+v", len(shots), shots)
	}

	if shots[0].Title != "shows the empty state before any files are uploaded" {
		t.Errorf("shots[0].Title = %q", shots[0].Title)
	}
	if shots[0].Path != "test-results/spec1-chrome/test-finished-1.png" {
		t.Errorf("expected preferred project 'chrome' screenshot, got %q", shots[0].Path)
	}

	if shots[1].Title != "only ran under firefox" {
		t.Errorf("shots[1].Title = %q", shots[1].Title)
	}
	if shots[1].Path != "test-results/spec3-firefox/test-finished-1.png" {
		t.Errorf("expected fallback to the only project present (firefox), got %q", shots[1].Path)
	}
}

func TestAllScreenshots_PreferredProjectAbsent(t *testing.T) {
	shots, err := AllScreenshots("testdata/e2e-report-screenshots.json", "webkit")
	if err != nil {
		t.Fatalf("AllScreenshots: %v", err)
	}
	if len(shots) != 2 {
		t.Fatalf("expected 2 shots, got %d", len(shots))
	}
	// webkit isn't present anywhere — falls back to the first project seen per spec.
	if shots[0].Path != "test-results/spec1-chrome/test-finished-1.png" {
		t.Errorf("expected fallback to first project (chrome), got %q", shots[0].Path)
	}
}

func TestAllScreenshots_MissingReport(t *testing.T) {
	if _, err := AllScreenshots("testdata/does-not-exist.json", "chrome"); err == nil {
		t.Error("expected an error for a missing report file")
	}
}

// TestAllScreenshots_RealMultiProjectReport reproduces the exact bug a real
// publish run hit: repeating captions. testdata/e2e-report-multiproject-real.json
// is a REAL Playwright 1.61.1 JSON report (fullyParallel:true, 3 projects,
// 2 specs), captured this session, that proves Playwright creates a
// SEPARATE spec node per project — not one spec node with a multi-entry
// Tests array, which is what this package originally (incorrectly)
// assumed. Screenshot attachments were synthetically added afterward (the
// verification run didn't have screenshot capture enabled) but the suite/
// spec/test nesting is exactly as Playwright produced it. Without deduping
// by title across the whole tree, AllScreenshots would return 6 entries
// (2 titles × 3 projects) instead of 2.
func TestAllScreenshots_RealMultiProjectReport(t *testing.T) {
	shots, err := AllScreenshots("testdata/e2e-report-multiproject-real.json", "chrome")
	if err != nil {
		t.Fatalf("AllScreenshots: %v", err)
	}
	if len(shots) != 2 {
		t.Fatalf("expected exactly 2 shots (one per unique spec title, deduped across 3 projects), got %d: %+v", len(shots), shots)
	}
	if shots[0].Title != "spec A does something" || shots[1].Title != "spec B does something else" {
		t.Errorf("titles = %+v, want the 2 unique titles in first-encountered order, no repeats", shots)
	}
	for _, s := range shots {
		if !strings.Contains(s.Path, "-chrome/") {
			t.Errorf("shot %q path = %q, want the preferred project (chrome)'s screenshot, not another project's", s.Title, s.Path)
		}
	}
}
