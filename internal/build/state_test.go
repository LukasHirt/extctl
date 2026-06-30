package build

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestSaveStateAndLoadState(t *testing.T) {
	dir := t.TempDir()
	s := &State{
		ID:           "my-ext",
		Date:         "2026-01-15",
		JiraKey:      "OSPO-42",
		Branch:       "ext/my-ext",
		Phase:        PhaseDone,
		Attempts:     2,
		SessionID:    "sess-abc",
		CostUSD:      3.14,
		Turns:        12,
		ScaffoldDone: true,
		CurrentStage: 3,
		TotalStages:  3,
		Gate: &GateResult{
			Passed: true,
			Score:  1.0,
			Stages: GateStages{
				Hygiene: "ok",
				Build:   "ok",
				Lint:    "ok",
				Unit:    "ok",
				E2E:     "skip",
			},
		},
		PR: &PRResult{
			Number: 42,
			URL:    "https://github.com/org/repo/pull/42",
			Ready:  true,
		},
	}

	if err := SaveState(dir, s); err != nil {
		t.Fatalf("SaveState: %v", err)
	}

	// Verify file path.
	p := filepath.Join(dir, "2026-01-15", "my-ext", "state.json")
	if _, err := os.Stat(p); err != nil {
		t.Fatalf("state.json not found at %s: %v", p, err)
	}

	loaded, err := LoadState(dir, "2026-01-15", "my-ext")
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	if loaded == nil {
		t.Fatal("LoadState returned nil")
	}

	if loaded.ID != s.ID {
		t.Errorf("ID = %q, want %q", loaded.ID, s.ID)
	}
	if loaded.Phase != s.Phase {
		t.Errorf("Phase = %q, want %q", loaded.Phase, s.Phase)
	}
	if loaded.SessionID != s.SessionID {
		t.Errorf("SessionID = %q, want %q", loaded.SessionID, s.SessionID)
	}
	if loaded.CostUSD != s.CostUSD {
		t.Errorf("CostUSD = %f, want %f", loaded.CostUSD, s.CostUSD)
	}
	if loaded.Gate == nil {
		t.Fatal("Gate is nil after roundtrip")
	}
	if !loaded.Gate.Passed {
		t.Error("Gate.Passed = false, want true")
	}
	if loaded.Gate.Stages.E2E != "skip" {
		t.Errorf("Gate.Stages.E2E = %q, want %q", loaded.Gate.Stages.E2E, "skip")
	}
	if loaded.PR == nil {
		t.Fatal("PR is nil after roundtrip")
	}
	if loaded.PR.Number != 42 {
		t.Errorf("PR.Number = %d, want 42", loaded.PR.Number)
	}
}

func TestLoadState_NotExist(t *testing.T) {
	dir := t.TempDir()
	s, err := LoadState(dir, "2026-01-01", "nonexistent")
	if err != nil {
		t.Fatalf("expected nil error, got: %v", err)
	}
	if s != nil {
		t.Errorf("expected nil state, got: %+v", s)
	}
}

func TestFindState(t *testing.T) {
	t.Run("found under one date", func(t *testing.T) {
		dir := t.TempDir()
		s := &State{ID: "my-ext", Date: "2026-01-15", Phase: PhaseBuilding}
		if err := SaveState(dir, s); err != nil {
			t.Fatal(err)
		}
		found, err := FindState(dir, "my-ext")
		if err != nil {
			t.Fatalf("FindState: %v", err)
		}
		if found == nil {
			t.Fatal("FindState returned nil")
		}
		if found.Phase != PhaseBuilding {
			t.Errorf("Phase = %q, want %q", found.Phase, PhaseBuilding)
		}
	})

	t.Run("not found returns nil nil", func(t *testing.T) {
		dir := t.TempDir()
		found, err := FindState(dir, "missing-ext")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if found != nil {
			t.Errorf("expected nil, got: %+v", found)
		}
	})

	t.Run("found under second date", func(t *testing.T) {
		dir := t.TempDir()
		// First date has a different extension.
		if err := SaveState(dir, &State{ID: "other", Date: "2026-01-14", Phase: PhaseDone}); err != nil {
			t.Fatal(err)
		}
		// Second date has the extension we're looking for.
		if err := SaveState(dir, &State{ID: "target", Date: "2026-01-15", Phase: PhaseGated}); err != nil {
			t.Fatal(err)
		}
		found, err := FindState(dir, "target")
		if err != nil {
			t.Fatalf("FindState: %v", err)
		}
		if found == nil {
			t.Fatal("FindState returned nil")
		}
		if found.Phase != PhaseGated {
			t.Errorf("Phase = %q, want %q", found.Phase, PhaseGated)
		}
	})

	t.Run("non-existent runs dir returns nil nil", func(t *testing.T) {
		found, err := FindState("/tmp/extctl-test-nonexistent-99999", "any")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if found != nil {
			t.Errorf("expected nil, got: %+v", found)
		}
	})
}

func TestSaveState_NoTmpFileLeft(t *testing.T) {
	dir := t.TempDir()
	s := &State{ID: "ext", Date: "2026-03-01", Phase: PhasePlanning}
	if err := SaveState(dir, s); err != nil {
		t.Fatal(err)
	}
	stateDir := filepath.Join(dir, "2026-03-01", "ext")
	entries, err := os.ReadDir(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.Name() != "state.json" {
			t.Errorf("unexpected file left: %s", e.Name())
		}
	}
}

func writeRawState(t *testing.T, runsDir, date, id string, s State) {
	t.Helper()
	dir := filepath.Join(runsDir, date, id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	data, err := json.Marshal(s)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "state.json"), data, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
}

func TestLoadAllStates(t *testing.T) {
	t.Run("empty dir", func(t *testing.T) {
		dir := t.TempDir()
		states, err := LoadAllStates(dir, time.Time{})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(states) != 0 {
			t.Errorf("want 0 states, got %d", len(states))
		}
	})

	t.Run("nonexistent dir returns nil", func(t *testing.T) {
		states, err := LoadAllStates("/nonexistent/path/xyz-extctl-test", time.Time{})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if states != nil {
			t.Errorf("want nil, got %v", states)
		}
	})

	t.Run("non-date subdirectory skipped", func(t *testing.T) {
		dir := t.TempDir()
		subDir := filepath.Join(dir, "not-a-date", "some-id")
		if err := os.MkdirAll(subDir, 0o755); err != nil {
			t.Fatal(err)
		}
		_ = os.WriteFile(filepath.Join(subDir, "state.json"), []byte(`{"id":"x"}`), 0o644)
		states, err := LoadAllStates(dir, time.Time{})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(states) != 0 {
			t.Errorf("want 0 states (non-date dir skipped), got %d", len(states))
		}
	})

	t.Run("corrupt state.json skipped", func(t *testing.T) {
		dir := t.TempDir()
		stateDir := filepath.Join(dir, "2026-06-01", "my-ext")
		if err := os.MkdirAll(stateDir, 0o755); err != nil {
			t.Fatal(err)
		}
		_ = os.WriteFile(filepath.Join(stateDir, "state.json"), []byte(`{invalid json`), 0o644)
		states, err := LoadAllStates(dir, time.Time{})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(states) != 0 {
			t.Errorf("want 0 states (corrupt skipped), got %d", len(states))
		}
	})

	t.Run("states across multiple date dirs", func(t *testing.T) {
		dir := t.TempDir()
		writeRawState(t, dir, "2026-06-01", "ext-a", State{ID: "ext-a", Date: "2026-06-01"})
		writeRawState(t, dir, "2026-06-10", "ext-b", State{ID: "ext-b", Date: "2026-06-10"})
		states, err := LoadAllStates(dir, time.Time{})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(states) != 2 {
			t.Errorf("want 2 states, got %d", len(states))
		}
	})

	t.Run("since filter excludes older dates", func(t *testing.T) {
		dir := t.TempDir()
		writeRawState(t, dir, "2026-05-01", "ext-old", State{ID: "ext-old", Date: "2026-05-01"})
		writeRawState(t, dir, "2026-06-15", "ext-new", State{ID: "ext-new", Date: "2026-06-15"})
		since, _ := time.Parse("2006-01-02", "2026-06-01")
		states, err := LoadAllStates(dir, since)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(states) != 1 {
			t.Errorf("want 1 state, got %d", len(states))
		}
		if len(states) > 0 && states[0].ID != "ext-new" {
			t.Errorf("want ext-new, got %s", states[0].ID)
		}
	})

	t.Run("since boundary is inclusive", func(t *testing.T) {
		dir := t.TempDir()
		writeRawState(t, dir, "2026-06-01", "ext-boundary", State{ID: "ext-boundary"})
		since, _ := time.Parse("2006-01-02", "2026-06-01")
		states, err := LoadAllStates(dir, since)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(states) != 1 {
			t.Errorf("want 1 state (boundary inclusive), got %d", len(states))
		}
	})
}
