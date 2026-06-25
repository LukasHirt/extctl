package github

import (
	"os"
	"os/exec"
	"testing"
)

// TestHelperProcess is the subprocess helper for Tier 4 tests.
func TestHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_HELPER_PROCESS") != "1" {
		return
	}
	stdout := os.Getenv("GO_HELPER_STDOUT")
	if stdout != "" {
		os.Stdout.WriteString(stdout)
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

func fakeExecFail(t *testing.T) func(string, ...string) *exec.Cmd {
	t.Helper()
	original := execCommand
	t.Cleanup(func() { execCommand = original })
	return func(name string, args ...string) *exec.Cmd {
		cmd := exec.Command(os.Args[0], "-test.run=TestHelperProcess", "--")
		cmd.Env = append(os.Environ(),
			"GO_WANT_HELPER_PROCESS=1",
			"GO_HELPER_EXIT=1",
		)
		return cmd
	}
}

func TestCreate_Success(t *testing.T) {
	execCommand = fakeExec(t, "https://github.com/org/repo/pull/42\n")
	pr, err := Create(PROptions{
		RepoSlug: "org/repo",
		Branch:   "my-branch",
		Title:    "My PR",
		Body:     "body",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if pr.Number != 42 {
		t.Errorf("PR.Number = %d, want 42", pr.Number)
	}
	if pr.URL != "https://github.com/org/repo/pull/42" {
		t.Errorf("PR.URL = %q", pr.URL)
	}
}

func TestCreate_DraftFlag(t *testing.T) {
	var capturedArgs []string
	original := execCommand
	t.Cleanup(func() { execCommand = original })
	execCommand = func(name string, args ...string) *exec.Cmd {
		capturedArgs = args
		cmd := exec.Command(os.Args[0], "-test.run=TestHelperProcess", "--")
		cmd.Env = append(os.Environ(),
			"GO_WANT_HELPER_PROCESS=1",
			"GO_HELPER_STDOUT=https://github.com/org/repo/pull/1\n",
		)
		return cmd
	}
	Create(PROptions{RepoSlug: "org/repo", Branch: "b", Title: "t", Body: "b", Draft: true}) //nolint:errcheck
	found := false
	for _, a := range capturedArgs {
		if a == "--draft" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("--draft not in args: %v", capturedArgs)
	}
}

func TestCreate_Labels(t *testing.T) {
	var capturedArgs []string
	original := execCommand
	t.Cleanup(func() { execCommand = original })
	execCommand = func(name string, args ...string) *exec.Cmd {
		capturedArgs = args
		cmd := exec.Command(os.Args[0], "-test.run=TestHelperProcess", "--")
		cmd.Env = append(os.Environ(),
			"GO_WANT_HELPER_PROCESS=1",
			"GO_HELPER_STDOUT=https://github.com/org/repo/pull/1\n",
		)
		return cmd
	}
	Create(PROptions{RepoSlug: "org/repo", Branch: "b", Title: "t", Body: "b", Labels: []string{"ai-generated"}}) //nolint:errcheck
	found := false
	for i, a := range capturedArgs {
		if a == "--label" && i+1 < len(capturedArgs) && capturedArgs[i+1] == "ai-generated" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("--label ai-generated not in args: %v", capturedArgs)
	}
}

func TestCreate_Error(t *testing.T) {
	execCommand = fakeExecFail(t)
	_, err := Create(PROptions{RepoSlug: "org/repo", Branch: "b", Title: "t", Body: "b"})
	if err == nil {
		t.Error("expected error for failing gh, got nil")
	}
}

func TestIsMerged_True(t *testing.T) {
	execCommand = fakeExec(t, `{"merged":true}`)
	merged, err := IsMerged("org/repo", 42)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !merged {
		t.Error("expected merged=true, got false")
	}
}

func TestIsMerged_False(t *testing.T) {
	execCommand = fakeExec(t, `{"merged":false}`)
	merged, err := IsMerged("org/repo", 42)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if merged {
		t.Error("expected merged=false, got true")
	}
}

func TestAddComment_Success(t *testing.T) {
	execCommand = fakeExec(t, "")
	if err := AddComment("org/repo", 42, "nice pr"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestSetReady_Success(t *testing.T) {
	execCommand = fakeExec(t, "")
	if err := SetReady("org/repo", 42); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}
