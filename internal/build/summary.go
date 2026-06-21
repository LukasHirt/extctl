package build

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/LukasHirt/extctl/internal/claude"
	"github.com/LukasHirt/extctl/internal/config"
)

// SummarizeOptions configures a build-summary synthesis run.
type SummarizeOptions struct {
	Config      *config.Config
	CandidateID string
	Date        string
	SpecMD      string
	OutputDir   string // absolute path to runs/{date}/{id}
	TotalStages int
}

// SynthesizeSummary collects per-stage result texts from stage-{N}.jsonl files,
// runs a Claude synthesis call, writes build-summary.md to OutputDir, and
// returns the content. On any error it returns an empty string so callers
// degrade gracefully to the old behaviour.
func SynthesizeSummary(opts SummarizeOptions) string {
	stageResults := collectStageResults(opts.OutputDir, opts.TotalStages)
	if stageResults == "" {
		return ""
	}

	promptBytes, err := os.ReadFile(opts.Config.Prompts.BuildSummary)
	if err != nil {
		return ""
	}

	prompt := renderTemplate(string(promptBytes), map[string]string{
		"{{SPEC_MD}}":       opts.SpecMD,
		"{{STAGE_RESULTS}}": stageResults,
	})

	result, err := claude.Run(claude.RunOptions{
		Prompt:       prompt,
		AllowedTools: []string{"Read"},
		Model:        opts.Config.Claude.VersionPin,
		OutputFile:   filepath.Join(opts.OutputDir, "build-summary.jsonl"),
	})
	if err != nil || result.Result == "" {
		return ""
	}

	_ = os.WriteFile(
		filepath.Join(opts.OutputDir, "build-summary.md"),
		[]byte(result.Result),
		0o644,
	)
	return result.Result
}

func collectStageResults(outputDir string, totalStages int) string {
	var sb strings.Builder
	for i := 1; i <= totalStages; i++ {
		r, err := claude.LoadResult(filepath.Join(outputDir, fmt.Sprintf("stage-%d.jsonl", i)))
		if err != nil || r.Result == "" {
			continue
		}
		fmt.Fprintf(&sb, "**Stage %d**: %s\n\n", i, r.Result)
	}
	return strings.TrimSpace(sb.String())
}
