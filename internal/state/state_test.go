package state_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/LukasHirt/extctl/internal/state"
)

// --- Pure function tests ---

func TestCarryovers(t *testing.T) {
	makeSlate := func(date string, candidates ...state.Candidate) *state.Slate {
		return &state.Slate{Date: date, Candidates: candidates}
	}
	cand := func(id string, status state.CandidateStatus, appearances int) state.Candidate {
		return state.Candidate{ID: id, Status: status, Appearances: appearances}
	}

	tests := []struct {
		name           string
		slates         []*state.Slate
		today          string
		maxAppearances int
		wantIDs        []string
	}{
		{
			name:           "empty slates",
			slates:         nil,
			today:          "2026-01-10",
			maxAppearances: 3,
			wantIDs:        nil,
		},
		{
			name:           "today's slate excluded",
			slates:         []*state.Slate{makeSlate("2026-01-10", cand("a", state.StatusNeedsApproval, 1))},
			today:          "2026-01-10",
			maxAppearances: 3,
			wantIDs:        nil,
		},
		{
			name:           "future slate excluded",
			slates:         []*state.Slate{makeSlate("2026-01-11", cand("a", state.StatusNeedsApproval, 1))},
			today:          "2026-01-10",
			maxAppearances: 3,
			wantIDs:        nil,
		},
		{
			name:           "past eligible candidate",
			slates:         []*state.Slate{makeSlate("2026-01-09", cand("a", state.StatusNeedsApproval, 1))},
			today:          "2026-01-10",
			maxAppearances: 3,
			wantIDs:        []string{"a"},
		},
		{
			name:           "at max appearances excluded",
			slates:         []*state.Slate{makeSlate("2026-01-09", cand("a", state.StatusNeedsApproval, 3))},
			today:          "2026-01-10",
			maxAppearances: 3,
			wantIDs:        nil,
		},
		{
			name:           "below max included",
			slates:         []*state.Slate{makeSlate("2026-01-09", cand("a", state.StatusNeedsApproval, 2))},
			today:          "2026-01-10",
			maxAppearances: 3,
			wantIDs:        []string{"a"},
		},
		{
			name: "declined status excluded",
			slates: []*state.Slate{
				makeSlate("2026-01-09", cand("a", state.StatusDeclined, 1)),
			},
			today:          "2026-01-10",
			maxAppearances: 3,
			wantIDs:        nil,
		},
		{
			name: "picked status excluded",
			slates: []*state.Slate{
				makeSlate("2026-01-09", cand("a", state.StatusPicked, 1)),
			},
			today:          "2026-01-10",
			maxAppearances: 3,
			wantIDs:        nil,
		},
		{
			name: "duplicate IDs across slates deduplicated",
			slates: []*state.Slate{
				makeSlate("2026-01-08", cand("a", state.StatusNeedsApproval, 1)),
				makeSlate("2026-01-09", cand("a", state.StatusNeedsApproval, 2)),
			},
			today:          "2026-01-10",
			maxAppearances: 3,
			wantIDs:        []string{"a"},
		},
		{
			name: "oldest-first ordering",
			slates: []*state.Slate{
				makeSlate("2026-01-09", cand("b", state.StatusNeedsApproval, 1)),
				makeSlate("2026-01-08", cand("a", state.StatusNeedsApproval, 1)),
			},
			today:          "2026-01-10",
			maxAppearances: 3,
			wantIDs:        []string{"b", "a"}, // order depends on slice order (oldest-first from caller)
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := state.Carryovers(tt.slates, tt.today, tt.maxAppearances)
			if len(got) != len(tt.wantIDs) {
				t.Errorf("got %d carryovers, want %d: %v", len(got), len(tt.wantIDs), ids(got))
				return
			}
			for i, wantID := range tt.wantIDs {
				if got[i].ID != wantID {
					t.Errorf("carryover[%d].ID = %q, want %q", i, got[i].ID, wantID)
				}
			}
		})
	}
}

func ids(cs []state.Candidate) []string {
	out := make([]string, len(cs))
	for i, c := range cs {
		out[i] = c.ID
	}
	return out
}

func TestDeliveredIDs(t *testing.T) {
	makeSlate := func(candidates ...state.Candidate) *state.Slate {
		return &state.Slate{Date: "2026-01-01", Candidates: candidates}
	}
	cand := func(id string, status state.CandidateStatus) state.Candidate {
		return state.Candidate{ID: id, Status: status}
	}

	tests := []struct {
		name          string
		slates        []*state.Slate
		deliveredYAML map[string]bool
		wantIDs       []string
		absentIDs     []string
	}{
		{
			name:          "empty inputs",
			slates:        nil,
			deliveredYAML: nil,
			wantIDs:       nil,
		},
		{
			name:          "picked from slate",
			slates:        []*state.Slate{makeSlate(cand("a", state.StatusPicked))},
			deliveredYAML: nil,
			wantIDs:       []string{"a"},
		},
		{
			name:          "rejected from slate",
			slates:        []*state.Slate{makeSlate(cand("b", state.StatusRejected))},
			deliveredYAML: nil,
			wantIDs:       []string{"b"},
		},
		{
			name:          "needs_approval not included",
			slates:        []*state.Slate{makeSlate(cand("c", state.StatusNeedsApproval))},
			deliveredYAML: nil,
			absentIDs:     []string{"c"},
		},
		{
			name:          "yaml map merged",
			slates:        nil,
			deliveredYAML: map[string]bool{"yaml-id": true},
			wantIDs:       []string{"yaml-id"},
		},
		{
			name:          "both sources merged",
			slates:        []*state.Slate{makeSlate(cand("slate-id", state.StatusPicked))},
			deliveredYAML: map[string]bool{"yaml-id": true},
			wantIDs:       []string{"slate-id", "yaml-id"},
		},
		{
			name: "deduplication across both sources",
			slates: []*state.Slate{makeSlate(
				cand("dup", state.StatusPicked),
			)},
			deliveredYAML: map[string]bool{"dup": true},
			wantIDs:       []string{"dup"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := state.DeliveredIDs(tt.slates, tt.deliveredYAML)
			for _, id := range tt.wantIDs {
				if !got[id] {
					t.Errorf("DeliveredIDs missing %q; got %v", id, got)
				}
			}
			for _, id := range tt.absentIDs {
				if got[id] {
					t.Errorf("DeliveredIDs should not contain %q; got %v", id, got)
				}
			}
		})
	}
}

// --- File I/O tests ---

func TestSaveAndLoad(t *testing.T) {
	dir := t.TempDir()
	s := &state.Slate{
		Date: "2026-01-15",
		Candidates: []state.Candidate{
			{ID: "a", Title: "A", Status: state.StatusNeedsApproval, Appearances: 1, Origin: "generated", Effort: "M"},
			{ID: "b", Title: "B", Status: state.StatusPicked, Appearances: 2, JiraKey: "OSPO-1"},
		},
		ReviewDone: true,
	}

	if err := state.Save(dir, s); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// Verify file exists at expected path.
	p := filepath.Join(dir, "2026-01-15", "slate.json")
	if _, err := os.Stat(p); err != nil {
		t.Fatalf("slate.json not found at %s: %v", p, err)
	}

	// Verify UpdatedAt was set.
	if s.UpdatedAt.IsZero() {
		t.Error("Save did not set UpdatedAt")
	}

	loaded, err := state.Load(dir, "2026-01-15")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loaded == nil {
		t.Fatal("Load returned nil")
	}

	if loaded.Date != s.Date {
		t.Errorf("Date = %q, want %q", loaded.Date, s.Date)
	}
	if loaded.ReviewDone != s.ReviewDone {
		t.Errorf("ReviewDone = %v, want %v", loaded.ReviewDone, s.ReviewDone)
	}
	if len(loaded.Candidates) != 2 {
		t.Fatalf("Candidates len = %d, want 2", len(loaded.Candidates))
	}
	if loaded.Candidates[0].ID != "a" {
		t.Errorf("Candidates[0].ID = %q, want %q", loaded.Candidates[0].ID, "a")
	}
	if loaded.Candidates[1].Status != state.StatusPicked {
		t.Errorf("Candidates[1].Status = %q, want %q", loaded.Candidates[1].Status, state.StatusPicked)
	}
}

func TestLoad_NotExist(t *testing.T) {
	dir := t.TempDir()
	s, err := state.Load(dir, "2099-01-01")
	if err != nil {
		t.Fatalf("expected nil error, got: %v", err)
	}
	if s != nil {
		t.Errorf("expected nil slate, got: %+v", s)
	}
}

func TestLoad_Corrupt(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "2026-01-01")
	if err := os.MkdirAll(p, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(p, "slate.json"), []byte("not json {{{"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := state.Load(dir, "2026-01-01")
	if err == nil {
		t.Error("expected error for corrupt JSON, got nil")
	}
}

func TestLoadAll(t *testing.T) {
	t.Run("empty dir", func(t *testing.T) {
		dir := t.TempDir()
		slates, err := state.LoadAll(dir)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(slates) != 0 {
			t.Errorf("got %d slates, want 0", len(slates))
		}
	})

	t.Run("non-existent dir returns nil nil", func(t *testing.T) {
		slates, err := state.LoadAll("/tmp/extctl-test-nonexistent-99999")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if slates != nil {
			t.Errorf("expected nil slates, got %v", slates)
		}
	})

	t.Run("three slates", func(t *testing.T) {
		dir := t.TempDir()
		for _, date := range []string{"2026-01-01", "2026-01-02", "2026-01-03"} {
			s := &state.Slate{Date: date, CreatedAt: time.Now()}
			if err := state.Save(dir, s); err != nil {
				t.Fatalf("Save %s: %v", date, err)
			}
		}
		slates, err := state.LoadAll(dir)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(slates) != 3 {
			t.Errorf("got %d slates, want 3", len(slates))
		}
	})

	t.Run("dir without slate.json skipped", func(t *testing.T) {
		dir := t.TempDir()
		// Create a date dir but no slate.json inside.
		if err := os.MkdirAll(filepath.Join(dir, "2026-01-01"), 0o755); err != nil {
			t.Fatal(err)
		}
		// Create a proper slate in a second date dir.
		if err := state.Save(dir, &state.Slate{Date: "2026-01-02"}); err != nil {
			t.Fatal(err)
		}
		slates, err := state.LoadAll(dir)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(slates) != 1 {
			t.Errorf("got %d slates, want 1 (empty dir should be skipped)", len(slates))
		}
	})
}

func TestLoadDelivered(t *testing.T) {
	t.Run("file not found returns empty map", func(t *testing.T) {
		dir := t.TempDir()
		got, err := state.LoadDelivered(filepath.Join(dir, "delivered.yaml"))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(got) != 0 {
			t.Errorf("expected empty map, got %v", got)
		}
	})

	t.Run("empty file returns empty map", func(t *testing.T) {
		dir := t.TempDir()
		p := filepath.Join(dir, "delivered.yaml")
		if err := os.WriteFile(p, []byte(""), 0o644); err != nil {
			t.Fatal(err)
		}
		got, err := state.LoadDelivered(p)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(got) != 0 {
			t.Errorf("expected empty map, got %v", got)
		}
	})

	t.Run("two entries parsed", func(t *testing.T) {
		dir := t.TempDir()
		p := filepath.Join(dir, "delivered.yaml")
		yaml := "- id: web-app-foo\n  title: Foo\n- id: web-app-bar\n  title: Bar\n"
		if err := os.WriteFile(p, []byte(yaml), 0o644); err != nil {
			t.Fatal(err)
		}
		got, err := state.LoadDelivered(p)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !got["web-app-foo"] {
			t.Error("missing web-app-foo")
		}
		if !got["web-app-bar"] {
			t.Error("missing web-app-bar")
		}
		if len(got) != 2 {
			t.Errorf("got %d entries, want 2", len(got))
		}
	})

	t.Run("malformed yaml returns error", func(t *testing.T) {
		dir := t.TempDir()
		p := filepath.Join(dir, "delivered.yaml")
		if err := os.WriteFile(p, []byte("key: [unclosed"), 0o644); err != nil {
			t.Fatal(err)
		}
		_, err := state.LoadDelivered(p)
		if err == nil {
			t.Error("expected error for malformed YAML, got nil")
		}
	})
}

func TestSave_NoTmpFileLeft(t *testing.T) {
	dir := t.TempDir()
	s := &state.Slate{Date: "2026-03-01"}
	if err := state.Save(dir, s); err != nil {
		t.Fatalf("Save: %v", err)
	}
	entries, err := os.ReadDir(filepath.Join(dir, "2026-03-01"))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.Name() != "slate.json" {
			t.Errorf("unexpected file left behind: %s", e.Name())
		}
	}
}
