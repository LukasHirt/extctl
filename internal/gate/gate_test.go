package gate

import (
	"net/http"
	"net/http/httptest" //nolint:all
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestHelperProcess is the subprocess helper for Tier 4 tests.
func TestHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_HELPER_PROCESS") != "1" {
		return
	}
	stdout := os.Getenv("GO_HELPER_STDOUT")
	if stdout != "" {
		_, _ = os.Stdout.WriteString(stdout)
	}
	exitCode := 0
	if os.Getenv("GO_HELPER_EXIT") == "1" {
		exitCode = 1
	}
	os.Exit(exitCode)
}

// TestMain installs a panicking default for execCommand.
func TestMain(m *testing.M) {
	if os.Getenv("GO_WANT_HELPER_PROCESS") == "1" {
		os.Exit(m.Run())
	}
	execCommand = func(name string, args ...string) *exec.Cmd {
		panic("execCommand called without test override")
	}
	os.Exit(m.Run())
}

// noopExec returns an exec replacement whose subprocess does nothing (exits 0).
func noopExec(t *testing.T) func(string, ...string) *exec.Cmd {
	t.Helper()
	original := execCommand
	t.Cleanup(func() { execCommand = original })
	return func(name string, args ...string) *exec.Cmd {
		cmd := exec.Command(os.Args[0], "-test.run=TestHelperProcess", "--")
		cmd.Env = append(os.Environ(), "GO_WANT_HELPER_PROCESS=1")
		return cmd
	}
}

// outputExec returns an exec replacement whose subprocess prints stdout.
func outputExec(t *testing.T, stdout string) func(string, ...string) *exec.Cmd {
	t.Helper()
	original := execCommand
	t.Cleanup(func() { execCommand = original })
	return func(name string, args ...string) *exec.Cmd {
		cmd := exec.Command(os.Args[0], "-test.run=TestHelperProcess", "--")
		cmd.Env = append(os.Environ(),
			"GO_WANT_HELPER_PROCESS=1",
			"GO_HELPER_STDOUT="+stdout,
		)
		return cmd
	}
}

func TestLogPrefix(t *testing.T) {
	tests := []struct {
		name  string
		logID string
		want  string
	}{
		{"empty", "", ""},
		{"non-empty", "my-ext", "[my-ext] "},
		{"with-hyphens", "web-app-foo", "[web-app-foo] "},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := logPrefix(tt.logID); got != tt.want {
				t.Errorf("logPrefix(%q) = %q, want %q", tt.logID, got, tt.want)
			}
		})
	}
}

func TestRenderANSI(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "plain text",
			input: "hello\nworld",
			want:  "hello\nworld",
		},
		{
			name:  "carriage return overwrites",
			input: "abc\rXY",
			want:  "XYc",
		},
		{
			name:  "carriage return full overwrite",
			input: "abc\rXYZ",
			want:  "XYZ",
		},
		{
			name: "cursor up ESC[1A replaces from current col",
			// After "line1\n" row=1 col=0; "line2" advances col to 5; ESC[1A moves row=0;
			// "replaced" writes at col=5 on row 0, overwriting from there.
			// The implementation does NOT reset col on cursor-up, so writes continue from col 5.
			input: "line1\nline2\x1b[1Areplaced",
			want:  "line1replaced\nline2",
		},
		{
			name: "cursor up ESC[2A",
			// "a\nb\nc" → after 'c' row=2,col=1; ESC[2A → row=0,col=1; "X" writes at (0,1)
			input: "a\nb\nc\x1b[2AX",
			want:  "aX\nb\nc",
		},
		{
			name:  "erase whole line ESC[2K",
			input: "abc\x1b[2K",
			want:  "",
		},
		{
			name: "erase to end ESC[0K",
			// \r resets col to 0; ESC[0K erases from col 0 to end → empty; "xy" writes at col 0
			// Result: row 0 = "xy" (since abcde was erased and xy written at start)
			input: "abcde\r\x1b[0Kxy",
			want:  "xy",
		},
		{
			name:  "SGR color stripped",
			input: "\x1b[31mred\x1b[0m",
			want:  "red",
		},
		{
			name:  "cursor up past top clamped",
			input: "\x1b[99Atext",
			want:  "text",
		},
		{
			name:  "trailing spaces trimmed",
			input: "abc   ",
			want:  "abc",
		},
		{
			name:  "empty string",
			input: "",
			want:  "",
		},
		{
			name:  "newlines only",
			input: "\n\n",
			want:  "\n\n",
		},
		{
			name:  "mixed newline and CR",
			input: "step 1\nstep 2\r\x1b[2Kstep 2 updated",
			want:  "step 1\nstep 2 updated",
		},
		{
			name:  "no trailing newline added",
			input: "hello",
			want:  "hello",
		},
		{
			name:  "cursor up then rewrite",
			input: "first\nsecond\x1b[1A\rthird",
			want:  "third\nsecond",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := renderANSI(tt.input)
			if got != tt.want {
				t.Errorf("renderANSI(%q)\n  got:  %q\n  want: %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestRenderANSI_EraseToEnd(t *testing.T) {
	// Write "abcde", \r resets col to 0, ESC[0K erases from col 0 to end → empty line
	input := "abcde\r\x1b[0K"
	got := renderANSI(input)
	if strings.TrimSpace(got) != "" {
		t.Errorf("erase to end: got %q, want empty", got)
	}
}

// --- Tier 4: Gate Run and EnsureOCIS (subprocess) ---

func TestRun_GatePassed(t *testing.T) {
	dir := t.TempDir()
	outputDir := filepath.Join(dir, "output")
	if err := os.MkdirAll(outputDir, 0o755); err != nil {
		t.Fatal(err)
	}
	gateJSON := `{"passed":true,"score":1.0,"stages":{"hygiene":"ok","build":"ok","lint":"ok","unit":"ok","e2e":"skip"}}`
	// Pre-write gate.json; Run() calls MkdirAll (no-op) then cmd.Run() then reads gate.json.
	if err := os.WriteFile(filepath.Join(outputDir, "gate.json"), []byte(gateJSON), 0o644); err != nil {
		t.Fatal(err)
	}
	execCommand = noopExec(t)

	result, err := Run("fake-script", dir, "ext-id", outputDir, 5, "", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.Passed {
		t.Error("expected Passed=true")
	}
	if result.Score != 1.0 {
		t.Errorf("Score = %f, want 1.0", result.Score)
	}
	if result.Stages.E2E != "skip" {
		t.Errorf("Stages.E2E = %q, want skip", result.Stages.E2E)
	}
}

func TestRun_GateFailed(t *testing.T) {
	dir := t.TempDir()
	outputDir := filepath.Join(dir, "output")
	if err := os.MkdirAll(outputDir, 0o755); err != nil {
		t.Fatal(err)
	}
	gateJSON := `{"passed":false,"score":0.5,"stages":{"hygiene":"ok","build":"fail","lint":"fail","unit":"fail","e2e":"skip"}}`
	if err := os.WriteFile(filepath.Join(outputDir, "gate.json"), []byte(gateJSON), 0o644); err != nil {
		t.Fatal(err)
	}
	execCommand = noopExec(t)

	result, err := Run("fake-script", dir, "ext-id", outputDir, 5, "", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Passed {
		t.Error("expected Passed=false")
	}
	if result.Score != 0.5 {
		t.Errorf("Score = %f, want 0.5", result.Score)
	}
	if result.Stages.Build != "fail" {
		t.Errorf("Stages.Build = %q, want fail", result.Stages.Build)
	}
}

func TestRun_NoGateJSON(t *testing.T) {
	dir := t.TempDir()
	outputDir := filepath.Join(dir, "output")
	// Don't write gate.json; expect error from os.ReadFile.
	execCommand = noopExec(t)

	_, err := Run("fake-script", dir, "ext-id", outputDir, 5, "", "")
	if err == nil {
		t.Error("expected error when gate.json missing, got nil")
	}
	if !strings.Contains(err.Error(), "gate.json") {
		t.Errorf("error %q should mention gate.json", err.Error())
	}
}

func TestEnsureOCIS_AlreadyRunning(t *testing.T) {
	execCommand = outputExec(t, "abc123\n")
	dir := t.TempDir()
	if err := EnsureOCIS(dir, ""); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestEnsureOCIS_StartsContainer(t *testing.T) {
	// Set health URL to an httptest server so polling resolves immediately.
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(ts.Close)
	original := ocisHealthURL
	t.Cleanup(func() { ocisHealthURL = original })
	ocisHealthURL = ts.URL

	callCount := 0
	origExec := execCommand
	t.Cleanup(func() { execCommand = origExec })
	execCommand = func(name string, args ...string) *exec.Cmd {
		callCount++
		// First call: docker ps → print nothing (not running).
		// Second call: docker compose up → exit 0.
		stdout := ""
		exit := "0"
		cmd := exec.Command(os.Args[0], "-test.run=TestHelperProcess", "--")
		cmd.Env = append(os.Environ(),
			"GO_WANT_HELPER_PROCESS=1",
			"GO_HELPER_STDOUT="+stdout,
			"GO_HELPER_EXIT="+exit,
		)
		return cmd
	}

	dir := t.TempDir()
	if err := EnsureOCIS(dir, ""); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if callCount < 2 {
		t.Errorf("expected at least 2 execCommand calls (ps + up), got %d", callCount)
	}
}
