package config_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/LukasHirt/extctl/internal/config"
)

func writeConfig(t *testing.T, dir, content string) string {
	t.Helper()
	p := filepath.Join(dir, "extctl.yaml")
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestLoad_Defaults(t *testing.T) {
	dir := t.TempDir()
	p := writeConfig(t, dir, "jira:\n  base_url: https://example.atlassian.net\n")
	cfg, err := config.Load(p)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Timezone != "Europe/Prague" {
		t.Errorf("Timezone = %q, want %q", cfg.Timezone, "Europe/Prague")
	}
	if cfg.FreshCandidatesPerDay != 3 {
		t.Errorf("FreshCandidatesPerDay = %d, want 3", cfg.FreshCandidatesPerDay)
	}
	if cfg.DefaultBranch != "main" {
		t.Errorf("DefaultBranch = %q, want %q", cfg.DefaultBranch, "main")
	}
	if cfg.Jira.CandidateStatus != "Needs Approval" {
		t.Errorf("Jira.CandidateStatus = %q, want %q", cfg.Jira.CandidateStatus, "Needs Approval")
	}
	if cfg.Jira.PickStatus != "Doing" {
		t.Errorf("Jira.PickStatus = %q, want %q", cfg.Jira.PickStatus, "Doing")
	}
	if cfg.Jira.DeclineStatus != "Not Doing" {
		t.Errorf("Jira.DeclineStatus = %q, want %q", cfg.Jira.DeclineStatus, "Not Doing")
	}
	if cfg.Jira.PollIntervalMin != 10 {
		t.Errorf("Jira.PollIntervalMin = %d, want 10", cfg.Jira.PollIntervalMin)
	}
	if cfg.Claude.MaxRepairAttempts != 3 {
		t.Errorf("Claude.MaxRepairAttempts = %d, want 3", cfg.Claude.MaxRepairAttempts)
	}
	if cfg.Claude.BudgetUSDPerBuild != 8 {
		t.Errorf("Claude.BudgetUSDPerBuild = %f, want 8", cfg.Claude.BudgetUSDPerBuild)
	}
	if cfg.Claude.BudgetUSDPerDay != 20 {
		t.Errorf("Claude.BudgetUSDPerDay = %f, want 20", cfg.Claude.BudgetUSDPerDay)
	}
	if cfg.Decay.MaxAppearances != 3 {
		t.Errorf("Decay.MaxAppearances = %d, want 3", cfg.Decay.MaxAppearances)
	}
	if cfg.Decay.Action != "backlog" {
		t.Errorf("Decay.Action = %q, want %q", cfg.Decay.Action, "backlog")
	}
	if cfg.Prompts.GenSpecs != "prompts/gen-specs.md" {
		t.Errorf("Prompts.GenSpecs = %q, want %q", cfg.Prompts.GenSpecs, "prompts/gen-specs.md")
	}
	if cfg.IdeaPool != "idea-pool.yaml" {
		t.Errorf("IdeaPool = %q, want %q", cfg.IdeaPool, "idea-pool.yaml")
	}
	if !filepath.IsAbs(cfg.RunsDir) {
		t.Errorf("RunsDir %q is not absolute", cfg.RunsDir)
	}
}

func TestLoad_OverridesDefaults(t *testing.T) {
	dir := t.TempDir()
	yaml := `
timezone: "America/New_York"
fresh_candidates_per_day: 5
default_branch: develop
jira:
  base_url: https://example.atlassian.net
  candidate_status: "To Do"
  pick_status: "In Progress"
  decline_status: "Won't Do"
  poll_interval_min: 5
claude:
  max_repair_attempts: 2
  budget_usd_per_build: 10
  budget_usd_per_day: 30
decay:
  max_appearances: 2
  action: decline
runs_dir: /tmp/test-runs
`
	p := writeConfig(t, dir, yaml)
	cfg, err := config.Load(p)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Timezone != "America/New_York" {
		t.Errorf("Timezone = %q", cfg.Timezone)
	}
	if cfg.FreshCandidatesPerDay != 5 {
		t.Errorf("FreshCandidatesPerDay = %d", cfg.FreshCandidatesPerDay)
	}
	if cfg.DefaultBranch != "develop" {
		t.Errorf("DefaultBranch = %q", cfg.DefaultBranch)
	}
	if cfg.Jira.CandidateStatus != "To Do" {
		t.Errorf("Jira.CandidateStatus = %q", cfg.Jira.CandidateStatus)
	}
	if cfg.Jira.PollIntervalMin != 5 {
		t.Errorf("Jira.PollIntervalMin = %d", cfg.Jira.PollIntervalMin)
	}
	if cfg.Claude.MaxRepairAttempts != 2 {
		t.Errorf("Claude.MaxRepairAttempts = %d", cfg.Claude.MaxRepairAttempts)
	}
	if cfg.Claude.BudgetUSDPerBuild != 10 {
		t.Errorf("Claude.BudgetUSDPerBuild = %f", cfg.Claude.BudgetUSDPerBuild)
	}
	if cfg.Decay.MaxAppearances != 2 {
		t.Errorf("Decay.MaxAppearances = %d", cfg.Decay.MaxAppearances)
	}
	if cfg.Decay.Action != "decline" {
		t.Errorf("Decay.Action = %q", cfg.Decay.Action)
	}
	if cfg.RunsDir != "/tmp/test-runs" {
		t.Errorf("RunsDir = %q", cfg.RunsDir)
	}
}

func TestLoad_FileNotFound(t *testing.T) {
	_, err := config.Load("/nonexistent/path/extctl.yaml")
	if err == nil {
		t.Error("expected error for missing file, got nil")
	}
	if !strings.Contains(err.Error(), "/nonexistent/path/extctl.yaml") {
		t.Errorf("error %q does not mention file path", err.Error())
	}
}

func TestLoad_MalformedYAML(t *testing.T) {
	dir := t.TempDir()
	p := writeConfig(t, dir, "jira: [unclosed")
	_, err := config.Load(p)
	if err == nil {
		t.Error("expected error for malformed YAML, got nil")
	}
}

func TestLoad_RunsDirAbsolute(t *testing.T) {
	dir := t.TempDir()
	p := writeConfig(t, dir, "jira:\n  base_url: x\nruns_dir: ./relative/path\n")
	cfg, err := config.Load(p)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !filepath.IsAbs(cfg.RunsDir) {
		t.Errorf("RunsDir %q is not absolute after load", cfg.RunsDir)
	}
}

func TestLoadDotEnv(t *testing.T) {
	tests := []struct {
		name      string
		content   string
		presetEnv map[string]string
		wantEnv   map[string]string
		wantErr   bool
	}{
		{
			name:    "simple KEY=VALUE",
			content: "EXTCTL_TEST_FOO=bar\n",
			wantEnv: map[string]string{"EXTCTL_TEST_FOO": "bar"},
		},
		{
			name:    "double quoted value",
			content: `EXTCTL_TEST_FOO="bar"` + "\n",
			wantEnv: map[string]string{"EXTCTL_TEST_FOO": "bar"},
		},
		{
			name:    "single quoted value",
			content: "EXTCTL_TEST_FOO='bar'\n",
			wantEnv: map[string]string{"EXTCTL_TEST_FOO": "bar"},
		},
		{
			name:    "comment ignored",
			content: "# this is a comment\nEXTCTL_TEST_FOO=baz\n",
			wantEnv: map[string]string{"EXTCTL_TEST_FOO": "baz"},
		},
		{
			name:      "pre-set env not overwritten",
			content:   "EXTCTL_TEST_FOO=new\n",
			presetEnv: map[string]string{"EXTCTL_TEST_FOO": "existing"},
			wantEnv:   map[string]string{"EXTCTL_TEST_FOO": "existing"},
		},
		{
			name:    "no trailing newline",
			content: "EXTCTL_TEST_FOO=notrail",
			wantEnv: map[string]string{"EXTCTL_TEST_FOO": "notrail"},
		},
		{
			name:    "invalid format",
			content: "NOEQUALS\n",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Set pre-existing env vars using t.Setenv (auto-restored).
			for k, v := range tt.presetEnv {
				t.Setenv(k, v)
			}
			// Ensure keys set by LoadDotEnv are cleaned up after test.
			for k := range tt.wantEnv {
				if _, exists := tt.presetEnv[k]; !exists {
					t.Cleanup(func() { os.Unsetenv(k) })
				}
			}

			dir := t.TempDir()
			p := filepath.Join(dir, ".env")
			if err := os.WriteFile(p, []byte(tt.content), 0o644); err != nil {
				t.Fatal(err)
			}

			err := config.LoadDotEnv(p)
			if tt.wantErr {
				if err == nil {
					t.Error("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			for k, want := range tt.wantEnv {
				if got := os.Getenv(k); got != want {
					t.Errorf("env %s = %q, want %q", k, got, want)
				}
			}
		})
	}

	t.Run("file not found is not an error", func(t *testing.T) {
		if err := config.LoadDotEnv("/nonexistent/.env"); err != nil {
			t.Errorf("unexpected error for missing .env: %v", err)
		}
	})
}

func TestJiraToken(t *testing.T) {
	t.Run("token set", func(t *testing.T) {
		t.Setenv("EXTCTL_JIRA_TOKEN", "my-token")
		got, err := config.JiraToken()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != "my-token" {
			t.Errorf("JiraToken = %q, want %q", got, "my-token")
		}
	})

	t.Run("token not set", func(t *testing.T) {
		t.Setenv("EXTCTL_JIRA_TOKEN", "")
		_, err := config.JiraToken()
		if err == nil {
			t.Error("expected error when token not set, got nil")
		}
		if !strings.Contains(err.Error(), "EXTCTL_JIRA_TOKEN") {
			t.Errorf("error %q should mention env var name", err.Error())
		}
	})
}

func TestJiraEmail(t *testing.T) {
	t.Run("email set", func(t *testing.T) {
		t.Setenv("EXTCTL_JIRA_EMAIL", "test@example.com")
		got, err := config.JiraEmail()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != "test@example.com" {
			t.Errorf("JiraEmail = %q, want %q", got, "test@example.com")
		}
	})

	t.Run("email not set", func(t *testing.T) {
		t.Setenv("EXTCTL_JIRA_EMAIL", "")
		_, err := config.JiraEmail()
		if err == nil {
			t.Error("expected error when email not set, got nil")
		}
		if !strings.Contains(err.Error(), "EXTCTL_JIRA_EMAIL") {
			t.Errorf("error %q should mention env var name", err.Error())
		}
	})
}
