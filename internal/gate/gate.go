package gate

import (
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
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

// Run executes gate/run-gate.sh and returns the parsed result.
// scriptPath is the absolute path to gate/run-gate.sh.
// outputDir is where gate.json and gate.log will be written.
// specBulletCount is the minimum number of expect() calls required in acceptance.spec.ts.
// mainCheckout is the web-extensions checkout running oCIS via docker-compose; when
// empty the e2e stage is skipped.
func Run(scriptPath, worktreePath, extID, outputDir string, specBulletCount int, mainCheckout string) (*Result, error) {
	if err := os.MkdirAll(outputDir, 0o755); err != nil {
		return nil, fmt.Errorf("mkdir gate output dir: %w", err)
	}

	args := []string{worktreePath, extID, outputDir, fmt.Sprintf("%d", specBulletCount), mainCheckout}
	cmd := exec.Command(scriptPath, args...)
	cmd.Stdout = nil
	cmd.Stderr = nil

	logPath := filepath.Join(outputDir, "gate.log")
	logFile, err := os.Create(logPath)
	if err != nil {
		return nil, fmt.Errorf("create gate.log: %w", err)
	}
	defer logFile.Close()
	cmd.Stdout = logFile
	cmd.Stderr = logFile

	fmt.Printf("gate: running %s…\n", scriptPath)
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
func EnsureOCIS(mainCheckout string) error {
	checkCmd := exec.Command("docker", "compose", "ps", "-q", "ocis")
	checkCmd.Dir = mainCheckout
	out, _ := checkCmd.Output()
	if strings.TrimSpace(string(out)) != "" {
		return nil // already running
	}

	fmt.Println("gate: oCIS not running — starting via docker compose up -d…")
	upCmd := exec.Command("docker", "compose", "up", "-d")
	upCmd.Dir = mainCheckout
	upCmd.Stdout = os.Stdout
	upCmd.Stderr = os.Stderr
	if err := upCmd.Run(); err != nil {
		return fmt.Errorf("docker compose up -d: %w", err)
	}

	const ocisURL = "https://host.docker.internal:9200"
	client := &http.Client{
		Timeout: 2 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec // self-signed dev cert
		},
	}
	for i := 0; i < 30; i++ {
		resp, err := client.Get(ocisURL)
		if err == nil {
			resp.Body.Close()
			fmt.Println("gate: oCIS is up")
			return nil
		}
		time.Sleep(2 * time.Second)
	}
	return fmt.Errorf("oCIS did not become reachable within 60s at %s", ocisURL)
}

// ReadLog returns the contents of gate.log from the output directory.
func ReadLog(outputDir string) (string, error) {
	data, err := os.ReadFile(filepath.Join(outputDir, "gate.log"))
	if os.IsNotExist(err) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("read gate.log: %w", err)
	}
	return string(data), nil
}
