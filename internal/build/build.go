package build

import (
	"fmt"
	"io"
	"io/fs"
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
	ScaffoldDir  string // directory containing scaffold/ template (defaults to ./scaffold)
	ClaudeMDPath string // path to CLAUDE.md to copy into the worktree (defaults to ./CLAUDE.md)
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

// Run executes the Phase B build:
//  1. Copy scaffold into the worktree at extensions/<id>/
//  2. Copy CLAUDE.md into the worktree root
//  3. Render the build-extension.md prompt with template vars
//  4. Invoke claude with Phase B scoped tools
//  5. Return the result (gate + repair are separate steps in poll.go)
func Run(opts Options) (*RunResult, error) {
	if opts.ScaffoldDir == "" {
		opts.ScaffoldDir = opts.Config.ScaffoldDir
	}
	if opts.ClaudeMDPath == "" {
		opts.ClaudeMDPath = "CLAUDE.md"
	}

	// 1. Copy scaffold into worktree/extensions/<id>/
	extDir := filepath.Join(opts.WorktreePath, "extensions", opts.CandidateID)
	if err := copyScaffold(opts.ScaffoldDir, extDir, opts.CandidateID); err != nil {
		return nil, fmt.Errorf("copy scaffold: %w", err)
	}

	// 2. Copy extctl's CLAUDE.md into worktree root only when the repo doesn't
	// already have one. The repo's own CLAUDE.md takes precedence — it carries
	// project conventions (i18n, component patterns, etc.) that the build agent
	// must follow. Tool restrictions are already enforced via allowedTools.
	dstCLAUDE := filepath.Join(opts.WorktreePath, "CLAUDE.md")
	if _, err := os.Stat(dstCLAUDE); os.IsNotExist(err) {
		if err := copyFile(opts.ClaudeMDPath, dstCLAUDE); err != nil {
			opts.logf("build: warning: could not copy CLAUDE.md: %v\n", err)
		}
	}

	// 3. Render the build-extension.md prompt.
	promptPath := opts.Config.Prompts.Build
	promptBytes, err := os.ReadFile(promptPath)
	if err != nil {
		return nil, fmt.Errorf("read build prompt %s: %w", promptPath, err)
	}
	prompt := renderTemplate(string(promptBytes), map[string]string{
		"{{EXT_ID}}":    opts.CandidateID,
		"{{EXT_TITLE}}": titleFromSpec(opts.SpecMD, opts.CandidateID),
		"{{SPEC_MD}}":   opts.SpecMD,
		"{{EFFORT}}":    opts.Effort,
	})

	// 4. Archive path.
	outputFile := filepath.Join(opts.Config.RunsDir, opts.Date, opts.CandidateID, "result.json")

	claudeOpts := claude.RunOptions{
		Prompt:       prompt,
		AllowedTools: buildTools,
		Model:        opts.Config.Claude.VersionPin,
		WorkDir:      opts.WorktreePath,
		OutputFile:   outputFile,
	}

	opts.logf("build: running claude (effort %s, workdir %s)…\n", opts.Effort, opts.WorktreePath)

	result, err := claude.Run(claudeOpts)
	if err != nil {
		return &RunResult{
			Success:  false,
			ErrorMsg: err.Error(),
			Attempts: 1,
		}, fmt.Errorf("claude build run: %w", err)
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

// copyScaffold copies the scaffold directory tree into dst, substituting
// template variables in file contents but not file names.
func copyScaffold(src, dst, extID string) error {
	vars := map[string]string{
		"{{EXT_ID}}":          extID,
		"{{EXT_TITLE}}":       extID, // placeholder; build prompt fills the real title
		"{{EXT_DESCRIPTION}}": "",
	}
	return filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, path)
		dstPath := filepath.Join(dst, rel)

		if d.IsDir() {
			return os.MkdirAll(dstPath, 0o755)
		}
		if d.Name() == ".gitkeep" {
			return os.MkdirAll(filepath.Dir(dstPath), 0o755)
		}
		return copyFileWithVars(path, dstPath, vars)
	})
}

func copyFileWithVars(src, dst string, vars map[string]string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return fmt.Errorf("read %s: %w", src, err)
	}
	content := string(data)
	for k, v := range vars {
		content = strings.ReplaceAll(content, k, v)
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", filepath.Dir(dst), err)
	}
	return os.WriteFile(dst, []byte(content), 0o644)
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, in)
	return err
}

func renderTemplate(tmpl string, vars map[string]string) string {
	for k, v := range vars {
		tmpl = strings.ReplaceAll(tmpl, k, v)
	}
	return tmpl
}

// titleFromSpec extracts the title field from the ## CANDIDATE block, or falls
// back to the ID if the block isn't parseable inline.
func titleFromSpec(specMD, fallback string) string {
	for line := range strings.SplitSeq(specMD, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(strings.ToLower(trimmed), "title:") {
			return strings.TrimSpace(trimmed[len("title:"):])
		}
	}
	return fallback
}
