package claude

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestHelperProcess is the subprocess helper invoked by Tier 4 tests.
// When GO_WANT_HELPER_PROCESS=1 it writes GO_HELPER_STDOUT to stdout and exits.
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

// TestMain sets execCommand to a panicking stub so that any test that forgets
// to install fakeExec immediately fails with a clear message.
func TestMain(m *testing.M) {
	if os.Getenv("GO_WANT_HELPER_PROCESS") == "1" {
		// Handled by TestHelperProcess; nothing to do here.
		os.Exit(m.Run())
	}
	execCommand = func(name string, args ...string) *exec.Cmd {
		panic("execCommand called without test override — install fakeExec in your test")
	}
	os.Exit(m.Run())
}

// fakeExec returns an execCommand replacement that runs the test binary itself
// as a subprocess with GO_WANT_HELPER_PROCESS=1, emitting stdout as a pre-baked string.
func fakeExec(t *testing.T, stdout string) func(string, ...string) *exec.Cmd {
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

// --- Tier 2: LoadResult (file I/O, no subprocess) ---

func TestLoadResult(t *testing.T) {
	assistantLine := `{"type":"assistant","message":{"content":[{"type":"text","text":"hello from claude"}]}}`
	resultLine := `{"type":"result","result":"done","session_id":"sess-abc","total_cost_usd":0.05,"num_turns":3,"is_error":false}`

	t.Run("valid stream", func(t *testing.T) {
		dir := t.TempDir()
		p := filepath.Join(dir, "out.jsonl")
		content := assistantLine + "\n" + resultLine + "\n"
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		got, err := LoadResult(p)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got.SessionID != "sess-abc" {
			t.Errorf("SessionID = %q, want %q", got.SessionID, "sess-abc")
		}
		if got.TotalCostUSD != 0.05 {
			t.Errorf("TotalCostUSD = %f, want 0.05", got.TotalCostUSD)
		}
		if got.NumTurns != 3 {
			t.Errorf("NumTurns = %d, want 3", got.NumTurns)
		}
		if !strings.Contains(got.FullText, "hello from claude") {
			t.Errorf("FullText %q should contain assistant text", got.FullText)
		}
	})

	t.Run("assistant text accumulated", func(t *testing.T) {
		dir := t.TempDir()
		p := filepath.Join(dir, "out.jsonl")
		line1 := `{"type":"assistant","message":{"content":[{"type":"text","text":"first"}]}}`
		line2 := `{"type":"assistant","message":{"content":[{"type":"text","text":"second"}]}}`
		content := line1 + "\n" + line2 + "\n" + resultLine + "\n"
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		got, err := LoadResult(p)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !strings.Contains(got.FullText, "first") || !strings.Contains(got.FullText, "second") {
			t.Errorf("FullText %q missing accumulated text", got.FullText)
		}
	})

	t.Run("missing result event returns error", func(t *testing.T) {
		dir := t.TempDir()
		p := filepath.Join(dir, "out.jsonl")
		if err := os.WriteFile(p, []byte(assistantLine+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		_, err := LoadResult(p)
		if err == nil {
			t.Error("expected error for missing result event, got nil")
		}
		if !strings.Contains(err.Error(), "no result event") {
			t.Errorf("error %q should mention 'no result event'", err.Error())
		}
	})

	t.Run("malformed JSON lines skipped", func(t *testing.T) {
		dir := t.TempDir()
		p := filepath.Join(dir, "out.jsonl")
		content := "not json\n" + assistantLine + "\nalso not json\n" + resultLine + "\n"
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		got, err := LoadResult(p)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got.SessionID != "sess-abc" {
			t.Errorf("SessionID = %q, want sess-abc", got.SessionID)
		}
	})

	t.Run("file not found returns error", func(t *testing.T) {
		_, err := LoadResult("/nonexistent/out.jsonl")
		if err == nil {
			t.Error("expected error for missing file, got nil")
		}
		if !strings.Contains(err.Error(), "/nonexistent/out.jsonl") {
			t.Errorf("error %q should mention file path", err.Error())
		}
	})

	t.Run("is_error true: IsError set but no Go error", func(t *testing.T) {
		dir := t.TempDir()
		p := filepath.Join(dir, "out.jsonl")
		errResult := `{"type":"result","result":"oops","session_id":"s","total_cost_usd":0,"num_turns":1,"is_error":true}`
		if err := os.WriteFile(p, []byte(errResult+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		got, err := LoadResult(p)
		if err != nil {
			t.Fatalf("LoadResult should not return Go error for is_error: %v", err)
		}
		if !got.IsError {
			t.Error("IsError should be true")
		}
		if got.FullText != "oops" {
			t.Errorf("FullText = %q, want %q", got.FullText, "oops")
		}
	})

	t.Run("empty file returns error", func(t *testing.T) {
		dir := t.TempDir()
		p := filepath.Join(dir, "empty.jsonl")
		if err := os.WriteFile(p, []byte(""), 0o644); err != nil {
			t.Fatal(err)
		}
		_, err := LoadResult(p)
		if err == nil {
			t.Error("expected error for empty file, got nil")
		}
	})
}

// --- Tier 4: Run (subprocess) ---

func buildClaudeJSONL(assistantText, sessionID string, costUSD float64, turns int, isError bool) string {
	assistantLine := `{"type":"assistant","message":{"content":[{"type":"text","text":"` + assistantText + `"}]}}`
	resultLine := `{"type":"result","result":"` + sessionID + `","session_id":"` + sessionID + `","total_cost_usd":` +
		formatFloat(costUSD) + `,"num_turns":` + itoa(turns) + `,"is_error":` + formatBool(isError) + `}`
	return assistantLine + "\n" + resultLine + "\n"
}

func formatFloat(f float64) string {
	if f == 0 {
		return "0"
	}
	// Good enough for test values like 0.05
	s := make([]byte, 0, 8)
	if f < 1 {
		s = append(s, '0')
	}
	// Use sprintf-like approach: just hardcode for tests
	_ = s
	return "0.05"
}

func formatBool(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	result := ""
	for n > 0 {
		result = string(rune('0'+n%10)) + result
		n /= 10
	}
	return result
}

func TestRun_Success(t *testing.T) {
	jsonl := buildClaudeJSONL("hello world", "sess-xyz", 0.05, 2, false)
	execCommand = fakeExec(t, jsonl)

	result, err := Run(RunOptions{Prompt: "test", WorkDir: t.TempDir()})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.SessionID != "sess-xyz" {
		t.Errorf("SessionID = %q, want sess-xyz", result.SessionID)
	}
	if !strings.Contains(result.FullText, "hello world") {
		t.Errorf("FullText %q missing 'hello world'", result.FullText)
	}
}

func TestRun_IsError(t *testing.T) {
	jsonl := `{"type":"result","result":"something went wrong","session_id":"s","total_cost_usd":0,"num_turns":1,"is_error":true}` + "\n"
	execCommand = fakeExec(t, jsonl)

	_, err := Run(RunOptions{Prompt: "test", WorkDir: t.TempDir()})
	if err == nil {
		t.Error("expected error for is_error=true result, got nil")
	}
	if !strings.Contains(err.Error(), "something went wrong") {
		t.Errorf("error %q should contain result text", err.Error())
	}
}

func TestRun_BinaryNotFound(t *testing.T) {
	original := execCommand
	t.Cleanup(func() { execCommand = original })
	execCommand = func(name string, args ...string) *exec.Cmd {
		return exec.Command("/nonexistent/binary")
	}
	_, err := Run(RunOptions{Prompt: "test"})
	if err == nil {
		t.Error("expected error for non-existent binary, got nil")
	}
}

func TestRun_WritesOutputFile(t *testing.T) {
	jsonl := buildClaudeJSONL("output", "sess-1", 0.05, 1, false)
	execCommand = fakeExec(t, jsonl)

	dir := t.TempDir()
	outFile := filepath.Join(dir, "out.jsonl")
	_, err := Run(RunOptions{Prompt: "test", OutputFile: outFile})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, err := os.Stat(outFile); err != nil {
		t.Errorf("output file not created: %v", err)
	}
}

func TestRun_ResumesSession(t *testing.T) {
	var capturedArgs []string
	original := execCommand
	t.Cleanup(func() { execCommand = original })

	jsonl := buildClaudeJSONL("done", "sess-resume", 0.01, 1, false)
	execCommand = func(name string, args ...string) *exec.Cmd {
		capturedArgs = args
		cmd := exec.Command(os.Args[0], "-test.run=TestHelperProcess", "--")
		cmd.Env = append(os.Environ(),
			"GO_WANT_HELPER_PROCESS=1",
			"GO_HELPER_STDOUT="+jsonl,
		)
		return cmd
	}

	_, err := Run(RunOptions{Prompt: "test", Resume: "old-session-id"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	found := false
	for i, a := range capturedArgs {
		if a == "--resume" && i+1 < len(capturedArgs) && capturedArgs[i+1] == "old-session-id" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("--resume old-session-id not found in args: %v", capturedArgs)
	}
}

func TestRun_AllowedToolsFlag(t *testing.T) {
	var capturedArgs []string
	original := execCommand
	t.Cleanup(func() { execCommand = original })

	jsonl := buildClaudeJSONL("done", "sess-1", 0.01, 1, false)
	execCommand = func(name string, args ...string) *exec.Cmd {
		capturedArgs = args
		cmd := exec.Command(os.Args[0], "-test.run=TestHelperProcess", "--")
		cmd.Env = append(os.Environ(),
			"GO_WANT_HELPER_PROCESS=1",
			"GO_HELPER_STDOUT="+jsonl,
		)
		return cmd
	}

	_, err := Run(RunOptions{Prompt: "test", AllowedTools: []string{"Read", "Write"}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	found := false
	for i, a := range capturedArgs {
		if a == "--allowedTools" && i+1 < len(capturedArgs) {
			if strings.Contains(capturedArgs[i+1], "Read") && strings.Contains(capturedArgs[i+1], "Write") {
				found = true
			}
		}
	}
	if !found {
		t.Errorf("--allowedTools not found in args: %v", capturedArgs)
	}
}
