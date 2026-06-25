package gate

import (
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Stages holds per-stage verdicts from run-gate.sh.
type Stages struct {
	Hygiene string `json:"hygiene"` // "ok" | "fail"
	Build   string `json:"build"`
	Lint    string `json:"lint"`
	Unit    string `json:"unit"`
	E2E     string `json:"e2e"` // "ok" | "fail" | "skip"
}

// Result is the output of gate/run-gate.sh, read from gate.json.
type Result struct {
	Passed bool    `json:"passed"`
	Score  float64 `json:"score"`
	Stages Stages  `json:"stages"`
}

// execCommand is the exec.Command function used to invoke the gate script and docker.
// Replaced in tests to avoid calling real processes.
var execCommand = exec.Command

// ocisHealthURL is the URL polled when waiting for oCIS to start.
// Replaced in tests to point to an httptest server.
var ocisHealthURL = "https://host.docker.internal:9200"

// Run executes gate/run-gate.sh and returns the parsed result.
// scriptPath is the absolute path to gate/run-gate.sh.
// outputDir is where gate.json and gate.log will be written.
// specBulletCount is the minimum number of expect() calls required in acceptance.spec.ts.
// mainCheckout is the web-extensions checkout running oCIS via docker-compose; when
// empty the e2e stage is skipped.
// logID is the candidate ID used to prefix log output (e.g. "web-app-ai-doc-summary").
func Run(scriptPath, worktreePath, extID, outputDir string, specBulletCount int, mainCheckout, logID string) (*Result, error) {
	prefix := logPrefix(logID)
	if err := os.MkdirAll(outputDir, 0o755); err != nil {
		return nil, fmt.Errorf("mkdir gate output dir: %w", err)
	}

	args := []string{worktreePath, extID, outputDir, fmt.Sprintf("%d", specBulletCount), mainCheckout}
	cmd := execCommand(scriptPath, args...)

	// Create gate.log so the script can open it with O_APPEND via "tee -a $LOG".
	// Do NOT redirect cmd.Stdout/Stderr here — the script owns all writes to its
	// LOG variable via tee; pointing cmd.Stdout at the same file would cause every
	// line to be written twice, corrupting the cursor-position accounting in renderANSI.
	logPath := filepath.Join(outputDir, "gate.log")
	if err := os.WriteFile(logPath, nil, 0o644); err != nil {
		return nil, fmt.Errorf("create gate.log: %w", err)
	}

	fmt.Printf("\n%sgate: running %s…\n", prefix, scriptPath)
	_ = cmd.Run() // exit code is encoded in gate.json; don't treat non-zero as a Go error

	// Read gate.json written by the script.
	jsonPath := filepath.Join(outputDir, "gate.json")
	data, err := os.ReadFile(jsonPath)
	if err != nil {
		return nil, fmt.Errorf("read gate.json: %w", err)
	}
	var result Result
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, fmt.Errorf("parse gate.json: %w", err)
	}
	return &result, nil
}

// EnsureOCIS checks that the oCIS container is running in mainCheckout.
// If it is not running, it starts it via docker compose up -d ocis and
// waits up to 60 s for the health endpoint to become ready.
// logID is the candidate ID used to prefix log output (e.g. "web-app-ai-doc-summary").
func EnsureOCIS(mainCheckout, logID string) error {
	prefix := logPrefix(logID)
	checkCmd := execCommand("docker", "compose", "ps", "-q", "ocis")
	checkCmd.Dir = mainCheckout
	out, _ := checkCmd.Output()
	if strings.TrimSpace(string(out)) != "" {
		return nil // already running
	}

	fmt.Printf("%sgate: oCIS not running — starting via docker compose up -d…\n", prefix)
	upCmd := execCommand("docker", "compose", "up", "-d")
	upCmd.Dir = mainCheckout
	upCmd.Stdout = os.Stdout
	upCmd.Stderr = os.Stderr
	if err := upCmd.Run(); err != nil {
		return fmt.Errorf("docker compose up -d: %w", err)
	}

	client := &http.Client{
		Timeout: 2 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec // self-signed dev cert
		},
	}
	for i := 0; i < 30; i++ {
		resp, err := client.Get(ocisHealthURL)
		if err == nil {
			resp.Body.Close()
			fmt.Printf("%sgate: oCIS is up\n", prefix)
			return nil
		}
		time.Sleep(2 * time.Second)
	}
	return fmt.Errorf("oCIS did not become reachable within 60s at %s", ocisHealthURL)
}

// ReadLog returns the contents of gate.log from the output directory,
// with ANSI cursor-movement sequences rendered to their visual result.
func ReadLog(outputDir string) (string, error) {
	data, err := os.ReadFile(filepath.Join(outputDir, "gate.log"))
	if os.IsNotExist(err) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("read gate.log: %w", err)
	}
	return renderANSI(string(data)), nil
}

// logPrefix formats logID as "[id] " for use in log output, or "" if logID is empty.
func logPrefix(logID string) string {
	if logID == "" {
		return ""
	}
	return "[" + logID + "] "
}

// renderANSI processes ANSI cursor-movement escape sequences in s and returns
// the final rendered text — what a terminal would display. It handles the
// sequences Playwright emits for in-place progress updates:
//
//   - ESC[nA  cursor up n lines
//   - ESC[2K  erase entire current line
//   - ESC[0K  erase from cursor to end of line
//   - ESC[...m  SGR (colors/style) — stripped
//   - \r  carriage return
func renderANSI(s string) string {
	lines := [][]rune{{}}
	row, col := 0, 0

	expand := func() {
		for len(lines) <= row {
			lines = append(lines, []rune{})
		}
	}

	parseN := func(param string, def int) int {
		if param == "" {
			return def
		}
		v, err := strconv.Atoi(param)
		if err != nil || v == 0 {
			return def
		}
		return v
	}

	runes := []rune(s)
	for i := 0; i < len(runes); {
		ch := runes[i]

		if ch == '\x1b' && i+1 < len(runes) && runes[i+1] == '[' {
			// Scan to end of CSI sequence: ESC [ <params> <final>
			j := i + 2
			for j < len(runes) && (runes[j] < 0x40 || runes[j] > 0x7e) {
				j++
			}
			if j < len(runes) {
				params := string(runes[i+2 : j])
				switch runes[j] {
				case 'A': // cursor up
					row -= parseN(params, 1)
					if row < 0 {
						row = 0
					}
				case 'K': // erase line (cursor does not move)
					expand()
					if params == "2" {
						lines[row] = []rune{}
					} else { // 0K or default: erase to end of line
						if col < len(lines[row]) {
							lines[row] = lines[row][:col]
						}
					}
				// 'm' (SGR colors), 'G' (cursor column), 'J' (erase display): discard
				}
				i = j + 1
				continue
			}
		}

		if ch == '\r' {
			col = 0
			i++
			continue
		}
		if ch == '\n' {
			row++
			col = 0
			expand()
			i++
			continue
		}

		expand()
		for len(lines[row]) <= col {
			lines[row] = append(lines[row], ' ')
		}
		lines[row][col] = ch
		col++
		i++
	}

	var sb strings.Builder
	for i, line := range lines {
		if i > 0 {
			sb.WriteByte('\n')
		}
		sb.WriteString(strings.TrimRight(string(line), " "))
	}
	return sb.String()
}
