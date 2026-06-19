package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/LukasHirt/extctl/internal/build"
	"github.com/LukasHirt/extctl/internal/claude"
	"github.com/LukasHirt/extctl/internal/config"
	"github.com/LukasHirt/extctl/internal/gate"
	"github.com/LukasHirt/extctl/internal/gen"
	gitpkg "github.com/LukasHirt/extctl/internal/git"
	githubpkg "github.com/LukasHirt/extctl/internal/github"
	"github.com/LukasHirt/extctl/internal/jira"
	"github.com/LukasHirt/extctl/internal/poll"
	scaffoldpkg "github.com/LukasHirt/extctl/internal/scaffold"
	"github.com/LukasHirt/extctl/internal/state"
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
		// Skip config loading for commands that don't need it.
		if cmd.Name() == "version" || cmd.Name() == "help" {
			return nil
		}
		var err error
		cfg, err = config.Load(cfgFile)
		if err != nil {
			return fmt.Errorf("load config: %w", err)
		}
		return nil
	},
}

// --- gen command ---

var (
	genDryRun   bool
	genSkipJira bool
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

// --- scaffold command ---

var scaffoldCmd = &cobra.Command{
	Use:   "scaffold",
	Short: "Manage the extension scaffold template",
}

var scaffoldFetchCmd = &cobra.Command{
	Use:     "fetch",
	Aliases: []string{"init"},
	Short:   "Fetch (or refresh) the scaffold from the skeleton repository",
	Long: `Clones the configured skeleton repository, strips .git/, applies the
exclusion list, and copies the result into the scaffold directory.
Files already in scaffold/ that are not present in the skeleton (e.g.
src/composables/useLLM.ts, tests/e2e/) are left untouched.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		return scaffoldpkg.Fetch(scaffoldpkg.FetchOptions{
			Source:  cfg.Scaffold.Source,
			Exclude: cfg.Scaffold.Exclude,
			DestDir: cfg.ScaffoldDir,
		})
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

		result, err := gate.Run(scriptPath, worktreePath, candidateID, outputDir, 1)
		if err != nil {
			return err
		}
		if result.Passed {
			fmt.Printf("gate PASSED (score %.2f)\n", result.Score)
		} else {
			fmt.Printf("gate FAILED\n")
			fmt.Printf("stages: hygiene=%s build=%s lint=%s unit=%s\n",
				result.Stages.Hygiene, result.Stages.Build,
				result.Stages.Lint, result.Stages.Unit)
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

		loc, err := time.LoadLocation(cfg.Timezone)
		if err != nil {
			return fmt.Errorf("load timezone: %w", err)
		}
		date := time.Now().In(loc).Format("2006-01-02")

		// Look up the candidate from slates.
		slates, err := state.LoadAll(cfg.RunsDir)
		if err != nil {
			return fmt.Errorf("load slates: %w", err)
		}
		var candidate *state.Candidate
		for i := len(slates) - 1; i >= 0; i-- {
			for j := range slates[i].Candidates {
				c := &slates[i].Candidates[j]
				if c.ID == candidateID || c.JiraKey == candidateID {
					candidate = c
					date = slates[i].Date
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

		// Load build state.
		bs, err := build.LoadState(cfg.RunsDir, date, candidate.ID)
		if err != nil {
			return fmt.Errorf("load build state: %w", err)
		}
		if bs == nil {
			return fmt.Errorf("candidate %s has no build state — run `extctl poll` or `extctl build %s` first", candidate.ID, candidate.ID)
		}
		if bs.Phase != build.PhasePlanReview && bs.Phase != build.PhaseStaging {
			return fmt.Errorf("candidate %s is not in plan_review phase (current: %s)", candidate.ID, bs.Phase)
		}

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
			if err := build.DeriveStages(cfg, candidate.ID, planPath, stagesPath, candidate.IssueComments); err != nil {
				bs.Phase = build.PhaseBlocked
				bs.ErrorMsg = "stage derivation failed: " + err.Error()
				_ = build.SaveState(cfg.RunsDir, bs)
				return fmt.Errorf("derive stages: %w", err)
			}
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

		loc, err := time.LoadLocation(cfg.Timezone)
		if err != nil {
			return fmt.Errorf("load timezone: %w", err)
		}
		date := time.Now().In(loc).Format("2006-01-02")

		// Look up the candidate from slates.
		slates, err := state.LoadAll(cfg.RunsDir)
		if err != nil {
			return fmt.Errorf("load slates: %w", err)
		}
		var candidate *state.Candidate
		for i := len(slates) - 1; i >= 0; i-- {
			for j := range slates[i].Candidates {
				c := &slates[i].Candidates[j]
				if c.ID == candidateID || c.JiraKey == candidateID {
					candidate = c
					date = slates[i].Date
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

		// Load build state.
		bs, err := build.LoadState(cfg.RunsDir, date, candidate.ID)
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
			gateResult, gateErr := gate.Run(absGateScript, worktreePath, candidate.ID, outputDir, bulletCount)
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
				}, gateLog, stageSessionID)
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

				gateResult, gateErr = gate.Run(absGateScript, worktreePath, candidate.ID, outputDir, bulletCount)
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

		fmt.Printf("[%s] pushing branch %s…\n", candidate.ID, bs.Branch)
		if err := gitpkg.PushBranch(cfg.TargetRepo.Checkout, bs.Branch); err != nil {
			return fmt.Errorf("push branch: %w", err)
		}

		gateScore := 0.0
		if bs.Gate != nil {
			gateScore = bs.Gate.Score
		}
		whatWasBuilt := ""
		lastStageJSONL := filepath.Join(outputDir, fmt.Sprintf("stage-%d.jsonl", len(stages)))
		if r, loadErr := claude.LoadResult(lastStageJSONL); loadErr == nil {
			whatWasBuilt = r.Result
		}
		prBody := githubpkg.FormatBody(
			candidate.SpecMD,
			whatWasBuilt,
			candidate.JiraKey,
			gateScore,
			bs.CostUSD,
			bs.Turns,
			bs.Attempts,
		)

		fmt.Printf("[%s] opening PR on %s…\n", candidate.ID, cfg.TargetRepo.Remote)
		pr, err := githubpkg.Create(githubpkg.PROptions{
			RepoSlug: cfg.TargetRepo.Remote,
			Branch:   bs.Branch,
			Title:    candidate.Title,
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
			comment := fmt.Sprintf("PR opened: %s\n\nGate score: %.2f | Cost: $%.2f | Turns: %d | Attempts: %d",
				pr.URL, gateScore, bs.CostUSD, bs.Turns, bs.Attempts)
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

	slateCmd.AddCommand(slateStatusCmd, slateCarryoversCmd)
	scaffoldCmd.AddCommand(scaffoldFetchCmd)
	rootCmd.AddCommand(genCmd, slateCmd, pollCmd, gateCmd, scaffoldCmd, approvePlanCmd, approveStagesCmd, versionCmd)
}

func main() {
	if err := config.LoadDotEnv(".env"); err != nil {
		fmt.Fprintln(os.Stderr, "warning:", err)
	}
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}
