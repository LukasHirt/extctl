package build

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestHelperProcess is the subprocess helper used by Tier 4 tests.
// When GO_WANT_HELPER_PROCESS=1, it prints GO_HELPER_STDOUT and exits.
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

func TestRenderTemplate(t *testing.T) {
	tests := []struct {
		name string
		tmpl string
		vars map[string]string
		want string
	}{
		{
			name: "single substitution",
			tmpl: "hello {{NAME}}",
			vars: map[string]string{"{{NAME}}": "world"},
			want: "hello world",
		},
		{
			name: "no placeholders",
			tmpl: "hello world",
			vars: map[string]string{},
			want: "hello world",
		},
		{
			name: "multiple substitutions",
			tmpl: "{{A}} and {{B}}",
			vars: map[string]string{"{{A}}": "x", "{{B}}": "y"},
			want: "x and y",
		},
		{
			name: "repeated placeholder",
			tmpl: "{{X}} plus {{X}}",
			vars: map[string]string{"{{X}}": "z"},
			want: "z plus z",
		},
		{
			name: "missing var left as-is",
			tmpl: "{{MISSING}}",
			vars: map[string]string{},
			want: "{{MISSING}}",
		},
		{
			name: "empty template",
			tmpl: "",
			vars: map[string]string{"{{X}}": "y"},
			want: "",
		},
		{
			name: "multiline template",
			tmpl: "line1\n{{KEY}}\nline3",
			vars: map[string]string{"{{KEY}}": "line2"},
			want: "line1\nline2\nline3",
		},
		{
			name: "empty var map",
			tmpl: "no vars",
			vars: nil,
			want: "no vars",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := renderTemplate(tt.tmpl, tt.vars)
			if got != tt.want {
				t.Errorf("renderTemplate(%q, %v) = %q, want %q", tt.tmpl, tt.vars, got, tt.want)
			}
		})
	}
}

func TestPriorStagesSummary(t *testing.T) {
	dir := t.TempDir()

	mustRun := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}

	mustRun("init")
	mustRun("config", "user.email", "test@test.com")
	mustRun("config", "user.name", "Test")

	writeFile := func(name, content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	writeFile("a.txt", "first")
	mustRun("add", "a.txt")
	mustRun("commit", "-m", "feat: stage 1 implementation")

	writeFile("b.txt", "second")
	mustRun("add", "b.txt")
	mustRun("commit", "-m", "feat: stage 2 implementation")

	t.Run("n=0 returns empty", func(t *testing.T) {
		got, err := PriorStagesSummary(dir, 0)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != "" {
			t.Errorf("PriorStagesSummary(dir, 0) = %q, want empty", got)
		}
	})

	t.Run("n=1 returns last commit", func(t *testing.T) {
		got, err := PriorStagesSummary(dir, 1)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !strings.Contains(got, "stage 2") {
			t.Errorf("summary missing 'stage 2':\n%s", got)
		}
	})

	t.Run("n=2 returns both commits", func(t *testing.T) {
		got, err := PriorStagesSummary(dir, 2)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !strings.Contains(got, "stage 1") {
			t.Errorf("summary missing 'stage 1':\n%s", got)
		}
		if !strings.Contains(got, "stage 2") {
			t.Errorf("summary missing 'stage 2':\n%s", got)
		}
	})
}
