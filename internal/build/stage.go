package build

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	"github.com/LukasHirt/extctl/internal/claude"
	"github.com/LukasHirt/extctl/internal/config"
)

// StageOptions configures a per-stage Claude build run.
type StageOptions struct {
	Config        *config.Config
	CandidateID   string
	Title         string // display title of the extension
	Effort        string // S | M | L
	SpecMD        string
	IssueComments string // formatted Jira issue comments fetched at pick time
	PlanPath      string // absolute path to plan.md
	StagesPath    string // absolute path to stages.md
	StageNum      int    // 1-indexed current stage number
	TotalStages   int
	StageDesc     string // description from stages.md for this stage
	WorktreePath  string // absolute path to git worktree
	Date          string // YYYY-MM-DD
	LogPrefix     string
	PriorWork     string // git log summary of files committed in prior stages
}

func (opts StageOptions) logf(format string, args ...any) {
	fmt.Printf(opts.LogPrefix+format, args...)
}

// BuildStage runs one stage of the per-stage build loop.
// Each stage starts a fresh Claude session; prior stage context is provided via
// opts.PriorWork (a compact git log summary) rather than session resumption.
func BuildStage(opts StageOptions) (*RunResult, error) {
	cfg := opts.Config

	promptPath := cfg.Prompts.BuildStage
	promptBytes, err := os.ReadFile(promptPath)
	if err != nil {
		return nil, fmt.Errorf("read build-stage prompt %s: %w", promptPath, err)
	}

	prompt := renderTemplate(string(promptBytes), map[string]string{
		"{{EXT_ID}}":         opts.CandidateID,
		"{{EXT_TITLE}}":      opts.Title,
		"{{EFFORT}}":         opts.Effort,
		"{{SPEC_MD}}":        opts.SpecMD,
		"{{ISSUE_COMMENTS}}": opts.IssueComments,
		"{{PLAN_PATH}}":      opts.PlanPath,
		"{{STAGES_PATH}}":    opts.StagesPath,
		"{{STAGE_NUM}}":      strconv.Itoa(opts.StageNum),
		"{{TOTAL_STAGES}}":   strconv.Itoa(opts.TotalStages),
		"{{STAGE_DESC}}":     opts.StageDesc,
		"{{PRIOR_WORK}}":     opts.PriorWork,
	})

	outputFile := filepath.Join(
		cfg.RunsDir, opts.Date, opts.CandidateID,
		fmt.Sprintf("stage-%d.jsonl", opts.StageNum),
	)

	claudeOpts := claude.RunOptions{
		Prompt:       prompt,
		AllowedTools: buildTools,
		Model:        cfg.Claude.VersionPin,
		WorkDir:      opts.WorktreePath,
		OutputFile:   outputFile,
	}

	opts.logf("build: stage %d/%d — %s (workdir %s)…\n",
		opts.StageNum, opts.TotalStages, opts.StageDesc, opts.WorktreePath)

	result, err := claude.Run(claudeOpts)
	if err != nil {
		return &RunResult{
			Success:  false,
			ErrorMsg: err.Error(),
			Attempts: 1,
		}, fmt.Errorf("claude build-stage %d run: %w", opts.StageNum, err)
	}

	return &RunResult{
		SessionID: result.SessionID,
		CostUSD:   result.TotalCostUSD,
		Turns:     result.NumTurns,
		Attempts:  1,
		Success:   true,
	}, nil
}
