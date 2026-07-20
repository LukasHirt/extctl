package build

import (
	"fmt"
	"os"
	"os/exec"
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
	"Bash(pnpm install *)", "Bash(pnpm build)", "Bash(pnpm test:unit *)",
	"Bash(pnpm lint *)", "Bash(pnpm check:types)", "Bash(git add *)",
	"Bash(git commit *)", "Bash(git status)", "Bash(git diff *)",
	"Bash(git rm -f *)",
}

// Rebase-repair tool allowlist: narrower than buildTools. Resolving a rebase
// conflict only requires editing conflicted files, staging them, and
// continuing the rebase — no build/lint/test/commit access, and no
// `git rebase --abort` (only the orchestrator decides to abort and retry, so
// it can tell a genuinely finished rebase apart from an abandoned one).
var rebaseTools = []string{
	"Read", "Edit", "Grep", "Glob",
	"Bash(git status)", "Bash(git diff *)", "Bash(git add *)",
	"Bash(git rebase --continue)", "Bash(pnpm install *)",
}

// RepairRebase runs a single attempt at resolving an in-progress rebase
// conflict against origin/<defaultBranch>, using the rebase-repair prompt.
// attempt is 1-indexed and used to name the output file
// (rebase-repair-1.jsonl, rebase-repair-2.jsonl, …). Each attempt starts a
// fresh Claude session — the caller aborts and restarts the rebase between
// attempts, so there is no prior session state worth resuming.
func RepairRebase(opts Options, defaultBranch string, attempt int) (*RunResult, error) {
	promptPath := opts.Config.Prompts.RebaseRepair
	promptBytes, err := os.ReadFile(promptPath)
	if err != nil {
		return nil, fmt.Errorf("read rebase-repair prompt %s: %w", promptPath, err)
	}
	prompt := renderTemplate(string(promptBytes), map[string]string{
		"{{EXT_ID}}":         opts.CandidateID,
		"{{DEFAULT_BRANCH}}": defaultBranch,
	})

	outputFile := filepath.Join(opts.Config.RunsDir, opts.Date, opts.CandidateID,
		fmt.Sprintf("rebase-repair-%d.jsonl", attempt))

	claudeOpts := claude.RunOptions{
		Prompt:       prompt,
		AllowedTools: rebaseTools,
		Model:        opts.Config.Claude.VersionPin,
		WorkDir:      opts.WorktreePath,
		OutputFile:   outputFile,
	}

	opts.logf("build: running rebase-repair attempt %d…\n", attempt)

	result, err := claude.Run(claudeOpts)
	if err != nil {
		return nil, fmt.Errorf("claude rebase-repair run: %w", err)
	}

	return &RunResult{
		SessionID: result.SessionID,
		CostUSD:   result.TotalCostUSD,
		Turns:     result.NumTurns,
		Attempts:  1,
		Success:   true,
	}, nil
}

// Repair runs a single repair attempt on gate failure using the same Claude session.
// attempt is 1-indexed and is used to name the output file (repair-1.jsonl, repair-2.jsonl, …).
func Repair(opts Options, gateLog string, sessionID string, attempt int) (*RunResult, error) {
	promptPath := opts.Config.Prompts.Repair
	promptBytes, err := os.ReadFile(promptPath)
	if err != nil {
		return nil, fmt.Errorf("read repair prompt %s: %w", promptPath, err)
	}
	prompt := renderTemplate(string(promptBytes), map[string]string{
		"{{EXT_ID}}":   opts.CandidateID,
		"{{GATE_LOG}}": gateLog,
	})

	outputFile := filepath.Join(opts.Config.RunsDir, opts.Date, opts.CandidateID,
		fmt.Sprintf("repair-%d.jsonl", attempt))

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

// PriorStagesSummary returns a compact git log summary of the last n commits in
// the worktree, showing the subject and changed files for each stage. This gives
// each new build stage enough context to understand what prior stages built
// without replaying the full conversation history.
func PriorStagesSummary(worktreePath string, n int) (string, error) {
	if n <= 0 {
		return "", nil
	}
	cmd := exec.Command("git", "log",
		"--reverse",
		fmt.Sprintf("-n%d", n),
		"--name-status",
		"--format=--- %s ---",
	)
	cmd.Dir = worktreePath
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("git log prior stages: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}
