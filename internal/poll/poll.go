package poll

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
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
	Date   string
	Picked []state.Candidate
	NoPick bool // true when no candidate was in pick status
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

	var picked []state.Candidate

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
				picked = append(picked, *c)
			}
		}
	}

	// Update slate: mark newly-picked candidates and fetch their Jira comments.
	if len(picked) > 0 {
		pickedKeys := make(map[string]bool, len(picked))
		for _, p := range picked {
			pickedKeys[p.JiraKey] = true
		}
		updatedCandidates := make([]state.Candidate, len(slate.Candidates))
		copy(updatedCandidates, slate.Candidates)
		for i := range updatedCandidates {
			if !pickedKeys[updatedCandidates[i].JiraKey] {
				continue
			}
			updatedCandidates[i].Status = state.StatusPicked
			// Fetch issue comments to carry into the build pipeline as context.
			// Log a warning on error but don't block the build.
			if comments, err := jiraClient.GetComments(updatedCandidates[i].JiraKey); err != nil {
				fmt.Printf("poll: warning: could not fetch comments for %s: %v\n", updatedCandidates[i].JiraKey, err)
			} else {
				updatedCandidates[i].IssueComments = jira.FormatComments(comments)
				fmt.Printf("poll: fetched %d comment(s) for %s\n", len(comments), updatedCandidates[i].JiraKey)
			}
		}
		// Propagate the fetched comments back into picked so runBuild sees them.
		pickedByKey := make(map[string]*state.Candidate, len(updatedCandidates))
		for i := range updatedCandidates {
			pickedByKey[updatedCandidates[i].JiraKey] = &updatedCandidates[i]
		}
		for i := range picked {
			if c := pickedByKey[picked[i].JiraKey]; c != nil {
				picked[i].IssueComments = c.IssueComments
			}
		}
		slate.Candidates = updatedCandidates
		if err := state.Save(opts.Config.RunsDir, slate); err != nil {
			return nil, fmt.Errorf("save slate: %w", err)
		}
		for _, p := range picked {
			fmt.Printf("poll: picked %s (%s)\n", p.ID, p.JiraKey)
		}
	}

	// Collect all picked candidates (including ones picked in earlier runs).
	var allPicked []state.Candidate
	for _, c := range slate.Candidates {
		if c.Status == state.StatusPicked {
			allPicked = append(allPicked, c)
		}
	}

	if len(allPicked) == 0 {
		fmt.Printf("poll: no action for %s — candidates still awaiting approval\n", date)
		return &Result{Date: date, NoPick: true}, nil
	}

	// Dry-run: show what would happen without side-effects.
	if opts.DryRun {
		for _, p := range allPicked {
			fmt.Printf("dry-run: would build %s (%s)\n", p.ID, p.JiraKey)
		}
		return &Result{Date: date, Picked: allPicked}, nil
	}

	var wg sync.WaitGroup
	var budgetMu sync.Mutex
	for _, p := range allPicked {
		wg.Add(1)
		go func(candidate state.Candidate) {
			defer wg.Done()
			if err := runBuild(opts, date, candidate, jiraClient, &budgetMu); err != nil {
				fmt.Printf("poll: build error for %s: %v\n", candidate.ID, err)
			}
		}(p)
	}
	wg.Wait()

	return &Result{Date: date, Picked: allPicked}, nil
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
func runBuild(opts Options, date string, candidate state.Candidate, jiraClient *jira.Client, budgetMu *sync.Mutex) error {
	runsDir := opts.Config.RunsDir

	// Idempotency: check existing build state.
	bs, err := build.LoadState(runsDir, date, candidate.ID)
	if err != nil {
		return fmt.Errorf("load build state: %w", err)
	}
	logf := func(format string, args ...any) {
		fmt.Printf("["+candidate.ID+"] "+format, args...)
	}

	if bs != nil {
		switch bs.Phase {
		case build.PhaseDone:
			logf("build: already done (PR #%d)\n", bs.PR.Number)
			return nil
		case build.PhaseBlocked:
			logf("build: blocked — skipping (manual intervention needed, see PR)\n")
			return nil
		case build.PhasePlanReview, build.PhaseStagesReview:
			logf("build: waiting for approval (phase %s) — run `extctl approve-plan %s`\n", bs.Phase, candidate.ID)
			return nil
		}
	}

	// Acquire a PID lock so concurrent poll invocations don't double-build.
	acquired, err := acquireLock(runsDir, date, candidate.ID)
	if err != nil {
		return fmt.Errorf("acquire build lock: %w", err)
	}
	if !acquired {
		logf("build: already building in another process — skipping\n")
		return nil
	}
	defer releaseLock(runsDir, date, candidate.ID)

	if bs != nil {
		switch bs.Phase {
		case build.PhaseGated:
			logf("build: gate already passed; going to publish\n")
			return publish(opts, date, candidate, bs, false, jiraClient)
		case build.PhaseGating, build.PhaseRepairing:
			worktreePath := filepath.Join(runsDir, date, candidate.ID, "worktree")
			outputDir := filepath.Join(runsDir, date, candidate.ID)
			logf("build: resuming from %s (session %s, attempts %d)…\n", bs.Phase, bs.SessionID, bs.Attempts)
			return gateRepairPublish(opts, date, candidate, bs, worktreePath, outputDir, bs.SessionID, jiraClient)
		case build.PhasePlanning, build.PhaseStaging:
			logf("build: restarting from %s (previous run interrupted)\n", bs.Phase)
			// fall through to re-enter from the top of the pipeline
		default:
			logf("build: in phase %s; restarting build\n", bs.Phase)
		}
	}

	branch := fmt.Sprintf("ext/%s-%s", date, candidate.ID)
	worktreePath := filepath.Join(runsDir, date, candidate.ID, "worktree")

	// Check daily budget and write initial state atomically so concurrent builds
	// see each other's in-progress reservations before their own Claude run starts.
	budgetMu.Lock()
	if err := checkBudget(opts.Config, runsDir, date); err != nil {
		budgetMu.Unlock()
		return err
	}
	bs = &build.State{
		ID:      candidate.ID,
		Date:    date,
		JiraKey: candidate.JiraKey,
		Branch:  branch,
		Phase:   build.PhaseBuilding,
	}
	if err := build.SaveState(runsDir, bs); err != nil {
		budgetMu.Unlock()
		return fmt.Errorf("save initial build state: %w", err)
	}
	budgetMu.Unlock()

	// Create git worktree.
	repoPath := opts.Config.TargetRepo.Checkout
	logf("build: fetching origin in %s…\n", repoPath)
	if err := gitpkg.FetchOrigin(repoPath); err != nil {
		return fmt.Errorf("git fetch origin: %w", err)
	}
	baseBranch := "origin/" + opts.Config.DefaultBranch
	logf("build: creating worktree %s on branch %s…\n", worktreePath, branch)
	if err := gitpkg.CreateWorktree(repoPath, worktreePath, branch, baseBranch); err != nil {
		return fmt.Errorf("create worktree: %w", err)
	}

	// Run Phase A.5: planning — generate plan.md before any code is written.
	// Guard against overwriting an existing plan.md if we're resuming from a
	// PhasePlanning crash that already wrote the file (e.g. Claude finished but
	// the SaveState call below never completed). Mirrors the pattern used in
	// approve-plan for stages.md.
	planPath := filepath.Join(runsDir, date, candidate.ID, "plan.md")
	if _, statErr := os.Stat(planPath); statErr != nil {
		// plan.md doesn't exist yet — run planning.
		bs.Phase = build.PhasePlanning
		if err := build.SaveState(runsDir, bs); err != nil {
			return fmt.Errorf("save planning state: %w", err)
		}

		logf("planning: generating plan.md…\n")
		if err := build.Plan(opts.Config, candidate.ID, candidate.SpecMD, candidate.IssueComments, planPath); err != nil {
			bs.Phase = build.PhaseBlocked
			bs.ErrorMsg = "planning failed: " + err.Error()
			_ = build.SaveState(runsDir, bs)
			return fmt.Errorf("plan %s: %w", candidate.ID, err)
		}
	} else {
		logf("planning: plan.md already exists — skipping generation\n")
	}

	// Planning complete (or already done) — wait for human approval.
	bs.Phase = build.PhasePlanReview
	if err := build.SaveState(runsDir, bs); err != nil {
		return fmt.Errorf("save plan-review state: %w", err)
	}
	logf("planning: plan written to %s — run `extctl approve-plan %s` to continue\n", planPath, candidate.ID)
	return nil
}

// gateRepairPublish runs the gate → repair loop → publish tail.
// Called after BuildStage and when resuming a PhaseGating/PhaseRepairing build.
func gateRepairPublish(opts Options, date string, candidate state.Candidate, bs *build.State, worktreePath, outputDir, sessionID string, jiraClient *jira.Client) error {
	runsDir := opts.Config.RunsDir
	logf := func(format string, args ...any) {
		fmt.Printf("["+candidate.ID+"] "+format, args...)
	}
	logPrefix := "[" + candidate.ID + "] "

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
			return publishBlocked(opts, date, candidate, bs, gateResult, outputDir, jiraClient,
				fmt.Sprintf("gate failed after %d repair attempt(s)", bs.Attempts-1))
		}
		if bs.CostUSD >= opts.Config.Claude.BudgetUSDPerBuild {
			logf("build: per-build budget of $%.2f exceeded ($%.4f spent); skipping repair\n",
				opts.Config.Claude.BudgetUSDPerBuild, bs.CostUSD)
			return publishBlocked(opts, date, candidate, bs, gateResult, outputDir, jiraClient,
				fmt.Sprintf("per-build budget of $%.2f exceeded ($%.4f spent)", opts.Config.Claude.BudgetUSDPerBuild, bs.CostUSD))
		}

		logf("build: gate failed (attempt %d/%d); running repair…\n", bs.Attempts, maxRepairs)
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
			LogPrefix:    logPrefix,
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
	logf := func(format string, args ...any) {
		fmt.Printf("["+candidate.ID+"] "+format, args...)
	}
	runsDir := opts.Config.RunsDir
	repoPath := opts.Config.TargetRepo.Checkout
	worktreePath := filepath.Join(runsDir, date, candidate.ID, "worktree")

	bs.Phase = build.PhasePublishing
	_ = build.SaveState(runsDir, bs)

	logf("build: pushing branch %s…\n", bs.Branch)
	if err := gitpkg.PushBranch(repoPath, bs.Branch); err != nil {
		return fmt.Errorf("push branch: %w", err)
	}

	gateScore := 0.0
	gateHygiene, gateBuild, gateLint, gateUnit, gateE2E := "", "", "", "", ""
	if bs.Gate != nil {
		gateScore = bs.Gate.Score
		gateHygiene = bs.Gate.Stages.Hygiene
		gateBuild = bs.Gate.Stages.Build
		gateLint = bs.Gate.Stages.Lint
		gateUnit = bs.Gate.Stages.Unit
		gateE2E = bs.Gate.Stages.E2E
	}
	whatWasBuilt := build.SynthesizeSummary(build.SummarizeOptions{
		Config:      opts.Config,
		CandidateID: candidate.ID,
		Date:        date,
		SpecMD:      candidate.SpecMD,
		OutputDir:   filepath.Join(runsDir, date, candidate.ID),
		TotalStages: bs.TotalStages,
	})
	prBody := githubpkg.FormatBody(githubpkg.BodyOptions{
		SpecMD:       candidate.SpecMD,
		WhatWasBuilt: whatWasBuilt,
		JiraKey:      candidate.JiraKey,
		JiraURL:      candidate.JiraURL,
		GateScore:    gateScore,
		GateHygiene:  gateHygiene,
		GateBuild:    gateBuild,
		GateLint:     gateLint,
		GateUnit:     gateUnit,
		GateE2E:      gateE2E,
		CostUSD:      bs.CostUSD,
		Turns:        bs.Turns,
		Attempts:     bs.Attempts,
	})

	logf("build: opening PR on %s…\n", opts.Config.TargetRepo.Remote)
	pr, err := githubpkg.Create(githubpkg.PROptions{
		RepoSlug: opts.Config.TargetRepo.Remote,
		Branch:   bs.Branch,
		Title:    fmt.Sprintf("feat(%s): add %s", candidate.ID, candidate.Title),
		Body:     prBody,
		Labels:   []string{},
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
		logf("build: warning: could not comment on Jira issue: %v\n", addErr)
	}

	logf("build: done — PR #%d: %s\n", pr.Number, pr.URL)

	// Clean up worktree only on success.
	if err := gitpkg.RemoveWorktree(repoPath, worktreePath); err != nil {
		logf("build: warning: could not remove worktree: %v\n", err)
	}

	return nil
}

// publishBlocked pushes the current branch as a draft PR and comments with
// the failure details. The Jira issue is left in Doing — a developer or
// manager reviews the PR and decides what to do next.
func publishBlocked(opts Options, date string, candidate state.Candidate, bs *build.State, gateResult *gate.Result, outputDir string, jiraClient *jira.Client, reason string) error {
	logf := func(format string, args ...any) {
		fmt.Printf("["+candidate.ID+"] "+format, args...)
	}
	runsDir := opts.Config.RunsDir
	repoPath := opts.Config.TargetRepo.Checkout

	bs.Phase = build.PhaseBlocked
	bs.ErrorMsg = reason
	_ = build.SaveState(runsDir, bs)

	logf("build: exhausted repairs; pushing draft PR for manual review…\n")

	if err := gitpkg.PushBranch(repoPath, bs.Branch); err != nil {
		return fmt.Errorf("push blocked branch: %w", err)
	}

	whatWasBuilt := build.SynthesizeSummary(build.SummarizeOptions{
		Config:      opts.Config,
		CandidateID: candidate.ID,
		Date:        date,
		SpecMD:      candidate.SpecMD,
		OutputDir:   filepath.Join(runsDir, date, candidate.ID),
		TotalStages: bs.TotalStages,
	})
	prBody := githubpkg.FormatBody(githubpkg.BodyOptions{
		SpecMD:       candidate.SpecMD,
		WhatWasBuilt: whatWasBuilt,
		JiraKey:      candidate.JiraKey,
		JiraURL:      candidate.JiraURL,
		GateScore:    gateResult.Score,
		GateHygiene:  gateResult.Stages.Hygiene,
		GateBuild:    gateResult.Stages.Build,
		GateLint:     gateResult.Stages.Lint,
		GateUnit:     gateResult.Stages.Unit,
		GateE2E:      gateResult.Stages.E2E,
		CostUSD:      bs.CostUSD,
		Turns:        bs.Turns,
		Attempts:     bs.Attempts,
	})

	pr, err := githubpkg.Create(githubpkg.PROptions{
		RepoSlug: opts.Config.TargetRepo.Remote,
		Branch:   bs.Branch,
		Title:    fmt.Sprintf("feat(%s): add %s", candidate.ID, candidate.Title),
		Body:     prBody,
		Labels:   []string{},
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
			"### Gate stages\n\n| Stage | Result |\n|---|---|\n| hygiene | %s |\n| build | %s |\n| lint | %s |\n| unit | %s |\n| e2e | %s |\n\n"+
			"<details><summary>Gate log</summary>\n\n```\n%s\n```\n\n</details>",
		bs.Attempts-1,
		gateResult.Stages.Hygiene, gateResult.Stages.Build,
		gateResult.Stages.Lint, gateResult.Stages.Unit, gateResult.Stages.E2E,
		truncate(gateLog, 3000),
	)
	if err := githubpkg.AddComment(opts.Config.TargetRepo.Remote, pr.Number, failComment); err != nil {
		logf("build: warning: could not comment on draft PR: %v\n", err)
	}

	logf("build: draft PR #%d opened for manual review: %s\n", pr.Number, pr.URL)
	logf("build: Jira issue %s left in Doing — transition manually after fix\n", candidate.JiraKey)
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
	return gate.Run(absScript, worktreePath, extID, outputDir, bulletCount, opts.Config.TargetRepo.Checkout)
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
// Must be called while holding the caller's budgetMu to avoid TOCTOU races
// between concurrent builds reading the same zero-cost initial state.
// In-progress builds (Building/Gating/Repairing) count as BudgetUSDPerBuild
// each (worst-case reservation) so that a second goroutine sees the slot taken.
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
		if err != nil || bs == nil {
			continue
		}
		switch bs.Phase {
		case build.PhaseBuilding, build.PhaseGating, build.PhaseRepairing:
			totalCost += cfg.Claude.BudgetUSDPerBuild
		default:
			totalCost += bs.CostUSD
		}
	}
	if totalCost >= cfg.Claude.BudgetUSDPerDay {
		return fmt.Errorf("daily budget of $%.2f exceeded (reserved/spent $%.2f); skipping build",
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
			E2E:     r.Stages.E2E,
		},
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

func lockFilePath(runsDir, date, id string) string {
	return filepath.Join(runsDir, date, id, "build.lock")
}

// acquireLock writes a PID lock file for the build. Returns true if the lock
// was acquired. Returns false (no error) if another live process holds the
// lock. Stale locks from dead processes are removed automatically.
func acquireLock(runsDir, date, id string) (bool, error) {
	path := lockFilePath(runsDir, date, id)

	data, err := os.ReadFile(path)
	if err == nil {
		var pid int
		if _, scanErr := fmt.Sscanf(strings.TrimSpace(string(data)), "%d", &pid); scanErr == nil {
			if isProcessAlive(pid) {
				return false, nil
			}
			fmt.Printf("build: removing stale lock for %s (pid %d is gone)\n", id, pid)
			_ = os.Remove(path)
		}
	} else if !os.IsNotExist(err) {
		return false, fmt.Errorf("read build lock: %w", err)
	}

	dir := filepath.Join(runsDir, date, id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return false, fmt.Errorf("mkdir for build lock: %w", err)
	}
	if err := os.WriteFile(path, []byte(fmt.Sprintf("%d\n", os.Getpid())), 0o644); err != nil {
		return false, fmt.Errorf("write build lock: %w", err)
	}
	return true, nil
}

func releaseLock(runsDir, date, id string) {
	_ = os.Remove(lockFilePath(runsDir, date, id))
}

// isProcessAlive returns true if a process with the given PID is running.
// EPERM means the process exists but we can't signal it — still alive.
func isProcessAlive(pid int) bool {
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	err = p.Signal(syscall.Signal(0))
	return err == nil || errors.Is(err, syscall.EPERM)
}
