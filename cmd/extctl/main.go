package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/LukasHirt/extctl/internal/config"
	"github.com/LukasHirt/extctl/internal/gen"
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
	genDryRun bool
	genModel  string
	genDate   string
)

var genCmd = &cobra.Command{
	Use:   "gen",
	Short: "Generate today's 3 fresh agentic extension specs and create Jira issues",
	RunE: func(cmd *cobra.Command, args []string) error {
		result, err := gen.Run(gen.Options{
			Config: cfg,
			DryRun: genDryRun,
			Date:   genDate,
			Model:  genModel,
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
	genCmd.Flags().StringVar(&genModel, "model", "",
		"claude model to use (e.g. claude-opus-4-6); defaults to claude's own default")
	genCmd.Flags().StringVar(&genDate, "date", "",
		"date to generate for in YYYY-MM-DD format (default: today)")

	slateCarryoversCmd.Flags().String("format", "", "output format: dedup-hint")

	slateCarryoversCmd.Flags().String("format", "", "output format: dedup-hint")

	slateCmd.AddCommand(slateStatusCmd, slateCarryoversCmd)
	rootCmd.AddCommand(genCmd, slateCmd, versionCmd)
}

func main() {
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}
