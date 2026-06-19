package build

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/LukasHirt/extctl/internal/claude"
	"github.com/LukasHirt/extctl/internal/config"
)

// planTools is the tool allowlist for the planning phase.
// Claude may read the codebase and write the plan file, but must not
// create or edit any source files.
var planTools = []string{"Read", "Grep", "Glob", "Write"}

// Plan runs the Phase A.5 planning step for a single candidate extension.
// It reads the plan-extension.md prompt template, substitutes template
// variables, and invokes claude headlessly in the web-extensions checkout.
//
// Parameters:
//   - cfg           – loaded extctl config
//   - id            – extension ID, e.g. "web-app-ai-doc-summary"
//   - specMD        – full ## CANDIDATE block from the slate
//   - issueComments – formatted Jira issue comments (from jira.FormatComments)
//   - planPath      – absolute path where Claude should write plan.md
func Plan(cfg *config.Config, id, specMD, issueComments, planPath string) error {
	promptPath := cfg.Prompts.Plan
	promptBytes, err := os.ReadFile(promptPath)
	if err != nil {
		return fmt.Errorf("read plan prompt %s: %w", promptPath, err)
	}

	prompt := renderTemplate(string(promptBytes), map[string]string{
		"{{EXT_ID}}":        id,
		"{{SPEC_MD}}":       specMD,
		"{{ISSUE_COMMENTS}}": issueComments,
		"{{PLAN_PATH}}":     planPath,
	})

	outputFile := filepath.Join(
		filepath.Dir(planPath),
		strings.TrimSuffix(filepath.Base(planPath), ".md")+".jsonl",
	)

	claudeOpts := claude.RunOptions{
		Prompt:       prompt,
		AllowedTools: planTools,
		Model:        cfg.Claude.VersionPin,
		WorkDir:      cfg.TargetRepo.Checkout,
		OutputFile:   outputFile,
	}

	result, err := claude.Run(claudeOpts)
	if err != nil {
		return fmt.Errorf("claude plan run: %w", err)
	}
	if result.IsError {
		return fmt.Errorf("claude plan returned error: %s", result.Result)
	}
	if result.Result == "" {
		return fmt.Errorf("claude plan returned empty result")
	}

	return nil
}
