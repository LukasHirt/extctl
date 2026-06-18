package claude

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Result is the subset of claude --output-format json we care about.
type Result struct {
	Result       string  `json:"result"`
	Subtype      string  `json:"subtype"`
	SessionID    string  `json:"session_id"`
	TotalCostUSD float64 `json:"total_cost_usd"`
	NumTurns     int     `json:"num_turns"`
	IsError      bool    `json:"is_error"`
}

// RunOptions configures a headless claude -p invocation.
type RunOptions struct {
	Prompt       string
	AllowedTools []string
	MaxTurns     int
	Model        string // optional; defaults to claude's own default
	WorkDir      string // working directory for the subprocess
	OutputFile   string // path to write the raw JSON result
	Resume       string // session_id to resume (for repair runs, per spec §8.3)
}

// Run invokes claude -p headlessly and returns the parsed result.
// It also writes the raw JSON to opts.OutputFile if set.
func Run(opts RunOptions) (*Result, error) {
	args := []string{"-p", opts.Prompt}

	if len(opts.AllowedTools) > 0 {
		args = append(args, "--allowedTools", strings.Join(opts.AllowedTools, ","))
	}
	if opts.MaxTurns > 0 {
		args = append(args, "--max-turns", fmt.Sprintf("%d", opts.MaxTurns))
	}
	if opts.Model != "" {
		args = append(args, "--model", opts.Model)
	}
	if opts.Resume != "" {
		args = append(args, "--resume", opts.Resume)
	}
	args = append(args, "--output-format", "json")

	cmd := exec.Command("claude", args...)
	if opts.WorkDir != "" {
		cmd.Dir = opts.WorkDir
	}
	// Pass through the environment; the subprocess needs ANTHROPIC_API_KEY.
	cmd.Env = os.Environ()

	out, err := cmd.Output()
	if err != nil {
		// Claude exits non-zero on error_max_turns but stdout still contains
		// valid JSON with real work in the worktree — parse before giving up.
		if len(out) > 0 {
			var partial Result
			if jsonErr := json.Unmarshal(out, &partial); jsonErr == nil && partial.Subtype == "error_max_turns" {
				if opts.OutputFile != "" {
					_ = os.MkdirAll(filepath.Dir(opts.OutputFile), 0o755)
					_ = os.WriteFile(opts.OutputFile, out, 0o644)
				}
				return &partial, nil
			}
		}
		stderr := ""
		if ee, ok := err.(*exec.ExitError); ok {
			stderr = string(ee.Stderr)
		}
		return nil, fmt.Errorf("claude exited non-zero: %w\nstderr: %s\nstdout: %s", err, stderr, truncate(string(out), 1000))
	}

	if opts.OutputFile != "" {
		if err := os.MkdirAll(filepath.Dir(opts.OutputFile), 0o755); err != nil {
			return nil, fmt.Errorf("mkdir for output file: %w", err)
		}
		if err := os.WriteFile(opts.OutputFile, out, 0o644); err != nil {
			return nil, fmt.Errorf("write output file: %w", err)
		}
	}

	var result Result
	if err := json.Unmarshal(out, &result); err != nil {
		return nil, fmt.Errorf("parse claude JSON output: %w\nraw: %s", err, truncate(string(out), 500))
	}
	if result.IsError {
		return nil, fmt.Errorf("claude reported an error: %s", truncate(result.Result, 500))
	}
	return &result, nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
