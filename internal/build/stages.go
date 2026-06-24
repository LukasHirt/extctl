package build

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/LukasHirt/extctl/internal/claude"
	"github.com/LukasHirt/extctl/internal/config"
)

// stagesTools is the tool allowlist for stage derivation.
// Claude may read the plan and write stages.md, but must not touch source files.
var stagesTools = []string{"Read", "Write"}

// stageLineRe matches both checked and unchecked stage lines:
//
//	- [ ] N. description
//	- [x] N. description
var stageLineRe = regexp.MustCompile(`^- \[[ x]\] (\d+)\. (.+)$`)

// DeriveStages invokes Claude to read planPath and write stages.md to stagesPath.
// Returns the Claude invocation cost in USD.
func DeriveStages(cfg *config.Config, id, planPath, stagesPath, issueComments string) (float64, error) {
	promptPath := cfg.Prompts.DeriveStages
	promptBytes, err := os.ReadFile(promptPath)
	if err != nil {
		return 0, fmt.Errorf("read derive-stages prompt %s: %w", promptPath, err)
	}

	prompt := renderTemplate(string(promptBytes), map[string]string{
		"{{EXT_ID}}":         id,
		"{{PLAN_PATH}}":      planPath,
		"{{STAGES_PATH}}":    stagesPath,
		"{{ISSUE_COMMENTS}}": issueComments,
	})

	outputFile := filepath.Join(
		filepath.Dir(stagesPath),
		strings.TrimSuffix(filepath.Base(stagesPath), ".md")+".jsonl",
	)

	claudeOpts := claude.RunOptions{
		Prompt:       prompt,
		AllowedTools: stagesTools,
		Model:        cfg.Claude.VersionPin,
		WorkDir:      cfg.TargetRepo.Checkout,
		OutputFile:   outputFile,
	}

	result, err := claude.Run(claudeOpts)
	if err != nil {
		return 0, fmt.Errorf("claude derive-stages run: %w", err)
	}
	if result.IsError {
		return 0, fmt.Errorf("claude derive-stages returned error: %s", result.Result)
	}
	if result.Result == "" {
		return 0, fmt.Errorf("claude derive-stages returned empty result")
	}

	return result.TotalCostUSD, nil
}

// docStageText is the canonical description of the fixed documentation stage.
const docStageText = "Update README.md (and CLAUDE.md if present) for the extension"

// AppendDocStage appends the fixed documentation stage to an existing stages.md.
// The new stage number is one more than the current highest stage.
// If the last stage already matches the doc stage text, this is a no-op (idempotent).
func AppendDocStage(stagesPath string) error {
	stages, err := ParseStages(stagesPath)
	if err != nil {
		return fmt.Errorf("parse stages for doc append: %w", err)
	}

	// Idempotency guard: skip if the doc stage is already the last stage.
	if len(stages) > 0 && stages[len(stages)-1] == docStageText {
		return nil
	}

	n := len(stages) + 1
	line := fmt.Sprintf("- [ ] %d. %s\n", n, docStageText)

	data, err := os.ReadFile(stagesPath)
	if err != nil {
		return fmt.Errorf("read stages file %s: %w", stagesPath, err)
	}

	// Ensure the file ends with a newline before appending.
	content := string(data)
	if !strings.HasSuffix(content, "\n") {
		content += "\n"
	}
	content += line

	return writeFileAtomic(stagesPath, []byte(content))
}

// ParseStages reads stages.md and returns stage descriptions (the text after "N. ").
// Returns descriptions for ALL stages (checked and unchecked), ordered by stage number.
func ParseStages(stagesPath string) ([]string, error) {
	data, err := os.ReadFile(stagesPath)
	if err != nil {
		return nil, fmt.Errorf("read stages file %s: %w", stagesPath, err)
	}

	var stages []string
	for _, line := range strings.Split(string(data), "\n") {
		m := stageLineRe.FindStringSubmatch(strings.TrimRight(line, "\r"))
		if m == nil {
			continue
		}
		stages = append(stages, m[2])
	}
	return stages, nil
}

// CheckStage marks stage n (1-indexed) as done in stages.md.
// Replaces "- [ ] N." with "- [x] N." and writes back atomically.
func CheckStage(stagesPath string, n int) error {
	data, err := os.ReadFile(stagesPath)
	if err != nil {
		return fmt.Errorf("read stages file %s: %w", stagesPath, err)
	}

	old := fmt.Sprintf("- [ ] %s.", strconv.Itoa(n))
	new := fmt.Sprintf("- [x] %s.", strconv.Itoa(n))

	content := string(data)
	if !strings.Contains(content, old) {
		return fmt.Errorf("stage %d not found (or already checked) in %s", n, stagesPath)
	}

	// Only replace the first occurrence to avoid touching other stage numbers
	// that might share a numeric prefix (e.g. 1 vs 10, 11, ...).
	// Since the format is "- [ ] N." with an exact number, a simple replacement
	// of the first match is safe here.
	updated := strings.Replace(content, old, new, 1)

	return writeFileAtomic(stagesPath, []byte(updated))
}

// writeFileAtomic writes data to path using a temp-file + rename pattern
// to avoid partial writes. Mirrors the pattern used in state.Save().
func writeFileAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	f, err := os.CreateTemp(dir, "stages-*.md.tmp")
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	tmpPath := f.Name()

	if _, err := f.Write(data); err != nil {
		f.Close()
		os.Remove(tmpPath)
		return fmt.Errorf("write temp file: %w", err)
	}
	if err := f.Close(); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("close temp file: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("rename stages file: %w", err)
	}
	return nil
}
