package build

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/LukasHirt/extctl/internal/claude"
	"github.com/LukasHirt/extctl/internal/config"
)

// Options configures a Phase B build run.
type Options struct {
	Config       *config.Config
	CandidateID  string
	JiraKey      string
	SpecMD       string // full ## CANDIDATE block from the slate
	Effort       string // S | M | L
	Date         string // YYYY-MM-DD
	WorktreePath string // absolute path to the git worktree
	LogPrefix    string // prepended to all log output, e.g. "[ai-quick-draft-creator] "
}

func (opts Options) logf(format string, args ...any) {
	fmt.Printf(opts.LogPrefix+format, args...)
}

// RunResult is what Run returns to the caller.
type RunResult struct {
	SessionID string
	CostUSD   float64
	Turns     int
	Attempts  int
	Success   bool
	ErrorMsg  string
}

// Phase B tool allowlist per spec §8.2.
// Note the space before * in Bash(git diff *) to avoid matching git diff-index.
var buildTools = []string{
	"Read", "Edit", "Write", "Grep", "Glob",
	"Bash(pnpm install)", "Bash(pnpm build)", "Bash(pnpm test *)",
	"Bash(pnpm lint *)", "Bash(git add *)", "Bash(git commit *)",
	"Bash(git status)", "Bash(git diff *)",
}

// Repair runs a single repair attempt on gate failure using the same Claude session.
func Repair(opts Options, gateLog string, sessionID string) (*RunResult, error) {
	promptPath := opts.Config.Prompts.Repair
	promptBytes, err := os.ReadFile(promptPath)
	if err != nil {
		return nil, fmt.Errorf("read repair prompt %s: %w", promptPath, err)
	}
	prompt := renderTemplate(string(promptBytes), map[string]string{
		"{{EXT_ID}}":   opts.CandidateID,
		"{{GATE_LOG}}": gateLog,
	})

	outputFile := filepath.Join(opts.Config.RunsDir, opts.Date, opts.CandidateID, "repair.json")

	claudeOpts := claude.RunOptions{
		Prompt:       prompt,
		AllowedTools: buildTools,
		Model:        opts.Config.Claude.VersionPin,
		WorkDir:      opts.WorktreePath,
		OutputFile:   outputFile,
		Resume:       sessionID,
	}

	opts.logf("build: running repair (resuming session %s)…\n", sessionID)

	result, err := claude.Run(claudeOpts)
	if err != nil {
		return &RunResult{
			Success:   false,
			ErrorMsg:  err.Error(),
			SessionID: sessionID,
			Attempts:  2,
		}, fmt.Errorf("claude repair run: %w", err)
	}

	return &RunResult{
		SessionID: result.SessionID,
		CostUSD:   result.TotalCostUSD,
		Turns:     result.NumTurns,
		Attempts:  2,
		Success:   true,
	}, nil
}

func renderTemplate(tmpl string, vars map[string]string) string {
	for k, v := range vars {
		tmpl = strings.ReplaceAll(tmpl, k, v)
	}
	return tmpl
}
