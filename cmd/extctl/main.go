package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/LukasHirt/extctl/internal/build"
	"github.com/LukasHirt/extctl/internal/config"
	"github.com/LukasHirt/extctl/internal/doctor"
	"github.com/LukasHirt/extctl/internal/gate"
	"github.com/LukasHirt/extctl/internal/gen"
	gitpkg "github.com/LukasHirt/extctl/internal/git"
	githubpkg "github.com/LukasHirt/extctl/internal/github"
	"github.com/LukasHirt/extctl/internal/jira"
	"github.com/LukasHirt/extctl/internal/marketplace"
	"github.com/LukasHirt/extctl/internal/media"
	"github.com/LukasHirt/extctl/internal/poll"
	"github.com/LukasHirt/extctl/internal/release"
	"github.com/LukasHirt/extctl/internal/state"
	"github.com/LukasHirt/extctl/internal/stats"
)

var (
	cfgFile string
	cfg     *config.Config
)

var rootCmd = &cobra.Command{
	Use:   "extctl",
	Short: "Daily oCIS Web extension candidate pipeline",
	Long: `extctl automates the daily oCIS Web extension candidate pipeline:
  - generates 3 agentic extension specs via Claude Code
  - creates Jira issues for review
  - builds the picked candidate into a GitHub PR`,
	SilenceUsage: true,
	PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
		// Skip config loading for commands that don't need it. doctor does
		// its own tolerant config loading so it can report a broken/missing
		// extctl.yaml as a finding instead of hard-failing here.
		if cmd.Name() == "version" || cmd.Name() == "help" || cmd.Name() == "doctor" {
			return nil
		}
		var err error
		cfg, err = config.Load(cfgFile)
		if err != nil {
			return fmt.Errorf("load config: %w", err)
		}

		// Only the commands that actually touch the target repo need its
		// checkout provisioned. slate/stats/version never read it.
		needsCheckout := map[string]bool{
			"gen": true, "poll": true, "gate": true,
			"approve-plan": true, "approve-stages": true, "release": true,
			"publish": true, "approve": true, "retry-screenshots": true,
		}
		if needsCheckout[cmd.Name()] {
			if err := gitpkg.EnsureCheckout(cfg.TargetRepo.Remote, cfg.TargetRepo.Checkout, cfg.DefaultBranch); err != nil {
				return fmt.Errorf("ensure target repo checkout: %w", err)
			}
		}
		// publish and its approve/retry-screenshots subcommands are the only
		// ones that also need a second repo checked out — they act on
		// owncloud/marketplace, not TargetRepo.
		needsMarketplaceCheckout := map[string]bool{"publish": true, "approve": true, "retry-screenshots": true}
		if needsMarketplaceCheckout[cmd.Name()] {
			if err := gitpkg.EnsureCheckout(cfg.MarketplaceRepo.Remote, cfg.MarketplaceRepo.Checkout, cfg.MarketplaceRepo.DefaultBranch); err != nil {
				return fmt.Errorf("ensure marketplace repo checkout: %w", err)
			}
		}
		return nil
	},
}

// --- gen command ---

var (
	genDryRun   bool
	genSkipJira bool
	genNoReview bool
	genFromFile string
	genModel    string
	genDate     string
)

var genCmd = &cobra.Command{
	Use:   "gen",
	Short: "Generate today's 3 fresh agentic extension specs and create Jira issues",
	RunE: func(cmd *cobra.Command, args []string) error {
		result, err := gen.Run(gen.Options{
			Config:   cfg,
			DryRun:   genDryRun,
			SkipJira: genSkipJira,
			NoReview: genNoReview,
			FromFile: genFromFile,
			Date:     genDate,
			Model:    genModel,
		})
		if err != nil {
			return err
		}

		if result.DryRun {
			return nil
		}

		fmt.Printf("\n✓ Slate for %s\n\n", result.Date)

		if len(result.Carryovers) > 0 {
			fmt.Println("Carryovers:")
			for _, c := range result.Carryovers {
				fmt.Printf("  [%d/%d] %s — %s\n  %s\n",
					c.Appearances, cfg.Decay.MaxAppearances,
					c.JiraKey, c.Title, c.JiraURL)
			}
			fmt.Println()
		}

		fmt.Println("Fresh candidates:")
		for _, c := range result.Fresh {
			fmt.Printf("  %s — %s\n  %s\n", c.JiraKey, c.Title, c.JiraURL)
		}

		fmt.Printf("\nTotal candidates: %d (%d fresh + %d carryover)\n",
			len(result.Fresh)+len(result.Carryovers),
			len(result.Fresh), len(result.Carryovers))
		fmt.Println("\nSend the above links to the manager for today's pick.")
		return nil
	},
}

// --- slate command ---

var slateCmd = &cobra.Command{
	Use:   "slate",
	Short: "Manage the daily candidate slate",
}

var slateStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show today's slate status",
	RunE: func(cmd *cobra.Command, args []string) error {
		slates, err := state.LoadAll(cfg.RunsDir)
		if err != nil {
			return err
		}
		if len(slates) == 0 {
			fmt.Println("No slates found.")
			return nil
		}
		latest := slates[len(slates)-1]
		fmt.Printf("Slate for %s (%d candidates)\n\n", latest.Date, len(latest.Candidates))
		for _, c := range latest.Candidates {
			tag := ""
			if c.Origin == "carryover" {
				tag = fmt.Sprintf(" [carryover %d/%d]", c.Appearances, cfg.Decay.MaxAppearances)
			}
			fmt.Printf("  %-20s %-12s %s%s\n  %s\n",
				c.JiraKey, string(c.Status), c.Title, tag, c.JiraURL)
		}
		return nil
	},
}

var slateCarryoversCmd = &cobra.Command{
	Use:   "carryovers",
	Short: "List current carryover candidates",
	RunE: func(cmd *cobra.Command, args []string) error {
		format, _ := cmd.Flags().GetString("format")
		slates, err := state.LoadAll(cfg.RunsDir)
		if err != nil {
			return err
		}
		// Use today's date from the last slate or system time.
		today := ""
		if len(slates) > 0 {
			today = slates[len(slates)-1].Date
		}
		carryovers := state.Carryovers(slates, today, cfg.Decay.MaxAppearances)
		if format == "dedup-hint" {
			for _, c := range carryovers {
				fmt.Printf("- %s: %s (appearances: %d/%d)\n",
					c.ID, c.Title, c.Appearances, cfg.Decay.MaxAppearances)
			}
			return nil
		}
		for _, c := range carryovers {
			fmt.Printf("%s  %s  appearances:%d/%d  %s\n",
				c.JiraKey, c.Title, c.Appearances, cfg.Decay.MaxAppearances, c.JiraURL)
		}
		return nil
	},
}

// --- poll command ---

var pollDryRun bool
var pollDate string

var pollCmd = &cobra.Command{
	Use:   "poll",
	Short: "Poll Jira for a candidate pick and trigger the build if found",
	RunE: func(cmd *cobra.Command, args []string) error {
		result, err := poll.Run(poll.Options{
			Config: cfg,
			DryRun: pollDryRun,
			Date:   pollDate,
		})
		if err != nil {
			return err
		}
		if result.NoPick {
			return nil
		}
		for _, p := range result.Picked {
			fmt.Printf("\nPicked: %s — %s\n  %s\n", p.JiraKey, p.Title, p.JiraURL)
		}
		return nil
	},
}

// --- gate command ---

var gateCmd = &cobra.Command{
	Use:   "gate <candidate-id>",
	Short: "Run the gate on an existing worktree (for debugging)",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		candidateID := args[0]

		loc, _ := time.LoadLocation(cfg.Timezone)
		date := time.Now().In(loc).Format("2006-01-02")

		worktreePath := filepath.Join(cfg.RunsDir, date, candidateID, "worktree")
		outputDir := filepath.Join(cfg.RunsDir, date, candidateID)
		scriptPath, err := filepath.Abs("gate/run-gate.sh")
		if err != nil {
			return err
		}

		result, err := gate.Run(scriptPath, worktreePath, candidateID, outputDir, 1, cfg.TargetRepo.Checkout, candidateID)
		if err != nil {
			return err
		}
		if result.Passed {
			fmt.Printf("gate PASSED (score %.2f)\n", result.Score)
		} else {
			fmt.Printf("gate FAILED\n")
			fmt.Printf("stages: hygiene=%s build=%s lint=%s unit=%s e2e=%s\n",
				result.Stages.Hygiene, result.Stages.Build,
				result.Stages.Lint, result.Stages.Unit, result.Stages.E2E)
			gateLog, _ := gate.ReadLog(outputDir)
			if gateLog != "" {
				fmt.Printf("\ngate.log:\n%s\n", gateLog)
			}
		}
		return nil
	},
}

// --- approve-plan command ---

var approvePlanCmd = &cobra.Command{
	Use:   "approve-plan <candidate-id>",
	Short: "Approve the plan for a candidate and derive implementation stages",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		candidateID := args[0]

		// Look up the candidate from slates.
		slates, err := state.LoadAll(cfg.RunsDir)
		if err != nil {
			return fmt.Errorf("load slates: %w", err)
		}
		var (
			candidate *state.Candidate
			date      string
		)
		for i := len(slates) - 1; i >= 0; i-- {
			for j := range slates[i].Candidates {
				c := &slates[i].Candidates[j]
				if c.ID == candidateID || c.JiraKey == candidateID {
					candidate = c
					break
				}
			}
			if candidate != nil {
				break
			}
		}
		if candidate == nil {
			return fmt.Errorf("candidate %q not found in any slate", candidateID)
		}

		// Load build state — scan all dates because a picked candidate may have
		// been carried over into a newer slate, making slates[i].Date wrong.
		bs, err := build.FindState(cfg.RunsDir, candidate.ID)
		if err != nil {
			return fmt.Errorf("load build state: %w", err)
		}
		if bs == nil {
			return fmt.Errorf("candidate %s has no build state — run `extctl poll` or `extctl build %s` first", candidate.ID, candidate.ID)
		}
		if bs.Phase != build.PhasePlanReview && bs.Phase != build.PhaseStaging {
			return fmt.Errorf("candidate %s is not in plan_review phase (current: %s)", candidate.ID, bs.Phase)
		}
		date = bs.Date

		// Check plan.md exists.
		planPath := filepath.Join(cfg.RunsDir, date, candidate.ID, "plan.md")
		if _, err := os.Stat(planPath); err != nil {
			return fmt.Errorf("plan.md not found at %s: %w", planPath, err)
		}

		// Transition to staging phase (no-op if re-entering from PhaseStaging).
		bs.Phase = build.PhaseStaging
		if err := build.SaveState(cfg.RunsDir, bs); err != nil {
			return fmt.Errorf("save staging state: %w", err)
		}

		fmt.Printf("[%s] approve-plan: deriving stages from %s…\n", candidate.ID, planPath)

		// Run stage derivation — skip if stages.md already exists (crash-resume).
		stagesPath := filepath.Join(cfg.RunsDir, date, candidate.ID, "stages.md")
		if _, statErr := os.Stat(stagesPath); statErr != nil {
			stagesCost, err := build.DeriveStages(cfg, candidate.ID, planPath, stagesPath, candidate.IssueComments)
			if err != nil {
				bs.Phase = build.PhaseBlocked
				bs.ErrorMsg = "stage derivation failed: " + err.Error()
				_ = build.SaveState(cfg.RunsDir, bs)
				return fmt.Errorf("derive stages: %w", err)
			}
			bs.CostUSD += stagesCost
		} else {
			fmt.Printf("[%s] approve-plan: stages.md already exists — skipping derivation\n", candidate.ID)
		}

		// Append the fixed documentation stage.
		if err := build.AppendDocStage(stagesPath); err != nil {
			bs.Phase = build.PhaseBlocked
			bs.ErrorMsg = "append doc stage failed: " + err.Error()
			_ = build.SaveState(cfg.RunsDir, bs)
			return fmt.Errorf("append doc stage: %w", err)
		}

		// Transition to stages_review phase.
		bs.Phase = build.PhaseStagesReview
		if err := build.SaveState(cfg.RunsDir, bs); err != nil {
			return fmt.Errorf("save stages-review state: %w", err)
		}

		fmt.Printf("[%s] approve-plan: stages written to %s\n", candidate.ID, stagesPath)
		fmt.Printf("[%s] approve-plan: review stages.md then run `extctl approve-stages %s` to continue\n", candidate.ID, candidate.ID)
		return nil
	},
}

// --- approve-stages command ---

var approveStagesCmd = &cobra.Command{
	Use:   "approve-stages <candidate-id>",
	Short: "Approve the stages and run the per-stage build loop",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		candidateID := args[0]

		// Look up the candidate from slates.
		slates, err := state.LoadAll(cfg.RunsDir)
		if err != nil {
			return fmt.Errorf("load slates: %w", err)
		}
		var (
			candidate *state.Candidate
			date      string
		)
		for i := len(slates) - 1; i >= 0; i-- {
			for j := range slates[i].Candidates {
				c := &slates[i].Candidates[j]
				if c.ID == candidateID || c.JiraKey == candidateID {
					candidate = c
					break
				}
			}
			if candidate != nil {
				break
			}
		}
		if candidate == nil {
			return fmt.Errorf("candidate %q not found in any slate", candidateID)
		}

		// Load build state — scan all dates because a picked candidate may have
		// been carried over into a newer slate, making slates[i].Date wrong.
		bs, err := build.FindState(cfg.RunsDir, candidate.ID)
		if err != nil {
			return fmt.Errorf("load build state: %w", err)
		}
		if bs == nil {
			return fmt.Errorf("candidate %s has no build state", candidate.ID)
		}
		if bs.Phase != build.PhaseStagesReview &&
			bs.Phase != build.PhaseBuilding &&
			bs.Phase != build.PhaseGating &&
			bs.Phase != build.PhaseRepairing {
			return fmt.Errorf("candidate %s is not in stages_review phase (current: %s)", candidate.ID, bs.Phase)
		}
		date = bs.Date

		// Check stages.md exists.
		stagesPath := filepath.Join(cfg.RunsDir, date, candidate.ID, "stages.md")
		if _, err := os.Stat(stagesPath); err != nil {
			return fmt.Errorf("stages.md not found at %s: %w", stagesPath, err)
		}

		// Parse stages to get total count.
		stages, err := build.ParseStages(stagesPath)
		if err != nil {
			return fmt.Errorf("parse stages: %w", err)
		}
		if len(stages) == 0 {
			return fmt.Errorf("stages.md contains no stages")
		}

		planPath := filepath.Join(cfg.RunsDir, date, candidate.ID, "plan.md")
		worktreePath := filepath.Join(cfg.RunsDir, date, candidate.ID, "worktree")

		// Capture entry phase and stage before we overwrite bs.Phase below.
		// Used in the per-stage loop to skip BuildStage when resuming from a
		// gate/repair crash that already produced a Claude session.
		entryPhase := bs.Phase
		resumeStage := bs.CurrentStage

		// Transition to building phase, set stage counters.
		bs.Phase = build.PhaseBuilding
		bs.TotalStages = len(stages)
		if bs.CurrentStage < 1 {
			bs.CurrentStage = 1
		}
		if err := build.SaveState(cfg.RunsDir, bs); err != nil {
			return fmt.Errorf("save building state: %w", err)
		}

		// Resolve gate script path.
		absGateScript, err := filepath.Abs("gate/run-gate.sh")
		if err != nil {
			return fmt.Errorf("resolve gate script path: %w", err)
		}
		outputDir := filepath.Join(cfg.RunsDir, date, candidate.ID)

		// stageSessionID holds the Claude session for the current stage only.
		// It is used to resume the session for repair attempts within the same
		// stage, but is cleared at the start of each new stage so that stages
		// never inherit each other's conversation history.
		// On crash recovery into PhaseGating/PhaseRepairing the prior session
		// is restored so repair can resume it.
		stageSessionID := ""
		if entryPhase == build.PhaseGating || entryPhase == build.PhaseRepairing {
			stageSessionID = bs.SessionID
		}

		// Per-stage build loop.
		maxRepairs := cfg.Claude.MaxRepairAttempts
		if maxRepairs < 1 {
			maxRepairs = 1
		}

		// Pre-flight: ensure oCIS is running before the build loop so the e2e
		// stage never fails simply because the stack wasn't started.
		if cfg.TargetRepo.Checkout != "" {
			fmt.Printf("[%s] pre-flight: ensuring oCIS is running in %s…\n", candidate.ID, cfg.TargetRepo.Checkout)
			if err := gate.EnsureOCIS(cfg.TargetRepo.Checkout, candidate.ID); err != nil {
				return fmt.Errorf("ocis pre-flight: %w", err)
			}
		}

		// Scaffold is an orchestrator action: create the package skeleton before
		// handing off to Claude's stage loop. Idempotent — skipped on crash-resume.
		if !bs.ScaffoldDone {
			fmt.Printf("[%s] scaffold: creating package skeleton…\n", candidate.ID)
			if err := build.ScaffoldExtension(build.ScaffoldOptions{
				Config:       cfg,
				CandidateID:  candidate.ID,
				Title:        candidate.Title,
				Description:  candidateDescription(candidate),
				WorktreePath: worktreePath,
				LogPrefix:    "[" + candidate.ID + "] ",
			}); err != nil {
				return fmt.Errorf("scaffold: %w", err)
			}
			bs.ScaffoldDone = true
			if err := build.SaveState(cfg.RunsDir, bs); err != nil {
				return fmt.Errorf("save state after scaffold: %w", err)
			}
		}

		for i, stageDesc := range stages {
			stageNum := i + 1
			if stageNum < bs.CurrentStage {
				fmt.Printf("[%s] stage %d/%d: already done — skipping\n", candidate.ID, stageNum, len(stages))
				continue
			}

			// New stage: clear the session so this stage starts fresh.
			if stageNum > resumeStage {
				stageSessionID = ""
				bs.SessionID = ""
			}

			fmt.Printf("[%s] building stage %d/%d: %s\n", candidate.ID, stageNum, len(stages), stageDesc)

			// Only run BuildStage if we're not resuming from a gate/repair crash
			// at this specific stage. If the crash happened after BuildStage produced
			// a commit but before the gate completed, the session is already in
			// bs.SessionID — skip straight to the gate to avoid duplicate commits.
			skipBuild := (stageNum == resumeStage) &&
				(entryPhase == build.PhaseGating || entryPhase == build.PhaseRepairing)

			if !skipBuild {
				// Summarise files committed by prior stages so Claude has
				// context without replaying the full conversation history.
				priorWork, _ := build.PriorStagesSummary(worktreePath, stageNum-1)

				result, err := build.BuildStage(build.StageOptions{
					Config:        cfg,
					CandidateID:   candidate.ID,
					Title:         candidate.Title,
					Effort:        candidate.Effort,
					SpecMD:        candidate.SpecMD,
					IssueComments: candidate.IssueComments,
					PlanPath:      planPath,
					StagesPath:    stagesPath,
					StageNum:      stageNum,
					TotalStages:   len(stages),
					StageDesc:     stageDesc,
					WorktreePath:  worktreePath,
					Date:          date,
					PriorWork:     priorWork,
				})
				if err != nil {
					bs.Phase = build.PhaseBlocked
					bs.ErrorMsg = err.Error()
					_ = build.SaveState(cfg.RunsDir, bs)
					return fmt.Errorf("stage %d: %w", stageNum, err)
				}

				stageSessionID = result.SessionID
				bs.SessionID = stageSessionID
				bs.CostUSD += result.CostUSD
				bs.Turns += result.Turns
			}

			// Run gate after each stage.
			bs.Phase = build.PhaseGating
			_ = build.SaveState(cfg.RunsDir, bs)

			bulletCount := countSpecBullets(candidate.SpecMD)
			gateResult, gateErr := gate.Run(absGateScript, worktreePath, candidate.ID, outputDir, bulletCount, cfg.TargetRepo.Checkout, candidate.ID)
			if gateErr != nil {
				bs.Phase = build.PhaseBlocked
				bs.ErrorMsg = "gate error: " + gateErr.Error()
				_ = build.SaveState(cfg.RunsDir, bs)
				return fmt.Errorf("gate stage %d: %w", stageNum, gateErr)
			}
			bs.Gate = &build.GateResult{
				Passed: gateResult.Passed,
				Score:  gateResult.Score,
				Stages: build.GateStages{
					Hygiene: gateResult.Stages.Hygiene,
					Build:   gateResult.Stages.Build,
					Lint:    gateResult.Stages.Lint,
					Unit:    gateResult.Stages.Unit,
					E2E:     gateResult.Stages.E2E,
				},
			}

			// Repair loop if gate failed.
			repairAttempts := 0
			for !gateResult.Passed {
				if repairAttempts >= maxRepairs {
					bs.Phase = build.PhaseBlocked
					bs.ErrorMsg = fmt.Sprintf("gate failed after %d repair attempt(s) at stage %d", repairAttempts, stageNum)
					_ = build.SaveState(cfg.RunsDir, bs)
					return fmt.Errorf("stage %d: gate failed after %d repair attempt(s)", stageNum, repairAttempts)
				}

				repairAttempts++
				bs.Attempts++
				fmt.Printf("[%s] stage %d gate failed (repair attempt %d/%d)…\n", candidate.ID, stageNum, repairAttempts, maxRepairs)
				bs.Phase = build.PhaseRepairing
				_ = build.SaveState(cfg.RunsDir, bs)

				gateLog, _ := gate.ReadLog(outputDir)
				repairResult, repairErr := build.Repair(build.Options{
					Config:       cfg,
					CandidateID:  candidate.ID,
					JiraKey:      candidate.JiraKey,
					SpecMD:       candidate.SpecMD,
					Effort:       candidate.Effort,
					Date:         date,
					WorktreePath: worktreePath,
					LogPrefix:    "[" + candidate.ID + "] ",
				}, gateLog, stageSessionID, repairAttempts)
				if repairErr != nil {
					bs.Phase = build.PhaseBlocked
					bs.ErrorMsg = repairErr.Error()
					_ = build.SaveState(cfg.RunsDir, bs)
					return fmt.Errorf("stage %d repair attempt %d: %w", stageNum, repairAttempts, repairErr)
				}

				stageSessionID = repairResult.SessionID
				bs.SessionID = stageSessionID
				bs.CostUSD += repairResult.CostUSD
				bs.Turns += repairResult.Turns
				bs.Phase = build.PhaseGating
				_ = build.SaveState(cfg.RunsDir, bs)

				gateResult, gateErr = gate.Run(absGateScript, worktreePath, candidate.ID, outputDir, bulletCount, cfg.TargetRepo.Checkout, candidate.ID)
				if gateErr != nil {
					bs.Phase = build.PhaseBlocked
					bs.ErrorMsg = "gate error after repair: " + gateErr.Error()
					_ = build.SaveState(cfg.RunsDir, bs)
					return fmt.Errorf("gate after stage %d repair: %w", stageNum, gateErr)
				}
				bs.Gate = &build.GateResult{
					Passed: gateResult.Passed,
					Score:  gateResult.Score,
					Stages: build.GateStages{
						Hygiene: gateResult.Stages.Hygiene,
						Build:   gateResult.Stages.Build,
						Lint:    gateResult.Stages.Lint,
						Unit:    gateResult.Stages.Unit,
						E2E:     gateResult.Stages.E2E,
					},
				}
			}

			// Gate passed — mark stage done.
			if err := build.CheckStage(stagesPath, stageNum); err != nil {
				return fmt.Errorf("check stage %d: %w", stageNum, err)
			}
			bs.CurrentStage = stageNum + 1
			bs.Phase = build.PhaseBuilding
			_ = build.SaveState(cfg.RunsDir, bs)

			fmt.Printf("[%s] stage %d/%d passed gate (score %.2f)\n", candidate.ID, stageNum, len(stages), gateResult.Score)
		}

		// All stages passed — publish.
		bs.Phase = build.PhaseGated
		_ = build.SaveState(cfg.RunsDir, bs)

		fmt.Printf("[%s] all %d stages passed — publishing…\n", candidate.ID, len(stages))

		// Push branch.
		bs.Phase = build.PhasePublishing
		_ = build.SaveState(cfg.RunsDir, bs)

		if mediaResult, mediaErr := media.Generate(cfg, worktreePath, candidate.ID,
			filepath.Join(cfg.RunsDir, date, candidate.ID, "media")); mediaErr != nil {
			fmt.Printf("[%s] warning: %v\n", candidate.ID, mediaErr)
		} else if mediaResult != nil {
			fmt.Printf("[%s] saved demo media (%d screenshot(s), video=%t)\n",
				candidate.ID, len(mediaResult.Screenshots), mediaResult.VideoPath != "")
		}

		fmt.Printf("[%s] wiring into docker-compose, CI, and oCIS config…\n", candidate.ID)
		if err := build.WireExtension(worktreePath, candidate.ID); err != nil {
			return fmt.Errorf("wire extension: %w", err)
		}

		fmt.Printf("[%s] pushing branch %s…\n", candidate.ID, bs.Branch)
		if err := gitpkg.PushBranch(cfg.TargetRepo.Checkout, bs.Branch); err != nil {
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
			Config:      cfg,
			CandidateID: candidate.ID,
			Date:        date,
			SpecMD:      candidate.SpecMD,
			OutputDir:   outputDir,
			TotalStages: len(stages),
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
		})

		fmt.Printf("[%s] opening PR on %s…\n", candidate.ID, cfg.TargetRepo.Remote)
		pr, err := githubpkg.Create(githubpkg.PROptions{
			RepoSlug: cfg.TargetRepo.Remote,
			Branch:   bs.Branch,
			Title:    fmt.Sprintf("feat(%s): add %s", candidate.ID, candidate.Title),
			Body:     prBody,
			Labels:   []string{},
			Draft:    false,
		})
		if err != nil {
			return fmt.Errorf("create PR: %w", err)
		}

		bs.PR = &build.PRResult{Number: pr.Number, URL: pr.URL, Ready: true}
		bs.Phase = build.PhaseDone
		_ = build.SaveState(cfg.RunsDir, bs)

		// Comment on the Jira issue with the PR link.
		jiraToken, jiraErr := config.JiraToken()
		jiraEmail, jiraEmailErr := config.JiraEmail()
		if jiraErr == nil && jiraEmailErr == nil && candidate.JiraKey != "" {
			jiraClient := jira.NewClient(cfg.Jira.BaseURL, jiraEmail, jiraToken)
			comment := fmt.Sprintf("PR opened: %s\n\nGate score: %.2f", pr.URL, gateScore)
			if addErr := jiraClient.AddComment(candidate.JiraKey, comment); addErr != nil {
				fmt.Printf("[%s] warning: could not comment on Jira issue: %v\n", candidate.ID, addErr)
			}
		}

		// Clean up worktree on success.
		if err := gitpkg.RemoveWorktree(cfg.TargetRepo.Checkout, worktreePath); err != nil {
			fmt.Printf("[%s] warning: could not remove worktree: %v\n", candidate.ID, err)
		}

		fmt.Printf("[%s] done — PR #%d: %s\n", candidate.ID, pr.Number, pr.URL)
		return nil
	},
}

// candidateDescription returns the candidate's parsed problem statement,
// falling back to its title for slates persisted before Description existed.
func candidateDescription(c *state.Candidate) string {
	if c.Description != "" {
		return c.Description
	}
	return c.Title
}

// countSpecBullets counts bullet lines in specMD for gate scoring.
func countSpecBullets(specMD string) int {
	n := 0
	for _, line := range strings.Split(specMD, "\n") {
		trimmed := strings.TrimLeft(line, " \t")
		if len(trimmed) > 0 && (trimmed[0] == '-' || trimmed[0] == '*') {
			n++
		}
	}
	if n < 1 {
		return 1
	}
	return n
}

// --- stats command ---

var statsDays int

var statsCmd = &cobra.Command{
	Use:   "stats",
	Short: "Show pipeline stats and cost summary",
	RunE: func(cmd *cobra.Command, args []string) error {
		r, err := stats.Compute(cfg, statsDays)
		if err != nil {
			return err
		}
		stats.Print(r, os.Stdout)
		return nil
	},
}

// --- release command ---

var releaseDryRun bool

var releaseCmd = &cobra.Command{
	Use:   "release",
	Short: "Tag merged-but-unreleased extensions for GitHub release",
	Long: `Scan the web-extensions checkout for extensions that have been merged to
the default branch but never released, and create + push a signed git tag
(<app-id>-v<version>) for each. The GitHub Action in web-extensions picks up
the pushed tag and builds the release.

An extension is considered released once any tag with prefix <app-id>-v exists,
so this command is idempotent. Signed tags require a signing key configured in
the target repo's git (user.signingkey).`,
	RunE: func(cmd *cobra.Command, args []string) error {
		return release.Run(cfg, releaseDryRun, os.Stdout)
	},
}

// --- publish command ---

var (
	publishDryRun bool
	publishID     string
	publishForce  bool
)

var publishCmd = &cobra.Command{
	Use:   "publish",
	Short: "Stage marketplace submissions for review (see also: approve, retry-screenshots)",
	Long: `Scan owncloud/web-extensions GitHub Releases for extensions with a
completed release that is not yet present in owncloud/marketplace, and for
each one: download its release bundle, generate extension.yaml, have Claude
write a fresh dedicated screenshot spec (never a reuse of acceptance.spec.ts,
which optimizes for passing assertions, not looking good publicly) and
capture screenshots from it via a live oCIS + Playwright run (best-effort —
a submission still goes out without screenshots if capture fails), and
commit the assembled submission to a local publish/<app-id>-v<version>
branch under extensions/<app-id>/releases/<version>/ — but does NOT push or
open a PR yet. Review the printed summary (open the screenshots it points
at), then run:

  extctl publish approve <app-id>            push the branch and open its PR
  extctl publish retry-screenshots <app-id>   recapture screenshots and retry

An extension whose package.json has no license field is skipped with a
clear error rather than published with a guessed license. Tags and minOCIS
are reused verbatim from this extension's most recent prior marketplace
release when one exists; otherwise tags fall back to Claude inference from
package.json/README (staged with no tags at all, flagged in the summary, if
that also fails), and minOCIS falls back to the latest stable oCIS release
that existed on or before the extension's first commit date (a heuristic,
flagged in the summary; left unset if that turns up nothing either).

An extension already staged locally (publish/<app-id>-v<version> exists but
has no open PR yet) is skipped by default — use "approve" or
"retry-screenshots" on it instead. Use --force with --id to discard that
staged branch and redo the download/screenshots/tag+minOCIS resolution from
scratch. A branch a prior run created but never finished committing to
(e.g. it crashed mid-download) is always re-staged automatically, with or
without --force.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		summary, err := marketplace.Run(marketplace.Options{
			Config: cfg,
			DryRun: publishDryRun,
			OnlyID: publishID,
			Force:  publishForce,
		}, os.Stdout)
		if summary != nil {
			for _, f := range summary.Failed {
				fmt.Printf("  ✗ %s@%s: %v\n", f.AppID, f.Version, f.Err)
			}
		}
		return err
	},
}

var publishApproveCmd = &cobra.Command{
	Use:   "approve <app-id>[@<version>]",
	Short: "Push a staged submission and open its marketplace PR",
	Long: `Push the local publish/<app-id>-v<version> branch a prior "extctl
publish" run committed, and open its PR against owncloud/marketplace.
<app-id> alone works if exactly one version is staged for it; use
<app-id>@<version> to disambiguate if more than one is pending. Idempotent —
if the branch already has an open PR, prints its URL instead of erroring or
opening a duplicate.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		prURL, err := marketplace.Approve(cfg, args[0], os.Stdout)
		if err != nil {
			return err
		}
		fmt.Println(prURL)
		return nil
	},
}

var publishRetryScreenshotsCmd = &cobra.Command{
	Use:   "retry-screenshots <app-id>[@<version>]",
	Short: "Regenerate and recapture screenshots for a staged submission",
	Long: `Have Claude write a fresh screenshot spec (not a retry of the same
one — observed capture failures have traced to content bugs in the
generated spec, not environmental flakiness a same-spec retry would fix)
and recapture screenshots for a submission a prior "extctl publish" run
staged, reusing its already-downloaded bundle and already-resolved tags/
minOCIS — no re-download, no new branch. The result is amended onto the
existing commit. <app-id> alone works if exactly one version is staged for
it; use <app-id>@<version> to disambiguate.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return marketplace.RetryScreenshots(cfg, args[0], os.Stdout)
	},
}

// --- doctor command ---

var doctorCmd = &cobra.Command{
	Use:   "doctor",
	Short: "Check the health of the local extctl installation",
	Long: `doctor checks extctl.yaml for validity and unsupported keys, required
secrets, required external tooling (git, gh, claude, docker, pnpm — plus
ffmpeg if media capture is enabled), referenced prompt/idea-pool/scaffold
paths, and the shape of the target repo checkout. It makes no network calls
and never mutates anything (it does not run "gh repo clone" or touch the
checkout) — safe to run at any time, including with a missing or broken
extctl.yaml.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		report := doctor.Run(cfgFile)
		doctor.Print(report, os.Stdout)
		if report.HasErrors() {
			os.Exit(1)
		}
		return nil
	},
}

// --- version command ---

var version = "dev"

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print extctl version",
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Printf("extctl %s\n", version)
	},
}

func init() {
	rootCmd.PersistentFlags().StringVar(&cfgFile, "config", "extctl.yaml",
		"config file (default: extctl.yaml in current directory)")

	genCmd.Flags().BoolVar(&genDryRun, "dry-run", false,
		"print the prompt that would be sent without calling claude or creating issues")
	genCmd.Flags().BoolVar(&genSkipJira, "skip-jira", false,
		"run claude and show parsed candidates but do not create Jira issues or write slate")
	genCmd.Flags().BoolVar(&genNoReview, "no-review", false,
		"skip interactive review and push all generated candidates directly to Jira (use for automation)")
	genCmd.Flags().StringVar(&genFromFile, "from-file", "",
		"skip claude and read candidates from an existing specgen.json (e.g. runs/2026-06-18/specgen.json)")
	genCmd.Flags().StringVar(&genModel, "model", "",
		"claude model to use (e.g. claude-opus-4-6); defaults to claude's own default")
	genCmd.Flags().StringVar(&genDate, "date", "",
		"date to generate for in YYYY-MM-DD format (default: today)")

	slateCarryoversCmd.Flags().String("format", "", "output format: dedup-hint")

	pollCmd.Flags().BoolVar(&pollDryRun, "dry-run", false,
		"print what would happen without touching Jira or state")
	pollCmd.Flags().StringVar(&pollDate, "date", "",
		"date to poll for in YYYY-MM-DD format (default: today)")

	releaseCmd.Flags().BoolVar(&releaseDryRun, "dry-run", false,
		"list merged-but-unreleased extensions without creating or pushing tags")

	publishCmd.Flags().BoolVar(&publishDryRun, "dry-run", false,
		"list extensions that would be staged without downloading, capturing screenshots, or committing anything")
	publishCmd.Flags().StringVar(&publishID, "id", "",
		"stage only this extension (app-id without the web-app- prefix, e.g. draw-io)")
	publishCmd.Flags().BoolVar(&publishForce, "force", false,
		"discard an already-staged local branch and re-stage from scratch (requires --id)")

	statsCmd.Flags().IntVar(&statsDays, "days", 30,
		"number of days of history to include in pipeline and cost sections")

	slateCmd.AddCommand(slateStatusCmd, slateCarryoversCmd)
	publishCmd.AddCommand(publishApproveCmd, publishRetryScreenshotsCmd)
	rootCmd.AddCommand(genCmd, slateCmd, pollCmd, gateCmd, approvePlanCmd, approveStagesCmd, releaseCmd, publishCmd, statsCmd, versionCmd, doctorCmd)
}

func main() {
	if err := config.LoadDotEnv(".env"); err != nil {
		fmt.Fprintln(os.Stderr, "warning:", err)
	}
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}
