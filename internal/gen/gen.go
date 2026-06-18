package gen

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/LukasHirt/extctl/internal/claude"
	"github.com/LukasHirt/extctl/internal/config"
	"github.com/LukasHirt/extctl/internal/jira"
	"github.com/LukasHirt/extctl/internal/state"
)

// Options controls a single gen run.
type Options struct {
	Config   *config.Config
	DryRun   bool   // if true, print what would happen but don't call claude or create issues
	SkipJira bool   // if true, run claude and parse candidates but don't create Jira issues
	Date     string // YYYY-MM-DD; defaults to today in the configured timezone
	Model    string // optional claude model override
}

// Result summarises what happened.
type Result struct {
	Date       string
	Carryovers []state.Candidate
	Fresh      []state.Candidate
	DryRun     bool
}

// Run executes the morning gen flow:
//  1. Load existing slates to derive carryovers + delivered IDs.
//  2. Build the claude prompt with the dedup context.
//  3. Run claude -p (unless dry run).
//  4. Parse the 3 candidate blocks.
//  5. Create Jira issues for the 3 fresh candidates.
//  6. Write/update today's slate.json.
func Run(opts Options) (*Result, error) {
	date := opts.Date
	if date == "" {
		loc, err := time.LoadLocation(opts.Config.Timezone)
		if err != nil {
			return nil, fmt.Errorf("load timezone: %w", err)
		}
		date = time.Now().In(loc).Format("2006-01-02")
	}

	// 1. Load all previous slates.
	allSlates, err := state.LoadAll(opts.Config.RunsDir)
	if err != nil {
		return nil, fmt.Errorf("load slates: %w", err)
	}

	// Check idempotency: if today's slate already has 3 fresh candidates, skip.
	todaySlate, err := state.Load(opts.Config.RunsDir, date)
	if err != nil {
		return nil, fmt.Errorf("load today's slate: %w", err)
	}
	freshToday := 0
	if todaySlate != nil {
		for _, c := range todaySlate.Candidates {
			if c.Origin == "generated" || c.Origin == "manual" {
				freshToday++
			}
		}
	}
	if freshToday >= opts.Config.FreshCandidatesPerDay {
		fmt.Printf("Today's slate already has %d fresh candidates — nothing to generate.\n", freshToday)
		res := &Result{Date: date, DryRun: opts.DryRun}
		if todaySlate != nil {
			for _, c := range todaySlate.Candidates {
				if c.Origin == "carryover" {
					res.Carryovers = append(res.Carryovers, c)
				} else {
					res.Fresh = append(res.Fresh, c)
				}
			}
		}
		return res, nil
	}

	// 2. Derive carryovers and delivered IDs.
	carryovers := state.Carryovers(allSlates, date, opts.Config.Decay.MaxAppearances)
	deliveredIDs := state.DeliveredIDs(allSlates)

	// 3. Build prompt.
	prompt, err := buildPrompt(opts.Config, carryovers, deliveredIDs)
	if err != nil {
		return nil, fmt.Errorf("build prompt: %w", err)
	}

	if opts.DryRun {
		fmt.Printf("=== DRY RUN: %s ===\n\n", date)
		fmt.Printf("Fresh candidates to generate: %d\n", opts.Config.FreshCandidatesPerDay)
		fmt.Printf("Claude working dir: %s\n", opts.Config.TargetRepo.Checkout)
		fmt.Printf("Max turns: %d\n\n", opts.Config.Claude.SpecGenMaxTurns)

		if len(carryovers) > 0 {
			fmt.Printf("Carryovers (%d):\n", len(carryovers))
			for _, c := range carryovers {
				fmt.Printf("  [%d/%d] %s — %s\n", c.Appearances, opts.Config.Decay.MaxAppearances, c.ID, c.Title)
			}
		} else {
			fmt.Println("Carryovers: none")
		}
		fmt.Println()

		if len(deliveredIDs) > 0 {
			fmt.Printf("Already delivered (dedup guard) (%d):\n", len(deliveredIDs))
			for id := range deliveredIDs {
				fmt.Printf("  %s\n", id)
			}
		} else {
			fmt.Println("Already delivered: none")
		}
		fmt.Println()

		fmt.Println("=== PROMPT THAT WOULD BE SENT TO CLAUDE ===")
		fmt.Println(prompt)
		fmt.Println("=== END PROMPT ===")
		return &Result{Date: date, Carryovers: carryovers, DryRun: true}, nil
	}

	// 4. Run claude.
	outputFile := filepath.Join(opts.Config.RunsDir, date, "specgen.json")
	claudeOpts := claude.RunOptions{
		Prompt:       prompt,
		AllowedTools: []string{"Read", "Grep", "Glob"},
		MaxTurns:     opts.Config.Claude.SpecGenMaxTurns,
		WorkDir:      opts.Config.TargetRepo.Checkout,
		OutputFile:   outputFile,
		Model:        opts.Model,
	}

	fmt.Printf("Running claude (max %d turns, working dir: %s)…\n",
		claudeOpts.MaxTurns, claudeOpts.WorkDir)

	result, err := claude.Run(claudeOpts)
	if err != nil {
		return nil, fmt.Errorf("claude run: %w", err)
	}

	fmt.Printf("Claude finished: %d turns, $%.4f\n", result.NumTurns, result.TotalCostUSD)

	// 5. Parse candidates.
	candidates, err := claude.ParseCandidates(result.Result)
	if err != nil {
		return nil, fmt.Errorf("parse candidates: %w\nresult saved to: %s", err, outputFile)
	}
	if len(candidates) == 0 {
		return nil, fmt.Errorf("claude returned no candidates; result saved to: %s", outputFile)
	}
	if len(candidates) != opts.Config.FreshCandidatesPerDay {
		fmt.Printf("warning: expected %d candidates, got %d — proceeding with what was returned\n",
			opts.Config.FreshCandidatesPerDay, len(candidates))
	}

	// Print parsed candidates regardless of --skip-jira.
	fmt.Println()
	for i, c := range candidates {
		fmt.Printf("Candidate %d: %s — %s (effort: %s)\n", i+1, c.ID, c.Title, c.Effort)
	}
	fmt.Println()

	if opts.SkipJira {
		fmt.Println("--skip-jira: Jira issues not created. Slate not written.")
		var fresh []state.Candidate
		for _, c := range candidates {
			fresh = append(fresh, state.Candidate{
				ID:     c.ID,
				Title:  c.Title,
				Effort: c.Effort,
				Origin: "generated",
			})
		}
		return &Result{Date: date, Carryovers: carryovers, Fresh: fresh}, nil
	}

	// 6. Create Jira issues.
	jiraToken, err := config.JiraToken()
	if err != nil {
		return nil, err
	}
	jiraEmail, err := config.JiraEmail()
	if err != nil {
		return nil, err
	}
	jiraClient := jira.NewClient(opts.Config.Jira.BaseURL, jiraEmail, jiraToken)

	var freshCandidates []state.Candidate
	for _, pc := range candidates {
		body := jira.FormatIssueBody(pc, 1, "generated")
		summary := jira.IssueSummary(pc.Title)

		fmt.Printf("Creating Jira issue: %s…\n", summary)
		issue, err := jiraClient.CreateIssue(jira.CreateIssueRequest{
			Project:     opts.Config.Jira.Project,
			Summary:     summary,
			Description: body,
			Labels:      []string{"ext-candidate", "claude-generated"},
		})
		if err != nil {
			return nil, fmt.Errorf("create issue for %s: %w", pc.ID, err)
		}

		// Transition to the candidate status (e.g. "Needs Approval").
		if err := jiraClient.Transition(issue.Key, opts.Config.Jira.CandidateStatus); err != nil {
			// Non-fatal: the issue exists, the transition just failed.
			fmt.Printf("warning: could not transition %s to %q: %v\n",
				issue.Key, opts.Config.Jira.CandidateStatus, err)
		}

		fmt.Printf("  ✓ %s — %s\n", issue.Key, issue.URL)

		freshCandidates = append(freshCandidates, state.Candidate{
			ID:          pc.ID,
			Title:       pc.Title,
			JiraKey:     issue.Key,
			JiraURL:     issue.URL,
			Status:      state.StatusNeedsApproval,
			Appearances: 1,
			Origin:      "generated",
			FirstDate:   date,
			Effort:      pc.Effort,
			SpecMD:      pc.Raw,
		})
	}

	// 7. Update carryover appearances and write today's slate.
	var updatedCarryovers []state.Candidate
	for _, c := range carryovers {
		c.Appearances++
		c.Origin = "carryover"
		updatedCarryovers = append(updatedCarryovers, c)
	}

	allCandidates := append(updatedCarryovers, freshCandidates...)
	slate := &state.Slate{
		Date:       date,
		Candidates: allCandidates,
		CreatedAt:  time.Now(),
	}
	if err := state.Save(opts.Config.RunsDir, slate); err != nil {
		return nil, fmt.Errorf("save slate: %w", err)
	}

	return &Result{
		Date:       date,
		Carryovers: updatedCarryovers,
		Fresh:      freshCandidates,
	}, nil
}

func buildPrompt(cfg *config.Config, carryovers []state.Candidate, deliveredIDs map[string]bool) (string, error) {
	// Load gen-specs.md and replace {{N}}.
	promptBytes, err := os.ReadFile(cfg.Prompts.GenSpecs)
	if err != nil {
		return "", fmt.Errorf("read gen-specs prompt %s: %w", cfg.Prompts.GenSpecs, err)
	}
	prompt := strings.ReplaceAll(string(promptBytes), "{{N}}", fmt.Sprintf("%d", cfg.FreshCandidatesPerDay))

	// Load idea pool.
	poolBytes, err := os.ReadFile(cfg.IdeaPool)
	if err != nil && !os.IsNotExist(err) {
		return "", fmt.Errorf("read idea pool %s: %w", cfg.IdeaPool, err)
	}
	poolSection := "Idea pool:\n(empty — generate net-new ideas grounded in the repo)"
	if len(poolBytes) > 0 {
		poolSection = "Idea pool:\n" + string(poolBytes)
	}
	prompt += "\n\n" + poolSection

	// Carryover dedup section.
	if len(carryovers) > 0 {
		var lines []string
		for _, c := range carryovers {
			lines = append(lines, fmt.Sprintf("- %s: %s (appearances: %d/%d)",
				c.ID, c.Title, c.Appearances, cfg.Decay.MaxAppearances))
		}
		prompt += "\n\nCarryover candidates already in today's slate " +
			"(do not duplicate or substantially overlap with any of these):\n" +
			strings.Join(lines, "\n")
	}

	// Delivered IDs dedup section.
	if len(deliveredIDs) > 0 {
		var lines []string
		for id := range deliveredIDs {
			lines = append(lines, "- "+id)
		}
		prompt += "\n\nAlready delivered extensions " +
			"(do not reproduce or substantially overlap):\n" +
			strings.Join(lines, "\n")
	}

	return prompt, nil
}
