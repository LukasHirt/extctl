package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"path/filepath"
	"time"

	"github.com/LukasHirt/extctl/internal/build"
	"github.com/LukasHirt/extctl/internal/config"
	"github.com/LukasHirt/extctl/internal/gate"
	"github.com/LukasHirt/extctl/internal/gen"
	gitpkg "github.com/LukasHirt/extctl/internal/git"
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

// --- build command ---

var buildCmd = &cobra.Command{
	Use:   "build <candidate-id>",
	Short: "Force-build a picked candidate (worktree → claude → gate → PR)",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		candidateID := args[0]

		loc, err := time.LoadLocation(cfg.Timezone)
		if err != nil {
			return fmt.Errorf("load timezone: %w", err)
		}
		date := time.Now().In(loc).Format("2006-01-02")

		// Look up the candidate from the latest slate.
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

		jiraToken, err := config.JiraToken()
		if err != nil {
			return err
		}
		jiraEmail, err := config.JiraEmail()
		if err != nil {
			return err
		}

		branch := fmt.Sprintf("ext/%s-%s", date, candidate.ID)
		worktreePath := filepath.Join(cfg.RunsDir, date, candidate.ID, "worktree")

		if err := gitpkg.FetchOrigin(cfg.TargetRepo.Checkout); err != nil {
			return fmt.Errorf("git fetch: %w", err)
		}
		baseBranch := "origin/" + cfg.DefaultBranch
		if err := gitpkg.CreateWorktree(cfg.TargetRepo.Checkout, worktreePath, branch, baseBranch); err != nil {
			return fmt.Errorf("create worktree: %w", err)
		}

		result, err := build.Run(build.Options{
			Config:       cfg,
			CandidateID:  candidate.ID,
			JiraKey:      candidate.JiraKey,
			SpecMD:       candidate.SpecMD,
			Effort:       candidate.Effort,
			Date:         date,
			WorktreePath: worktreePath,
		})
		if err != nil {
			return err
		}

		// Use the same jira client that poll.Run would use (just for the build cmd).
		_ = jiraToken
		_ = jiraEmail
		fmt.Printf("\nbuild done: cost $%.4f · turns %d · session %s\n",
			result.CostUSD, result.Turns, result.SessionID)
		fmt.Printf("result: %s\n",
			filepath.Join(cfg.RunsDir, date, candidate.ID, "result.json"))
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
	rootCmd.AddCommand(genCmd, slateCmd, pollCmd, buildCmd, gateCmd, scaffoldCmd, versionCmd)
}

func main() {
	if err := config.LoadDotEnv(".env"); err != nil {
		fmt.Fprintln(os.Stderr, "warning:", err)
	}
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}
