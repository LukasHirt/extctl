package build

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// Phase tracks where a build is in its lifecycle.
type Phase string

const (
	PhasePlanning     Phase = "planning"
	PhasePlanReview   Phase = "plan_review"
	PhaseStaging      Phase = "staging"
	PhaseStagesReview Phase = "stages_review"
	PhaseBuilding     Phase = "building"
	PhaseGating       Phase = "gating"
	PhaseRepairing    Phase = "repairing"
	PhaseGated        Phase = "gated"   // gate passed
	PhasePublishing   Phase = "publishing"
	PhaseDone         Phase = "done"
	PhaseBlocked      Phase = "blocked" // gate failed after repair
)

// GateStages holds the per-stage result from gate/run-gate.sh.
type GateStages struct {
	Hygiene string `json:"hygiene"` // "ok" | "fail"
	Build   string `json:"build"`
	Lint    string `json:"lint"`
	Unit    string `json:"unit"`
	E2E     string `json:"e2e"` // "ok" | "fail" | "skip"
}

// GateResult is the output of the validation gate.
type GateResult struct {
	Passed bool       `json:"passed"`
	Score  float64    `json:"score"`
	Stages GateStages `json:"stages"`
}

// PRResult holds the published GitHub PR details.
type PRResult struct {
	Number int    `json:"number"`
	URL    string `json:"url"`
	Ready  bool   `json:"ready"`
}

// State is the per-build state written to runs/<date>/<id>/state.json.
// It is the idempotency key: the poll loop checks this before starting a build.
type State struct {
	ID           string      `json:"id"`
	Date         string      `json:"date"`
	JiraKey      string      `json:"jira"`
	Branch       string      `json:"branch"`
	Phase        Phase       `json:"phase"`
	Attempts     int         `json:"attempts"`
	SessionID    string      `json:"session_id,omitempty"`
	CostUSD      float64     `json:"cost_usd"`
	Turns        int         `json:"turns"`
	ScaffoldDone bool        `json:"scaffold_done,omitempty"`
	CurrentStage int         `json:"current_stage,omitempty"`
	TotalStages  int         `json:"total_stages,omitempty"`
	ErrorMsg     string      `json:"error,omitempty"`
	Gate         *GateResult `json:"gate,omitempty"`
	PR           *PRResult   `json:"pr,omitempty"`
	JiraTransitionedDone bool        `json:"jira_transitioned_done,omitempty"`
}

func buildDir(runsDir, date, id string) string {
	return filepath.Join(runsDir, date, id)
}

func statePath(runsDir, date, id string) string {
	return filepath.Join(buildDir(runsDir, date, id), "state.json")
}

// LoadState reads the build state for the given date+id.
// Returns nil, nil if the file does not exist.
func LoadState(runsDir, date, id string) (*State, error) {
	path := statePath(runsDir, date, id)
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("open build state %s: %w", path, err)
	}
	defer f.Close()
	var s State
	if err := json.NewDecoder(f).Decode(&s); err != nil {
		return nil, fmt.Errorf("decode build state %s: %w", path, err)
	}
	return &s, nil
}

// FindState scans all date subdirectories under runsDir and returns the first
// build state found for the given candidate ID. Returns nil, nil if none exists.
// Use this instead of LoadState when the candidate's build date may differ from
// the slate date it was found in (e.g. a picked candidate carried over to a
// newer slate).
func FindState(runsDir, id string) (*State, error) {
	entries, err := os.ReadDir(runsDir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read runs dir %s: %w", runsDir, err)
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		s, err := LoadState(runsDir, e.Name(), id)
		if err != nil {
			return nil, err
		}
		if s != nil {
			return s, nil
		}
	}
	return nil, nil
}

// SaveState atomically writes the build state to runs/<date>/<id>/state.json.
func SaveState(runsDir string, s *State) error {
	dir := buildDir(runsDir, s.Date, s.ID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", dir, err)
	}
	path := statePath(runsDir, s.Date, s.ID)
	f, err := os.CreateTemp(dir, "state-*.json.tmp")
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	tmpPath := f.Name()

	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	if err := enc.Encode(s); err != nil {
		f.Close()
		os.Remove(tmpPath)
		return fmt.Errorf("encode build state: %w", err)
	}
	if err := f.Close(); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("close temp file: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("rename build state: %w", err)
	}
	return nil
}
