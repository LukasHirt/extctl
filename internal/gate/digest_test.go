package gate

import (
	"strconv"
	"strings"
	"testing"
)

// buildLog assembles a synthetic gate.log matching the real [gate] marker
// format written by gate/run-gate.sh's log/stage_ok/stage_fail helpers.
// stages maps stage name -> (body, passed). Stage order is fixed to match
// the real gate: hygiene, build, lint, unit, e2e — a stage is omitted (and
// everything after it) once a failing stage is reached, mirroring
// run-gate.sh's early-exit behavior.
func buildLog(order []string, failStage string, failBody string, failReason string) string {
	var b strings.Builder
	b.WriteString("[gate] === Gate for some-ext ===\n[gate] Worktree: /tmp/worktree\n[gate] Output:   /tmp/output\n")
	for i, name := range order {
		b.WriteString("[gate] \n")
		b.WriteString("[gate] --- Stage " + strconv.Itoa(i+1) + ": " + name + " ---\n")
		if name == failStage {
			b.WriteString(failBody)
			b.WriteString("[gate] FAIL " + name + ": " + failReason + "\n")
			return b.String()
		}
		b.WriteString("some pnpm/eslint/vitest output for " + name + "\nmore lines\n")
		b.WriteString("[gate] PASS " + name + "\n")
	}
	return b.String()
}

var stageOrder = []string{"hygiene", "build", "lint", "unit", "e2e"}

func TestDigest_AllStagesPass(t *testing.T) {
	log := buildLog(stageOrder, "", "", "")
	digest := Digest(log, "testdata/does-not-exist.json")

	for _, name := range stageOrder {
		if !strings.Contains(digest, "[gate] PASS "+name) {
			t.Errorf("expected PASS marker for %s in digest, got:\n%s", name, digest)
		}
	}
	if strings.Contains(digest, "some pnpm/eslint/vitest output") {
		t.Error("expected passing-stage bodies to be collapsed, but raw body survived")
	}
}

func TestDigest_NonE2EStageFails(t *testing.T) {
	log := buildLog(stageOrder, "build", "vite build failed: syntax error\n", "pnpm build failed")
	digest := Digest(log, "testdata/does-not-exist.json")

	if !strings.Contains(digest, "[gate] PASS hygiene") {
		t.Error("expected the earlier passing stage to still collapse")
	}
	if !strings.Contains(digest, "vite build failed: syntax error") {
		t.Error("expected the failing non-e2e stage's body to be preserved verbatim")
	}
	if !strings.Contains(digest, "[gate] FAIL build: pnpm build failed") {
		t.Error("expected the FAIL marker to be preserved")
	}
	if strings.Contains(digest, "Stage 5: e2e") {
		t.Error("e2e stage never ran and should not appear in the digest")
	}
}

func TestDigest_UnrecognizedLogFormat(t *testing.T) {
	raw := "nothing here matches the [gate] marker format at all\njust plain text\n"
	digest := Digest(raw, "testdata/does-not-exist.json")
	if digest != raw {
		t.Errorf("expected unrecognized log to pass through unchanged, got:\n%s", digest)
	}
}

func TestDigest_E2E_SingleFailure(t *testing.T) {
	log := buildLog(stageOrder, "e2e", "Running 4 tests using 1 worker\n<raw playwright console output>\n", "playwright tests failed")
	digest := Digest(log, "testdata/e2e-report-single.json")

	if !strings.Contains(digest, "1 distinct e2e failure(s) across 1 browser project(s)") {
		t.Errorf("expected single-failure summary line, got:\n%s", digest)
	}
	if !strings.Contains(digest, "Test: fails looking for a missing element") {
		t.Error("expected test title in digest")
	}
	if !strings.Contains(digest, "Failed in: chrome") {
		t.Error("expected project name in digest")
	}
	if !strings.Contains(digest, "getByTestId('nonexistent')") {
		t.Error("expected the error message to be embedded")
	}
	if !strings.Contains(digest, "Page state at failure") {
		t.Error("expected the error-context.md snapshot to be embedded")
	}
	if strings.Contains(digest, "<raw playwright console output>") {
		t.Error("expected the raw console body to be replaced by the digest, not kept alongside it")
	}
}

func TestDigest_E2E_DuplicateAcrossBrowsers(t *testing.T) {
	log := buildLog(stageOrder, "e2e", "raw console\n", "playwright tests failed")
	digest := Digest(log, "testdata/e2e-report-duplicate.json")

	if !strings.Contains(digest, "1 distinct e2e failure(s) across 2 browser project(s)") {
		t.Errorf("expected the identical failure to collapse into one group, got:\n%s", digest)
	}
	if !strings.Contains(digest, "Failed in: chrome, chrome2") {
		t.Error("expected both projects listed on the single group")
	}
	if n := strings.Count(digest, "--- Failure"); n != 1 {
		t.Errorf("expected exactly one failure block, found %d", n)
	}
	// Only one error-context.md should be embedded, not one per browser —
	// both fixture files share this exact heading text, so count occurrences.
	if n := strings.Count(digest, "Page state at failure"); n != 1 {
		t.Errorf("expected exactly one embedded snapshot, found %d", n)
	}
}

func TestDigest_E2E_TwoDistinctFailures(t *testing.T) {
	log := buildLog(stageOrder, "e2e", "raw console\n", "playwright tests failed")
	digest := Digest(log, "testdata/e2e-report-two-distinct.json")

	if !strings.Contains(digest, "2 distinct e2e failure(s) across 2 browser project(s)") {
		t.Errorf("expected two distinct groups, got:\n%s", digest)
	}
	if n := strings.Count(digest, "--- Failure"); n != 2 {
		t.Errorf("expected exactly two failure blocks, found %d", n)
	}
	if !strings.Contains(digest, "Failed in: chrome\n") && !strings.Contains(digest, "Failed in: chrome\r\n") {
		t.Error("expected the first failure to be scoped to chrome only")
	}
	if !strings.Contains(digest, "Failed in: chrome2") {
		t.Error("expected the second failure to be scoped to chrome2 only")
	}
	if !strings.Contains(digest, "a-totally-different-locator") {
		t.Error("expected the second, distinct error message to appear")
	}
}

func TestDigest_MissingJSONReport_FallsBack(t *testing.T) {
	rawBody := "Running 4 tests using 1 worker\n<raw playwright console output that should survive>\n"
	log := buildLog(stageOrder, "e2e", rawBody, "playwright tests failed")
	digest := Digest(log, "testdata/does-not-exist.json")

	if !strings.Contains(digest, "<raw playwright console output that should survive>") {
		t.Errorf("expected fallback to raw e2e body when the JSON report is missing, got:\n%s", digest)
	}
}

func TestDigest_MalformedJSONReport_FallsBack(t *testing.T) {
	rawBody := "Running 4 tests using 1 worker\n<raw playwright console output that should survive>\n"
	log := buildLog(stageOrder, "e2e", rawBody, "playwright tests failed")
	digest := Digest(log, "testdata/malformed.json")

	if !strings.Contains(digest, "<raw playwright console output that should survive>") {
		t.Errorf("expected fallback to raw e2e body when the JSON report is malformed, got:\n%s", digest)
	}
}
