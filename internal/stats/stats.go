package stats

import (
	"fmt"
	"time"

	"github.com/LukasHirt/extctl/internal/build"
	"github.com/LukasHirt/extctl/internal/config"
	"github.com/LukasHirt/extctl/internal/state"
)

// InFlightBuild summarises a build that is not yet done.
type InFlightBuild struct {
	ID           string
	Phase        build.Phase
	CurrentStage int
	TotalStages  int
	CostUSD      float64
}

// TodaySection holds the snapshot of today's slate and active builds.
type TodaySection struct {
	Date      string
	HasSlate  bool
	Total     int
	Fresh     int
	Carryover int
	ByStatus  map[state.CandidateStatus]int
	InFlight  []InFlightBuild
}

// HealthSection holds aggregate pipeline metrics over the requested window.
type HealthSection struct {
	Days       int
	SlatesRun  int
	Offered    int
	Picked     int
	Successful int // phase is done or gated (gate passed)
	AvgRepairs float64
	AvgTurns   float64
}

// CostSection holds spend metrics over the requested window.
type CostSection struct {
	TotalUSD          float64
	AvgPerBuildUSD    float64
	MostExpensiveID   string
	MostExpensiveUSD  float64
	BudgetPerBuildUSD float64
	TotalBuilds       int
}

// Report is the full stats output.
type Report struct {
	Today  TodaySection
	Health HealthSection
	Cost   CostSection
}

// Compute aggregates stats from slates and build states.
func Compute(cfg *config.Config, days int) (*Report, error) {
	if days <= 0 {
		return nil, fmt.Errorf("days must be a positive integer")
	}

	loc, err := time.LoadLocation(cfg.Timezone)
	if err != nil {
		loc = time.UTC
	}
	now := time.Now().In(loc)
	today := now.Format("2006-01-02")
	since := now.AddDate(0, 0, -days)
	sinceStr := since.Format("2006-01-02")

	allSlates, err := state.LoadAll(cfg.RunsDir)
	if err != nil {
		return nil, fmt.Errorf("load slates: %w", err)
	}

	// Load all build states without a date filter so in-flight detection is not
	// limited to the history window (a build started 31 days ago may still be active).
	allBuildStates, err := build.LoadAllStates(cfg.RunsDir, time.Time{})
	if err != nil {
		return nil, fmt.Errorf("load build states: %w", err)
	}

	r := &Report{}

	// TODAY section — populate from the latest slate matching today's date.
	for _, s := range allSlates {
		if s.Date != today {
			continue
		}
		r.Today.HasSlate = true
		r.Today.Date = today
		r.Today.Total = len(s.Candidates)
		r.Today.ByStatus = make(map[state.CandidateStatus]int, 6)
		for _, c := range s.Candidates {
			r.Today.ByStatus[c.Status]++
			if c.Origin == "carryover" {
				r.Today.Carryover++
			} else {
				r.Today.Fresh++
			}
		}
		break
	}
	if !r.Today.HasSlate {
		r.Today.Date = today
	}

	// In-flight: all build states that are not done.
	for _, bs := range allBuildStates {
		if bs.Phase == build.PhaseDone {
			continue
		}
		r.Today.InFlight = append(r.Today.InFlight, InFlightBuild{
			ID:           bs.ID,
			Phase:        bs.Phase,
			CurrentStage: bs.CurrentStage,
			TotalStages:  bs.TotalStages,
			CostUSD:      bs.CostUSD,
		})
	}

	// HEALTH section — aggregate slates and build states within the date window.
	r.Health.Days = days
	for _, s := range allSlates {
		if s.Date < sinceStr {
			continue
		}
		r.Health.SlatesRun++
		r.Health.Offered += len(s.Candidates)
		for _, c := range s.Candidates {
			if c.Status == state.StatusPicked {
				r.Health.Picked++
			}
		}
	}

	var totalRepairs, totalTurns, buildsWithData int
	for _, bs := range allBuildStates {
		if bs.Date < sinceStr {
			continue
		}
		if bs.Phase == build.PhaseDone || bs.Phase == build.PhaseGated {
			r.Health.Successful++
		}
		if bs.Turns > 0 || bs.Attempts > 0 {
			totalRepairs += bs.Attempts
			totalTurns += bs.Turns
			buildsWithData++
		}
	}
	if buildsWithData > 0 {
		r.Health.AvgRepairs = float64(totalRepairs) / float64(buildsWithData)
		r.Health.AvgTurns = float64(totalTurns) / float64(buildsWithData)
	}

	// COST section — aggregate build costs within the date window.
	r.Cost.BudgetPerBuildUSD = cfg.Claude.BudgetUSDPerBuild
	for _, bs := range allBuildStates {
		if bs.Date < sinceStr {
			continue
		}
		if bs.CostUSD <= 0 {
			continue
		}
		r.Cost.TotalBuilds++
		r.Cost.TotalUSD += bs.CostUSD
		if bs.CostUSD > r.Cost.MostExpensiveUSD {
			r.Cost.MostExpensiveUSD = bs.CostUSD
			r.Cost.MostExpensiveID = bs.ID
		}
	}
	if r.Cost.TotalBuilds > 0 {
		r.Cost.AvgPerBuildUSD = r.Cost.TotalUSD / float64(r.Cost.TotalBuilds)
	}

	return r, nil
}
