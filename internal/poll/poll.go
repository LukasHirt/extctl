package poll

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/LukasHirt/extctl/internal/build"
	"github.com/LukasHirt/extctl/internal/config"
	"github.com/LukasHirt/extctl/internal/gate"
	githubpkg "github.com/LukasHirt/extctl/internal/github"
	gitpkg "github.com/LukasHirt/extctl/internal/git"
	"github.com/LukasHirt/extctl/internal/jira"
	"github.com/LukasHirt/extctl/internal/state"
)

// Options controls a single poll pass.
type Options struct {
	Config       *config.Config
	DryRun       bool   // print what would happen without touching Jira or state
	Date         string // YYYY-MM-DD; defaults to today in configured timezone
	ScaffoldDir  string // path to scaffold/ (defaults to ./scaffold)
	GateScript   string // path to gate/run-gate.sh (defaults to ./gate/run-gate.sh)
	ClaudeMDPath string // path to CLAUDE.md (defaults to ./CLAUDE.md)
}

// Result summarises what the poll pass found and did.
type Result struct {
	Date     string
	Picked   *state.Candidate // nil if no pick detected
	Declined []state.Candidate
	NoPick   bool // true when no candidate was in pick status
}

// Run executes one poll pass:
//  1. Load today's slate.
//  2. Check for merged PRs and transition those to Done in Jira.
//  3. Query Jira for candidates in pick status.
//  4. Trigger the build pipeline for any newly-picked candidate.
//
// Jira status transitions are minimal by design: extctl only sets Done
// (when a PR is merged). All other status changes are made by the manager
// or developer in Jira directly.
func Run(opts Options) (*Result, error) {
	if opts.ScaffoldDir == "" {
		opts.ScaffoldDir = "scaffold"
	}
	if opts.GateScript == "" {
		opts.GateScript = "gate/run-gate.sh"
	}
	if opts.ClaudeMDPath == "" {
		opts.ClaudeMDPath = "CLAUDE.md"
	}

	date := opts.Date
	if date == "" {
		loc, err := time.LoadLocation(opts.Config.Timezone)
		if err != nil {
			return nil, fmt.Errorf("load timezone: %w", err)
		}
		date = time.Now().In(loc).Format("2006-01-02")
	}

	// Load today's slate.
	slate, err := state.Load(opts.Config.RunsDir, date)
	if err != nil {
		return nil, fmt.Errorf("load slate: %w", err)
	}
	if slate == nil {
		fmt.Printf("poll: no slate for %s — run `extctl gen` first\n", date)
		return &Result{Date: date, NoPick: true}, nil
	}

	// Build Jira client.
	jiraToken, err := config.JiraToken()
	if err != nil {
		return nil, err
	}
	jiraEmail, err := config.JiraEmail()
	if err != nil {
		return nil, err
	}
	jiraClient := jira.NewClient(opts.Config.Jira.BaseURL, jiraEmail, jiraToken)

	// Check for merged PRs and transition those Jira issues to Done.
	// This is the only Jira status transition extctl performs automatically.
	if !opts.DryRun {
		checkMergedPRs(opts, date, slate, jiraClient)
	}

	// Collect candidates still awaiting approval.
	var open []state.Candidate
	for _, c := range slate.Candidates {
		if c.Status == state.StatusNeedsApproval {
			open = append(open, c)
		}
	}
	if len(open) == 0 {
		fmt.Printf("poll: no open candidates for %s\n", date)
		return &Result{Date: date, NoPick: true}, nil
	}

	// Query Jira for current status of all open candidates.
	openKeys := make([]string, 0, len(open))
	for _, c := range open {
		if c.JiraKey != "" {
			openKeys = append(openKeys, `"`+c.JiraKey+`"`)
		}
	}

	var picked *state.Candidate

	if len(openKeys) > 0 {
		jql := fmt.Sprintf("issueKey in (%s)", strings.Join(openKeys, ","))
		refs, err := jiraClient.SearchIssues(jql, []string{"status"})
		if err != nil {
			return nil, fmt.Errorf("poll Jira: %w", err)
		}

		jiraStatus := make(map[string]string, len(refs))
		for _, r := range refs {
			jiraStatus[r.Key] = r.Status
		}

		for i := range open {
			c := &open[i]
			status, ok := jiraStatus[c.JiraKey]
			if !ok {
				continue
			}
			if status == opts.Config.Jira.PickStatus {
				if picked != nil {
					fmt.Printf("poll: warning: multiple picked candidates; ignoring %s\n", c.JiraKey)
					continue
				}
				picked = c
			}
		}
	}

	if picked == nil {
		fmt.Printf("poll: no action for %s — candidates still awaiting approval\n", date)
		return &Result{Date: date, NoPick: true}, nil
	}

	// Dry-run: show what would happen without side-effects.
	if opts.DryRun {
		fmt.Printf("dry-run: would pick %s (%s) and start build\n", picked.ID, picked.JiraKey)
		return &Result{Date: date, Picked: picked}, nil
	}

	// Update slate: mark the picked candidate locally.
	updatedCandidates := make([]state.Candidate, len(slate.Candidates))
	copy(updatedCandidates, slate.Candidates)
	for i := range updatedCandidates {
		if updatedCandidates[i].JiraKey == picked.JiraKey {
			updatedCandidates[i].Status = state.StatusPicked
		}
	}
	slate.Candidates = updatedCandidates
	if err := state.Save(opts.Config.RunsDir, slate); err != nil {
		return nil, fmt.Errorf("save slate: %w", err)
	}

	fmt.Printf("poll: picked %s (%s)\n", picked.ID, picked.JiraKey)
	if err := runBuild(opts, date, *picked, jiraClient); err != nil {
		fmt.Printf("poll: build error: %v\n", err)
	}

	return &Result{Date: date, Picked: picked}, nil
}

// checkMergedPRs looks for any candidates whose PR has been merged and
// transitions their Jira issue to Done. This is the only automated Jira
// transition extctl performs.
func checkMergedPRs(opts Options, date string, slate *state.Slate, jiraClient *jira.Client) {
	for _, c := range slate.Candidates {
		bs, err := build.LoadState(opts.Config.RunsDir, date, c.ID)
		if err != nil || bs == nil || bs.Phase != build.PhaseDone || bs.PR == nil {
			continue
		}
		if bs.JiraTransitionedDone {
			continue
		}
		merged, err := githubpkg.IsMerged(opts.Config.TargetRepo.Remote, bs.PR.Number)
		if err != nil {
			fmt.Printf("poll: could not check PR #%d merge status: %v\n", bs.PR.Number, err)
			continue
		}
		if !merged {
			continue
		}
		if err := jiraClient.Transition(c.JiraKey, opts.Config.Jira.BuildStatus); err != nil {
			fmt.Printf("poll: could not transition %s to Done: %v\n", c.JiraKey, err)
			continue
		}
		bs.JiraTransitionedDone = true
		if err := build.SaveState(opts.Config.RunsDir, bs); err != nil {
			fmt.Printf("poll: could not save build state after Done transition: %v\n", err)
		}
		fmt.Printf("poll: PR #%d merged — transitioned %s to Done\n", bs.PR.Number, c.JiraKey)
	}
}

// runBuild runs the full Phase B pipeline for a picked candidate:
// build → gate → repair loop → publish (or draft PR on block)
func runBuild(opts Options, date string, candidate state.Candidate, jiraClient *jira.Client) error {
	runsDir := opts.Config.RunsDir

	// Idempotency: check existing build state.
	bs, err := build.LoadState(runsDir, date, candidate.ID)
	if err != nil {
		return fmt.Errorf("load build state: %w", err)
	}
	if bs != nil {
		switch bs.Phase {
		case build.PhaseDone:
			fmt.Printf("build: %s already done (PR #%d)\n", candidate.ID, bs.PR.Number)
			return nil
		case build.PhaseBlocked:
			fmt.Printf("build: %s is blocked — skipping (manual intervention needed, see PR)\n", candidate.ID)
			return nil
		case build.PhaseGated:
			fmt.Printf("build: %s gate already passed; going to publish\n", candidate.ID)
			return publish(opts, date, candidate, bs, false, jiraClient)
		}
		fmt.Printf("build: %s in phase %s; restarting build\n", candidate.ID, bs.Phase)
	}

	// Check daily budget.
	if err := checkBudget(opts.Config, runsDir, date); err != nil {
		return err
	}

	branch := fmt.Sprintf("ext/%s-%s", date, candidate.ID)
	worktreePath := filepath.Join(runsDir, date, candidate.ID, "worktree")
	outputDir := filepath.Join(runsDir, date, candidate.ID)

	bs = &build.State{
		ID:      candidate.ID,
		Date:    date,
		JiraKey: candidate.JiraKey,
		Branch:  branch,
		Phase:   build.PhaseBuilding,
	}
	if err := build.SaveState(runsDir, bs); err != nil {
		return fmt.Errorf("save initial build state: %w", err)
	}

	// Create git worktree.
	repoPath := opts.Config.TargetRepo.Checkout
	fmt.Printf("build: fetching origin in %s…\n", repoPath)
	if err := gitpkg.FetchOrigin(repoPath); err != nil {
		return fmt.Errorf("git fetch origin: %w", err)
	}
	baseBranch := "origin/" + opts.Config.DefaultBranch
	fmt.Printf("build: creating worktree %s on branch %s…\n", worktreePath, branch)
	if err := gitpkg.CreateWorktree(repoPath, worktreePath, branch, baseBranch); err != nil {
		return fmt.Errorf("create worktree: %w", err)
	}

	// Run Phase B build.
	buildResult, err := build.Run(build.Options{
		Config:       opts.Config,
		CandidateID:  candidate.ID,
		JiraKey:      candidate.JiraKey,
		SpecMD:       candidate.SpecMD,
		Effort:       candidate.Effort,
		Date:         date,
		WorktreePath: worktreePath,
		ScaffoldDir:  opts.ScaffoldDir,
		ClaudeMDPath: opts.ClaudeMDPath,
	})
	if err != nil {
		bs.Phase = build.PhaseBlocked
		bs.ErrorMsg = err.Error()
		_ = build.SaveState(runsDir, bs)
		return fmt.Errorf("build run: %w", err)
	}

	sessionID := buildResult.SessionID
	bs.SessionID = sessionID
	bs.CostUSD = buildResult.CostUSD
	bs.Turns = buildResult.Turns
	bs.Attempts = buildResult.Attempts
	bs.Phase = build.PhaseGating
	_ = build.SaveState(runsDir, bs)

	// Run gate → repair loop.
	gateResult, err := runGate(opts, worktreePath, candidate.ID, outputDir, candidate.SpecMD)
	if err != nil {
		return fmt.Errorf("gate: %w", err)
	}
	bs.Gate = toStateGate(gateResult)

	maxRepairs := opts.Config.Claude.MaxRepairAttempts
	if maxRepairs < 1 {
		maxRepairs = 1
	}

	for !gateResult.Passed {
		if bs.Attempts > maxRepairs {
			// All repair attempts exhausted: push a draft PR and comment with details.
			return publishBlocked(opts, date, candidate, bs, gateResult, outputDir, jiraClient)
		}

		fmt.Printf("build: gate failed (attempt %d/%d); running repair…\n", bs.Attempts, maxRepairs)
		bs.Phase = build.PhaseRepairing
		_ = build.SaveState(runsDir, bs)

		gateLog, _ := gate.ReadLog(outputDir)
		repairResult, repairErr := build.Repair(build.Options{
			Config:       opts.Config,
			CandidateID:  candidate.ID,
			JiraKey:      candidate.JiraKey,
			SpecMD:       candidate.SpecMD,
			Effort:       candidate.Effort,
			Date:         date,
			WorktreePath: worktreePath,
		}, gateLog, sessionID)
		if repairErr != nil {
			return fmt.Errorf("repair attempt %d: %w", bs.Attempts, repairErr)
		}

		sessionID = repairResult.SessionID
		bs.SessionID = sessionID
		bs.CostUSD += repairResult.CostUSD
		bs.Turns += repairResult.Turns
		bs.Attempts++
		bs.Phase = build.PhaseGating
		_ = build.SaveState(runsDir, bs)

		gateResult, err = runGate(opts, worktreePath, candidate.ID, outputDir, candidate.SpecMD)
		if err != nil {
			return fmt.Errorf("re-gate after repair %d: %w", bs.Attempts-1, err)
		}
		bs.Gate = toStateGate(gateResult)
	}

	bs.Phase = build.PhaseGated
	_ = build.SaveState(runsDir, bs)

	return publish(opts, date, candidate, bs, false, jiraClient)
}

// publish pushes the branch and opens a ready-for-review PR.
func publish(opts Options, date string, candidate state.Candidate, bs *build.State, isDraft bool, jiraClient *jira.Client) error {
	runsDir := opts.Config.RunsDir
	repoPath := opts.Config.TargetRepo.Checkout
	worktreePath := filepath.Join(runsDir, date, candidate.ID, "worktree")

	bs.Phase = build.PhasePublishing
	_ = build.SaveState(runsDir, bs)

	fmt.Printf("build: pushing branch %s…\n", bs.Branch)
	if err := gitpkg.PushBranch(repoPath, bs.Branch); err != nil {
		return fmt.Errorf("push branch: %w", err)
	}

	gateScore := 0.0
	if bs.Gate != nil {
		gateScore = bs.Gate.Score
	}
	prBody := githubpkg.FormatBody(
		candidate.SpecMD,
		"",
		candidate.JiraKey,
		gateScore,
		bs.CostUSD,
		bs.Turns,
		bs.Attempts,
	)

	fmt.Printf("build: opening PR on %s…\n", opts.Config.TargetRepo.Remote)
	pr, err := githubpkg.Create(githubpkg.PROptions{
		RepoSlug: opts.Config.TargetRepo.Remote,
		Branch:   bs.Branch,
		Title:    candidate.Title,
		Body:     prBody,
		Labels:   []string{"claude-generated", "delivered"},
		Draft:    isDraft,
	})
	if err != nil {
		return fmt.Errorf("create PR: %w", err)
	}

	bs.PR = &build.PRResult{Number: pr.Number, URL: pr.URL, Ready: !isDraft}
	bs.Phase = build.PhaseDone
	_ = build.SaveState(runsDir, bs)

	// Comment on the Jira issue with the PR link (no status transition — manager does that).
	comment := fmt.Sprintf("PR opened: %s\n\nGate score: %.2f | Cost: $%.2f | Turns: %d | Attempts: %d",
		pr.URL, gateScore, bs.CostUSD, bs.Turns, bs.Attempts)
	if addErr := jiraClient.AddComment(candidate.JiraKey, comment); addErr != nil {
		fmt.Printf("build: warning: could not comment on Jira issue: %v\n", addErr)
	}

	fmt.Printf("build: done — PR #%d: %s\n", pr.Number, pr.URL)

	// Clean up worktree only on success.
	if err := gitpkg.RemoveWorktree(repoPath, worktreePath); err != nil {
		fmt.Printf("build: warning: could not remove worktree: %v\n", err)
	}

	return nil
}

// publishBlocked pushes the current branch as a draft PR and comments with
// the failure details. The Jira issue is left in Doing — a developer or
// manager reviews the PR and decides what to do next.
func publishBlocked(opts Options, date string, candidate state.Candidate, bs *build.State, gateResult *gate.Result, outputDir string, jiraClient *jira.Client) error {
	runsDir := opts.Config.RunsDir
	repoPath := opts.Config.TargetRepo.Checkout

	bs.Phase = build.PhaseBlocked
	bs.ErrorMsg = fmt.Sprintf("gate failed after %d repair attempt(s)", bs.Attempts-1)
	_ = build.SaveState(runsDir, bs)

	fmt.Printf("build: %s exhausted repairs; pushing draft PR for manual review…\n", candidate.ID)

	if err := gitpkg.PushBranch(repoPath, bs.Branch); err != nil {
		return fmt.Errorf("push blocked branch: %w", err)
	}

	prBody := githubpkg.FormatBody(
		candidate.SpecMD,
		"",
		candidate.JiraKey,
		gateResult.Score,
		bs.CostUSD,
		bs.Turns,
		bs.Attempts,
	)

	pr, err := githubpkg.Create(githubpkg.PROptions{
		RepoSlug: opts.Config.TargetRepo.Remote,
		Branch:   bs.Branch,
		Title:    candidate.Title,
		Body:     prBody,
		Labels:   []string{"claude-generated"},
		Draft:    true,
	})
	if err != nil {
		return fmt.Errorf("create draft PR: %w", err)
	}

	bs.PR = &build.PRResult{Number: pr.Number, URL: pr.URL, Ready: false}
	_ = build.SaveState(runsDir, bs)

	// Comment on the PR with failure details.
	gateLog, _ := gate.ReadLog(outputDir)
	failComment := fmt.Sprintf(
		"## Automated repair exhausted\n\nThe pipeline ran %d repair attempt(s) and the gate still failed.\n"+
			"**Manual fix required** — review the gate output below and push a fix commit.\n\n"+
			"### Gate stages\n\n| Stage | Result |\n|---|---|\n| hygiene | %s |\n| build | %s |\n| lint | %s |\n| unit | %s |\n\n"+
			"<details><summary>Gate log</summary>\n\n```\n%s\n```\n\n</details>",
		bs.Attempts-1,
		gateResult.Stages.Hygiene, gateResult.Stages.Build,
		gateResult.Stages.Lint, gateResult.Stages.Unit,
		truncate(gateLog, 3000),
	)
	if err := githubpkg.AddComment(opts.Config.TargetRepo.Remote, pr.Number, failComment); err != nil {
		fmt.Printf("build: warning: could not comment on draft PR: %v\n", err)
	}

	fmt.Printf("build: draft PR #%d opened for manual review: %s\n", pr.Number, pr.URL)
	fmt.Printf("build: Jira issue %s left in Doing — transition manually after fix\n", candidate.JiraKey)
	return nil
}

// runGate shells out to gate/run-gate.sh and returns the result.
func runGate(opts Options, worktreePath, extID, outputDir, specMD string) (*gate.Result, error) {
	bulletCount := countBullets(specMD)
	if bulletCount < 1 {
		bulletCount = 1
	}
	absScript, err := filepath.Abs(opts.GateScript)
	if err != nil {
		return nil, fmt.Errorf("resolve gate script path: %w", err)
	}
	return gate.Run(absScript, worktreePath, extID, outputDir, bulletCount)
}

// countBullets counts bullet lines as a rough acceptance-bullet count.
func countBullets(specMD string) int {
	n := 0
	for _, line := range strings.Split(specMD, "\n") {
		trimmed := strings.TrimLeft(line, " \t")
		if len(trimmed) > 0 && (trimmed[0] == '-' || trimmed[0] == '*') {
			n++
		}
	}
	return n
}

// checkBudget returns an error if the daily build budget is already exceeded.
func checkBudget(cfg *config.Config, runsDir, date string) error {
	dirPath := filepath.Join(runsDir, date)
	entries, err := os.ReadDir(dirPath)
	if err != nil {
		return nil // directory may not exist yet
	}
	var totalCost float64
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		bs, err := build.LoadState(runsDir, date, e.Name())
		if err == nil && bs != nil {
			totalCost += bs.CostUSD
		}
	}
	if totalCost >= cfg.Claude.BudgetUSDPerDay {
		return fmt.Errorf("daily budget of $%.2f exceeded (spent $%.2f); skipping build",
			cfg.Claude.BudgetUSDPerDay, totalCost)
	}
	return nil
}

func toStateGate(r *gate.Result) *build.GateResult {
	return &build.GateResult{
		Passed: r.Passed,
		Score:  r.Score,
		Stages: build.GateStages{
			Hygiene: r.Stages.Hygiene,
			Build:   r.Stages.Build,
			Lint:    r.Stages.Lint,
			Unit:    r.Stages.Unit,
		},
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
