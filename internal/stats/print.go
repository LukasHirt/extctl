package stats

import (
	"fmt"
	"io"
	"strings"

	"github.com/LukasHirt/extctl/internal/state"
)

// Print writes the three-section stats report to w.
func Print(r *Report, w io.Writer) {
	printToday(r, w)
	fmt.Fprintln(w)
	printHealth(r, w)
	fmt.Fprintln(w)
	printCost(r, w)
}

func printToday(r *Report, w io.Writer) {
	fmt.Fprintf(w, "TODAY  %s\n", r.Today.Date)
	if !r.Today.HasSlate {
		fmt.Fprintln(w, "  No slate for today — run `extctl gen` to generate candidates.")
		return
	}
	fmt.Fprintf(w, "  %-14s%d total    %d fresh · %d carryover\n",
		"Candidates", r.Today.Total, r.Today.Fresh, r.Today.Carryover)

	statusOrder := []state.CandidateStatus{
		state.StatusNeedsApproval,
		state.StatusPicked,
		state.StatusDeclined,
		state.StatusDecayed,
		state.StatusBacklogged,
		state.StatusRejected,
	}
	var parts []string
	for _, st := range statusOrder {
		if n := r.Today.ByStatus[st]; n > 0 {
			parts = append(parts, fmt.Sprintf("%s %d", st, n))
		}
	}
	fmt.Fprintf(w, "  %-14s%s\n", "Status", strings.Join(parts, "  ·  "))

	if len(r.Today.InFlight) == 0 {
		fmt.Fprintf(w, "  %-14s(none)\n", "In-flight")
		return
	}
	for i, b := range r.Today.InFlight {
		label := ""
		if i == 0 {
			label = "In-flight"
		}
		stageStr := ""
		if b.TotalStages > 0 {
			stageStr = fmt.Sprintf("  stage %d/%d", b.CurrentStage, b.TotalStages)
		}
		fmt.Fprintf(w, "  %-14s%-40s  %-14s%s  $%.2f\n",
			label, b.ID, string(b.Phase), stageStr, b.CostUSD)
	}
}

func printHealth(r *Report, w io.Writer) {
	fmt.Fprintf(w, "PIPELINE  last %d days  (%d slates)\n", r.Health.Days, r.Health.SlatesRun)
	if r.Health.SlatesRun == 0 {
		fmt.Fprintln(w, "  No data yet.")
		return
	}
	fmt.Fprintf(w, "  %-14s%d candidates\n", "Offered", r.Health.Offered)

	pickPct := 0.0
	if r.Health.Offered > 0 {
		pickPct = float64(r.Health.Picked) / float64(r.Health.Offered) * 100
	}
	fmt.Fprintf(w, "  %-14s%d picked  (%.0f%%)\n", "Pick rate", r.Health.Picked, pickPct)

	buildPct := 0.0
	if r.Health.Picked > 0 {
		buildPct = float64(r.Health.Successful) / float64(r.Health.Picked) * 100
	}
	fmt.Fprintf(w, "  %-14s%d done  (%.0f%% of picked)\n", "Build rate", r.Health.Successful, buildPct)
	fmt.Fprintf(w, "  %-14s%.1f per build\n", "Avg repairs", r.Health.AvgRepairs)
	fmt.Fprintf(w, "  %-14s%.0f per build\n", "Avg turns", r.Health.AvgTurns)
}

func printCost(r *Report, w io.Writer) {
	fmt.Fprintf(w, "COST  last %d days\n", r.Health.Days)
	if r.Cost.TotalBuilds == 0 {
		fmt.Fprintln(w, "  No build costs recorded yet.")
		return
	}
	fmt.Fprintf(w, "  %-14s$%.2f\n", "Total", r.Cost.TotalUSD)
	highStr := ""
	if r.Cost.MostExpensiveID != "" {
		highStr = fmt.Sprintf("  ·  $%.2f highest (%s)", r.Cost.MostExpensiveUSD, r.Cost.MostExpensiveID)
	}
	fmt.Fprintf(w, "  %-14s$%.2f avg%s\n", "Per build", r.Cost.AvgPerBuildUSD, highStr)

	if r.Cost.BudgetPerBuildUSD > 0 {
		budgetTotal := r.Cost.BudgetPerBuildUSD * float64(r.Cost.TotalBuilds)
		pct := r.Cost.TotalUSD / budgetTotal * 100
		fmt.Fprintf(w, "  %-14s$%.2f / $%.2f  (%.0f%%)    [$%.0f/build × %d builds configured]\n",
			"Budget", r.Cost.TotalUSD, budgetTotal, pct,
			r.Cost.BudgetPerBuildUSD, r.Cost.TotalBuilds)
	}
}
