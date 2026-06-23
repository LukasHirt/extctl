package claude

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Result is the subset of the stream-json "result" event we care about.
type Result struct {
	Result       string  `json:"result"`
	Subtype      string  `json:"subtype"`
	SessionID    string  `json:"session_id"`
	TotalCostUSD float64 `json:"total_cost_usd"`
	NumTurns     int     `json:"num_turns"`
	IsError      bool    `json:"is_error"`
	// FullText is all assistant text turns concatenated with the final result.
	// Use this for parsing structured output that Claude may emit mid-conversation.
	FullText string `json:"-"`
}

// RunOptions configures a headless claude -p invocation.
type RunOptions struct {
	Prompt       string
	AllowedTools []string
	Model        string // optional; defaults to claude's own default
	WorkDir      string // working directory for the subprocess
	OutputFile   string // path to write the raw JSONL stream
	Resume       string // session_id to resume (for repair runs, per spec §8.3)
}

// streamEvent is a single line of --output-format stream-json output.
type streamEvent struct {
	Type    string         `json:"type"`
	Message *streamMessage `json:"message,omitempty"`
	// Fields present only on type=="result"
	Result       string  `json:"result"`
	Subtype      string  `json:"subtype"`
	SessionID    string  `json:"session_id"`
	TotalCostUSD float64 `json:"total_cost_usd"`
	NumTurns     int     `json:"num_turns"`
	IsError      bool    `json:"is_error"`
}

type streamMessage struct {
	Content []contentBlock `json:"content"`
}

type contentBlock struct {
	Type  string          `json:"type"`
	Text  string          `json:"text,omitempty"`
	Name  string          `json:"name,omitempty"`
	Input json.RawMessage `json:"input,omitempty"`
}

// Run invokes claude -p headlessly with stream-json output, printing live
// progress to stdout as Claude works. The raw JSONL is written to
// opts.OutputFile if set. Returns the parsed final result.
func Run(opts RunOptions) (*Result, error) {
	args := []string{"-p"}
	if len(opts.AllowedTools) > 0 {
		args = append(args, "--allowedTools", strings.Join(opts.AllowedTools, ","))
	}
	if opts.Model != "" {
		args = append(args, "--model", opts.Model)
	}
	if opts.Resume != "" {
		args = append(args, "--resume", opts.Resume)
	}
	args = append(args, "--verbose", "--output-format", "stream-json")

	cmd := exec.Command("claude", args...)
	if opts.WorkDir != "" {
		cmd.Dir = opts.WorkDir
	}
	cmd.Env = os.Environ()
	cmd.Stdin = strings.NewReader(opts.Prompt)
	cmd.Stderr = os.Stderr

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("stdout pipe: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start claude: %w", err)
	}

	var (
		rawLines      [][]byte
		result        *Result
		scanErr       error
		assistantText strings.Builder
	)

	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 4*1024*1024), 4*1024*1024)
	for scanner.Scan() {
		raw := make([]byte, len(scanner.Bytes()))
		copy(raw, scanner.Bytes())
		rawLines = append(rawLines, raw)

		var ev streamEvent
		if err := json.Unmarshal(raw, &ev); err != nil {
			continue
		}
		switch ev.Type {
		case "assistant":
			if ev.Message != nil {
				printMessage(ev.Message)
				for _, block := range ev.Message.Content {
					if block.Type == "text" && block.Text != "" {
						assistantText.WriteString(block.Text)
						assistantText.WriteString("\n")
					}
				}
			}
		case "result":
			result = &Result{
				Result:       ev.Result,
				Subtype:      ev.Subtype,
				SessionID:    ev.SessionID,
				TotalCostUSD: ev.TotalCostUSD,
				NumTurns:     ev.NumTurns,
				IsError:      ev.IsError,
			}
		}
	}
	if err := scanner.Err(); err != nil {
		scanErr = fmt.Errorf("reading claude stdout: %w", err)
	}

	if err := cmd.Wait(); err != nil {
		if scanErr != nil {
			return nil, scanErr
		}
		return nil, fmt.Errorf("claude exited non-zero: %w", err)
	}
	if scanErr != nil {
		return nil, scanErr
	}

	if opts.OutputFile != "" && len(rawLines) > 0 {
		if err := os.MkdirAll(filepath.Dir(opts.OutputFile), 0o755); err != nil {
			return nil, fmt.Errorf("mkdir for output file: %w", err)
		}
		var buf []byte
		for _, l := range rawLines {
			buf = append(buf, l...)
			buf = append(buf, '\n')
		}
		if err := os.WriteFile(opts.OutputFile, buf, 0o644); err != nil {
			return nil, fmt.Errorf("write output file: %w", err)
		}
	}

	if result == nil {
		return nil, fmt.Errorf("claude stream ended without a result event")
	}
	if result.IsError {
		return nil, fmt.Errorf("claude reported an error: %s", truncate(result.Result, 500))
	}
	if assistantText.Len() > 0 {
		result.FullText = assistantText.String()
	} else {
		result.FullText = result.Result
	}
	return result, nil
}

// LoadResult reads a JSONL file written by a previous Run and returns the Result.
// Used by --from-file to replay a saved session without re-running Claude.
func LoadResult(path string) (*Result, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	var (
		result        *Result
		assistantText strings.Builder
	)
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var ev streamEvent
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			continue
		}
		switch ev.Type {
		case "assistant":
			if ev.Message != nil {
				for _, block := range ev.Message.Content {
					if block.Type == "text" && block.Text != "" {
						assistantText.WriteString(block.Text)
						assistantText.WriteString("\n")
					}
				}
			}
		case "result":
			result = &Result{
				Result:       ev.Result,
				Subtype:      ev.Subtype,
				SessionID:    ev.SessionID,
				TotalCostUSD: ev.TotalCostUSD,
				NumTurns:     ev.NumTurns,
				IsError:      ev.IsError,
			}
		}
	}
	if result == nil {
		return nil, fmt.Errorf("no result event found in %s", path)
	}
	if assistantText.Len() > 0 {
		result.FullText = assistantText.String()
	} else {
		result.FullText = result.Result
	}
	return result, nil
}

func printMessage(msg *streamMessage) {
	for _, block := range msg.Content {
		switch block.Type {
		case "text":
			if block.Text != "" {
				fmt.Print(block.Text)
			}
		case "tool_use":
			fmt.Printf("\n▶ %s\n", formatToolUse(block))
		}
	}
}

func formatToolUse(block contentBlock) string {
	if len(block.Input) == 0 {
		return block.Name
	}
	var input map[string]any
	if err := json.Unmarshal(block.Input, &input); err != nil {
		return block.Name
	}
	for _, key := range []string{"command", "file_path", "path", "pattern", "query", "old_string"} {
		if v, ok := input[key]; ok {
			s := fmt.Sprintf("%v", v)
			if len(s) > 80 {
				s = s[:77] + "…"
			}
			return fmt.Sprintf("%s(%s=%s)", block.Name, key, s)
		}
	}
	for k, v := range input {
		s := fmt.Sprintf("%v", v)
		if len(s) > 80 {
			s = s[:77] + "…"
		}
		return fmt.Sprintf("%s(%s=%s)", block.Name, k, s)
	}
	return block.Name
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
