package gen

import (
	"bufio"
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
	NoReview bool   // if true, skip interactive review and push all candidates to Jira directly
	FromFile string // if set, skip claude and read candidates from this specgen.json path
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

// rejectedSpec holds a candidate that was discarded during interactive review.
type rejectedSpec struct {
	Candidate claude.ParsedCandidate
	Reason    string
}

// Run executes the morning gen flow:
//  1. Load existing slates to derive carryovers + delivered IDs.
//  2. Build the claude prompt with the dedup context.
//  3. Run claude -p (unless dry run).
//  4. Parse the 3 candidate blocks.
//  5. Interactive review (unless --no-review or --skip-jira).
//  6. Create Jira issues for approved candidates.
//  7. Write/update today's slate.json.
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

	// Check idempotency: skip if today's review is already done, or if we already
	// have enough non-rejected fresh candidates.
	todaySlate, err := state.Load(opts.Config.RunsDir, date)
	if err != nil {
		return nil, fmt.Errorf("load today's slate: %w", err)
	}
	if todaySlate != nil {
		if todaySlate.ReviewDone {
			fmt.Printf("Today's slate review is already complete — nothing to generate.\n")
			res := &Result{Date: date, DryRun: opts.DryRun}
			for _, c := range todaySlate.Candidates {
				if c.Origin == "carryover" {
					res.Carryovers = append(res.Carryovers, c)
				} else if c.Status != state.StatusRejected {
					res.Fresh = append(res.Fresh, c)
				}
			}
			return res, nil
		}
		freshToday := 0
		for _, c := range todaySlate.Candidates {
			if (c.Origin == "generated" || c.Origin == "manual") && c.Status != state.StatusRejected {
				freshToday++
			}
		}
		if freshToday >= opts.Config.FreshCandidatesPerDay {
			fmt.Printf("Today's slate already has %d fresh candidates — nothing to generate.\n", freshToday)
			res := &Result{Date: date, DryRun: opts.DryRun}
			for _, c := range todaySlate.Candidates {
				if c.Origin == "carryover" {
					res.Carryovers = append(res.Carryovers, c)
				} else {
					res.Fresh = append(res.Fresh, c)
				}
			}
			return res, nil
		}
	}

	// 2. Derive carryovers and delivered IDs.
	carryovers := state.Carryovers(allSlates, date, opts.Config.Decay.MaxAppearances)
	deliveredYAML, err := state.LoadDelivered(opts.Config.DeliveredYAML)
	if err != nil {
		return nil, fmt.Errorf("load delivered.yaml: %w", err)
	}
	deliveredIDs := state.DeliveredIDs(allSlates, deliveredYAML)

	// 3. Build prompt.
	prompt, err := buildPrompt(opts.Config, carryovers, deliveredIDs)
	if err != nil {
		return nil, fmt.Errorf("build prompt: %w", err)
	}

	if opts.DryRun {
		fmt.Printf("=== DRY RUN: %s ===\n\n", date)
		fmt.Printf("Fresh candidates to generate: %d\n", opts.Config.FreshCandidatesPerDay)
		fmt.Printf("Claude working dir: %s\n\n", opts.Config.TargetRepo.Checkout)

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

	// 4. Run claude or load from existing file.
	outputFile := filepath.Join(opts.Config.RunsDir, date, "specgen.jsonl")

	var result *claude.Result
	if opts.FromFile != "" {
		fmt.Printf("Loading candidates from %s…\n", opts.FromFile)
		r, err := claude.LoadResult(opts.FromFile)
		if err != nil {
			return nil, fmt.Errorf("--from-file: %w", err)
		}
		result = r
		if abs, _ := filepath.Abs(opts.FromFile); abs != outputFile {
			data, err := os.ReadFile(opts.FromFile)
			if err != nil {
				return nil, fmt.Errorf("read --from-file for copy: %w", err)
			}
			if err := os.MkdirAll(filepath.Dir(outputFile), 0o755); err != nil {
				return nil, fmt.Errorf("mkdir for output file: %w", err)
			}
			if err := os.WriteFile(outputFile, data, 0o644); err != nil {
				return nil, fmt.Errorf("copy specgen.jsonl: %w", err)
			}
		}
		fmt.Printf("Loaded: %d turns, $%.4f\n", result.NumTurns, result.TotalCostUSD)
	} else {
		claudeOpts := claude.RunOptions{
			Prompt:       prompt,
			AllowedTools: []string{"Read", "Grep", "Glob"},
			WorkDir:      opts.Config.TargetRepo.Checkout,
			OutputFile:   outputFile,
			Model:        opts.Model,
		}

		fmt.Printf("Running claude (working dir: %s)…\n", claudeOpts.WorkDir)

		result, err = claude.Run(claudeOpts)
		if err != nil {
			return nil, fmt.Errorf("claude run: %w", err)
		}
		fmt.Printf("Claude finished: %d turns, $%.4f\n", result.NumTurns, result.TotalCostUSD)
	}

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

	// 6. Interactive review loop (unless --no-review).
	var allRejected []rejectedSpec
	if !opts.NoReview {
		extraDelivered := map[string]bool{}
		pendingForReview := candidates
		var allApproved []claude.ParsedCandidate
		replacementRound := 0

		for {
			approved, rejected, err := reviewCandidates(pendingForReview, opts.Config.RunsDir, date)
			if err != nil {
				return nil, fmt.Errorf("review candidates: %w", err)
			}
			allApproved = append(allApproved, approved...)
			allRejected = append(allRejected, rejected...)
			for _, r := range rejected {
				extraDelivered[r.Candidate.ID] = true
			}

			needed := opts.Config.FreshCandidatesPerDay - len(allApproved)
			if len(rejected) == 0 || needed <= 0 {
				break
			}

			expandedDelivered := make(map[string]bool, len(deliveredIDs)+len(extraDelivered))
			for id := range deliveredIDs {
				expandedDelivered[id] = true
			}
			for id := range extraDelivered {
				expandedDelivered[id] = true
			}

			replacementRound++
			fmt.Printf("\n%d candidate(s) discarded — generating %d replacement(s)…\n", len(rejected), needed)
			pendingForReview, err = generateReplacements(opts, needed, expandedDelivered, carryovers, date, replacementRound)
			if err != nil {
				return nil, fmt.Errorf("generate replacements (round %d): %w", replacementRound, err)
			}
			if len(pendingForReview) == 0 {
				fmt.Println("No replacement candidates returned — proceeding with what was approved.")
				break
			}
		}
		candidates = allApproved
	}

	if len(candidates) == 0 {
		fmt.Println("No candidates approved — slate not written.")
		return &Result{Date: date, Carryovers: carryovers}, nil
	}

	// 7. Create Jira issues for approved candidates.
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
			Labels:      []string{"ext-candidate"},
		})
		if err != nil {
			return nil, fmt.Errorf("create issue for %s: %w", pc.ID, err)
		}

		if err := jiraClient.Transition(issue.Key, opts.Config.Jira.CandidateStatus); err != nil {
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

	// Append rejected candidates so they appear in the dedup guard on future runs.
	for _, r := range allRejected {
		freshCandidates = append(freshCandidates, state.Candidate{
			ID:             r.Candidate.ID,
			Title:          r.Candidate.Title,
			Effort:         r.Candidate.Effort,
			Status:         state.StatusRejected,
			RejectedReason: r.Reason,
			Origin:         "generated",
			FirstDate:      date,
			SpecMD:         r.Candidate.Raw,
			Appearances:    1,
		})
	}

	// 8. Update carryover appearances and write today's slate.
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
		ReviewDone: !opts.NoReview,
	}
	if err := state.Save(opts.Config.RunsDir, slate); err != nil {
		return nil, fmt.Errorf("save slate: %w", err)
	}

	// Split fresh into approved vs rejected for the result summary.
	var approvedFresh []state.Candidate
	for _, c := range freshCandidates {
		if c.Status != state.StatusRejected {
			approvedFresh = append(approvedFresh, c)
		}
	}

	return &Result{
		Date:       date,
		Carryovers: updatedCarryovers,
		Fresh:      approvedFresh,
	}, nil
}

// reviewCandidates presents each candidate interactively and returns approved
// and rejected lists. The user can approve (a), discard with a reason (d),
// edit the spec file (e), or show the full spec (s).
func reviewCandidates(candidates []claude.ParsedCandidate, runsDir, date string) ([]claude.ParsedCandidate, []rejectedSpec, error) {
	reader := bufio.NewReader(os.Stdin)
	var approved []claude.ParsedCandidate
	var rejected []rejectedSpec
	total := len(candidates)

	for i, c := range candidates {
		fmt.Printf("\n--- Candidate %d/%d ---\n", i+1, total)
		printCandidateSummary(c)

	prompt:
		for {
			fmt.Print("\n[a]pprove  [d]iscard  [e]dit spec  [s]how full spec\n> ")
			line, _ := reader.ReadString('\n')
			choice := strings.TrimSpace(strings.ToLower(line))

			switch choice {
			case "a":
				approved = append(approved, c)
				fmt.Printf("✓ Approved: %s\n", c.ID)
				break prompt

			case "d":
				fmt.Print("Reason for discarding: ")
				reason, _ := reader.ReadString('\n')
				reason = strings.TrimSpace(reason)
				rejected = append(rejected, rejectedSpec{Candidate: c, Reason: reason})
				fmt.Printf("✗ Discarded: %s\n", c.ID)
				break prompt

			case "e":
				path, err := writeEditableSpec(runsDir, date, c.ID, c.Raw)
				if err != nil {
					fmt.Printf("error writing spec: %v\n", err)
					continue
				}
				fmt.Printf("Spec written to %s\nEdit it, then press Enter to continue...", path)
				reader.ReadString('\n')
				updated, err := os.ReadFile(path)
				if err != nil {
					fmt.Printf("error reading updated spec: %v\n", err)
					continue
				}
				c.Raw = string(updated)
				fmt.Println("Spec updated.")

			case "s":
				fmt.Println()
				fmt.Println(c.Raw)
			}
		}
	}
	return approved, rejected, nil
}

// generateReplacements runs Claude to produce count replacement candidates,
// using an expanded deliveredIDs map that includes the just-rejected IDs.
// Output is written to runs/<date>/specgen-r<round>.jsonl so the original is preserved.
func generateReplacements(opts Options, count int, deliveredIDs map[string]bool, carryovers []state.Candidate, date string, round int) ([]claude.ParsedCandidate, error) {
	cfgCopy := *opts.Config
	cfgCopy.FreshCandidatesPerDay = count
	prompt, err := buildPrompt(&cfgCopy, carryovers, deliveredIDs)
	if err != nil {
		return nil, fmt.Errorf("build replacement prompt: %w", err)
	}

	outputFile := filepath.Join(opts.Config.RunsDir, date, fmt.Sprintf("specgen-r%d.jsonl", round))
	claudeOpts := claude.RunOptions{
		Prompt:       prompt,
		AllowedTools: []string{"Read", "Grep", "Glob"},
		WorkDir:      opts.Config.TargetRepo.Checkout,
		OutputFile:   outputFile,
		Model:        opts.Model,
	}
	fmt.Printf("Running claude for %d replacement(s) (round %d)…\n", count, round)
	result, err := claude.Run(claudeOpts)
	if err != nil {
		return nil, fmt.Errorf("claude replacement run: %w", err)
	}
	fmt.Printf("Claude finished: %d turns, $%.4f\n", result.NumTurns, result.TotalCostUSD)

	candidates, err := claude.ParseCandidates(result.Result)
	if err != nil {
		return nil, fmt.Errorf("parse replacement candidates: %w", err)
	}
	return candidates, nil
}

// writeEditableSpec writes the raw spec markdown to runs/<date>/review-<id>.md
// so the user can edit it in their preferred editor.
func writeEditableSpec(runsDir, date, id, raw string) (string, error) {
	dir := filepath.Join(runsDir, date)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("mkdir: %w", err)
	}
	path := filepath.Join(dir, "review-"+id+".md")
	if err := os.WriteFile(path, []byte(raw), 0o644); err != nil {
		return "", fmt.Errorf("write review file: %w", err)
	}
	return path, nil
}

// printCandidateSummary prints a short preview of a candidate.
func printCandidateSummary(c claude.ParsedCandidate) {
	fmt.Printf("ID:     %s\n", c.ID)
	fmt.Printf("Title:  %s\n", c.Title)
	fmt.Printf("Effort: %s\n", c.Effort)
	if c.Sketch != "" {
		lines := strings.SplitN(c.Sketch, "\n", 4)
		n := len(lines)
		if n > 3 {
			n = 3
		}
		fmt.Printf("Sketch: %s\n", strings.Join(lines[:n], " "))
	}
}

func buildPrompt(cfg *config.Config, carryovers []state.Candidate, deliveredIDs map[string]bool) (string, error) {
	promptBytes, err := os.ReadFile(cfg.Prompts.GenSpecs)
	if err != nil {
		return "", fmt.Errorf("read gen-specs prompt %s: %w", cfg.Prompts.GenSpecs, err)
	}
	prompt := strings.ReplaceAll(string(promptBytes), "{{N}}", fmt.Sprintf("%d", cfg.FreshCandidatesPerDay))

	poolBytes, err := os.ReadFile(cfg.IdeaPool)
	if err != nil && !os.IsNotExist(err) {
		return "", fmt.Errorf("read idea pool %s: %w", cfg.IdeaPool, err)
	}
	poolSection := "Idea pool:\n(empty — generate net-new ideas grounded in the repo)"
	if len(poolBytes) > 0 {
		poolSection = "Idea pool:\n" + string(poolBytes)
	}
	prompt += "\n\n" + poolSection

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
