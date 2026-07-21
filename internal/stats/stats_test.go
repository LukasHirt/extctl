package stats_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/LukasHirt/extctl/internal/build"
	"github.com/LukasHirt/extctl/internal/config"
	"github.com/LukasHirt/extctl/internal/state"
	"github.com/LukasHirt/extctl/internal/stats"
)

// daysAgo returns the date n days before now (UTC, matching minCfg's
// Timezone: "UTC"), formatted the same way stats.Compute formats dates.
// Relative to time.Now() rather than a hardcoded date so these tests don't
// silently start failing once the calendar moves past a fixed cutoff.
func daysAgo(n int) string {
	return time.Now().UTC().AddDate(0, 0, -n).Format("2006-01-02")
}

func minCfg(runsDir string) *config.Config {
	return &config.Config{
		Timezone: "UTC",
		RunsDir:  runsDir,
		Claude:   config.Claude{BudgetUSDPerBuild: 8.0},
		Decay:    config.Decay{MaxAppearances: 3},
	}
}

func writeSlate(t *testing.T, runsDir string, s *state.Slate) {
	t.Helper()
	if err := state.Save(runsDir, s); err != nil {
		t.Fatalf("save slate: %v", err)
	}
}

func writeBuildState(t *testing.T, runsDir, date, id string, s build.State) {
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

func TestCompute_DaysValidation(t *testing.T) {
	dir := t.TempDir()
	cfg := minCfg(dir)
	for _, bad := range []int{0, -1, -100} {
		_, err := stats.Compute(cfg, bad)
		if err == nil {
			t.Errorf("days=%d: expected error, got nil", bad)
		}
	}
}

func TestCompute_EmptyRunsDir(t *testing.T) {
	dir := t.TempDir()
	cfg := minCfg(dir)
	r, err := stats.Compute(cfg, 30)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if r.Today.HasSlate {
		t.Error("want HasSlate=false for empty dir")
	}
	if r.Health.SlatesRun != 0 {
		t.Errorf("want SlatesRun=0, got %d", r.Health.SlatesRun)
	}
	if r.Cost.TotalBuilds != 0 {
		t.Errorf("want TotalBuilds=0, got %d", r.Cost.TotalBuilds)
	}
}

func TestCompute_TodaySection(t *testing.T) {
	dir := t.TempDir()
	cfg := minCfg(dir)

	// Write a slate 10 days ago — well within the 30-day window below.
	writeSlate(t, dir, &state.Slate{
		Date: daysAgo(10),
		Candidates: []state.Candidate{
			{ID: "ext-a", Status: state.StatusPicked, Origin: "generated"},
			{ID: "ext-b", Status: state.StatusNeedsApproval, Origin: "carryover"},
		},
	})

	r, err := stats.Compute(cfg, 30)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if r.Health.SlatesRun < 1 {
		t.Errorf("want SlatesRun >= 1, got %d", r.Health.SlatesRun)
	}
	if r.Health.Offered < 2 {
		t.Errorf("want Offered >= 2, got %d", r.Health.Offered)
	}
	if r.Health.Picked < 1 {
		t.Errorf("want Picked >= 1, got %d", r.Health.Picked)
	}
}

func TestCompute_HealthSuccessful(t *testing.T) {
	dir := t.TempDir()
	cfg := minCfg(dir)

	writeBuildState(t, dir, daysAgo(10), "ext-done", build.State{
		ID: "ext-done", Date: daysAgo(10), Phase: build.PhaseDone, CostUSD: 5.0, Turns: 80,
	})
	writeBuildState(t, dir, daysAgo(10), "ext-gated", build.State{
		ID: "ext-gated", Date: daysAgo(10), Phase: build.PhaseGated, CostUSD: 3.0, Turns: 50,
	})
	writeBuildState(t, dir, daysAgo(10), "ext-blocked", build.State{
		ID: "ext-blocked", Date: daysAgo(10), Phase: build.PhaseBlocked, CostUSD: 6.0, Turns: 100, Attempts: 3,
	})

	r, err := stats.Compute(cfg, 30)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if r.Health.Successful != 2 {
		t.Errorf("want Successful=2 (done+gated), got %d", r.Health.Successful)
	}
}

func TestCompute_InFlight(t *testing.T) {
	dir := t.TempDir()
	cfg := minCfg(dir)

	writeBuildState(t, dir, daysAgo(10), "ext-building", build.State{
		ID: "ext-building", Date: daysAgo(10), Phase: build.PhaseBuilding,
		CurrentStage: 2, TotalStages: 5, CostUSD: 4.0,
	})
	writeBuildState(t, dir, daysAgo(10), "ext-done", build.State{
		ID: "ext-done", Date: daysAgo(10), Phase: build.PhaseDone, CostUSD: 5.0,
	})
	writeBuildState(t, dir, daysAgo(10), "ext-blocked", build.State{
		ID: "ext-blocked", Date: daysAgo(10), Phase: build.PhaseBlocked, CostUSD: 2.0,
	})

	r, err := stats.Compute(cfg, 30)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// done is excluded; building and blocked are in-flight
	if len(r.Today.InFlight) != 2 {
		t.Errorf("want 2 in-flight builds, got %d", len(r.Today.InFlight))
	}
}

func TestCompute_CostAggregation(t *testing.T) {
	dir := t.TempDir()
	cfg := minCfg(dir)

	writeBuildState(t, dir, daysAgo(10), "cheap", build.State{
		ID: "cheap", Date: daysAgo(10), Phase: build.PhaseDone, CostUSD: 2.0, Turns: 30,
	})
	writeBuildState(t, dir, daysAgo(10), "expensive", build.State{
		ID: "expensive", Date: daysAgo(10), Phase: build.PhaseDone, CostUSD: 7.0, Turns: 120,
	})

	r, err := stats.Compute(cfg, 30)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if r.Cost.TotalBuilds != 2 {
		t.Errorf("want TotalBuilds=2, got %d", r.Cost.TotalBuilds)
	}
	if r.Cost.TotalUSD != 9.0 {
		t.Errorf("want TotalUSD=9.0, got %.2f", r.Cost.TotalUSD)
	}
	if r.Cost.MostExpensiveID != "expensive" {
		t.Errorf("want MostExpensiveID=expensive, got %s", r.Cost.MostExpensiveID)
	}
	if r.Cost.AvgPerBuildUSD != 4.5 {
		t.Errorf("want AvgPerBuildUSD=4.5, got %.2f", r.Cost.AvgPerBuildUSD)
	}
}

func TestCompute_DaysWindowExcludesOld(t *testing.T) {
	dir := t.TempDir()
	cfg := minCfg(dir)

	// Old build, outside the 30-day window below.
	writeBuildState(t, dir, daysAgo(60), "old-build", build.State{
		ID: "old-build", Date: daysAgo(60), Phase: build.PhaseDone, CostUSD: 10.0,
	})
	// Recent build (10 days ago)
	writeBuildState(t, dir, daysAgo(10), "new-build", build.State{
		ID: "new-build", Date: daysAgo(10), Phase: build.PhaseDone, CostUSD: 3.0,
	})

	r, err := stats.Compute(cfg, 30)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if r.Cost.TotalBuilds != 1 {
		t.Errorf("want TotalBuilds=1 (old excluded), got %d", r.Cost.TotalBuilds)
	}
	if r.Cost.TotalUSD != 3.0 {
		t.Errorf("want TotalUSD=3.0, got %.2f", r.Cost.TotalUSD)
	}
}

func TestCompute_BudgetConfig(t *testing.T) {
	dir := t.TempDir()
	cfg := minCfg(dir)
	cfg.Claude.BudgetUSDPerBuild = 8.0

	r, err := stats.Compute(cfg, 30)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if r.Cost.BudgetPerBuildUSD != 8.0 {
		t.Errorf("want BudgetPerBuildUSD=8.0, got %.2f", r.Cost.BudgetPerBuildUSD)
	}
}
