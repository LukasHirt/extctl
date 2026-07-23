package marketplace

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/LukasHirt/extctl/internal/claude"
	"github.com/LukasHirt/extctl/internal/config"
)

// screenshotSpecRelPath is where GenerateScreenshotSpec writes its output,
// relative to packages/web-app-<appID>/ — a sibling of acceptance.spec.ts,
// never overwriting it.
const screenshotSpecRelPath = "tests/e2e/marketplace-screenshots.spec.ts"

// marketplaceScreenshotsTools is Read-only plus Write/Edit scoped by
// instruction to exactly one output file (see
// prompts/marketplace-screenshots.md) — Claude must understand the
// extension's existing source and acceptance spec to write a good screenshot
// spec, but must not touch either. Edit is granted alongside Write (not
// Write alone) because Claude routinely needs to revise the file it just
// wrote (e.g. fixing a flaky assertion after re-reading a source file) —
// observed failing with only Write granted: Claude correctly wrote the spec,
// then tried Edit on that same file, got denied, and gave up mid-task
// instead of falling back to a full Write.
//
// Bash is scoped to exactly `pnpm playwright test ...` — never a bare
// `Bash` grant — so Claude can run the spec it just wrote against the
// already-brought-up oCIS instance (see prepareOCISForCapture, called
// before this) and iterate on real failures instead of writing blind and
// hoping. This is the same class of pattern-scoped Bash grant CLAUDE.md
// already documents for build-stage.md/repair.md (e.g.
// `Bash(pnpm install *)`); like those, `--allowedTools` is a pre-approval
// allowlist, not a hard sandbox — see internal/claude/run.go's
// DisallowedTools doc comment.
var marketplaceScreenshotsTools = []string{"Read", "Grep", "Glob", "Write", "Edit", "Bash(pnpm playwright test *)"}

// GenerateScreenshotSpec asks Claude to write a fresh, dedicated
// tests/e2e/marketplace-screenshots.spec.ts for appID in
// cfg.TargetRepo.Checkout — never acceptance.spec.ts, which is written to
// prove functional assertions pass, not to look good publicly (it tends to
// split one journey into several near-duplicate-looking tests and often
// stops before the actual generated content renders) — and, since it has
// scoped Bash access to `pnpm playwright test` (see
// marketplaceScreenshotsTools) and a live oCIS is already up by the time
// this is called (prepareOCISForCapture runs first), to actually run it
// against reportPath/project and fix what it sees fail before returning,
// rather than writing blind. This file is never committed; the caller
// removes it once done.
//
// Not guaranteed to leave every test passing (Claude self-limits its own
// fix-and-rerun cycles per the prompt) — the caller checks reportPath itself
// (gate.AllTestsPassed) and falls back to running the spec directly
// (runPlaywrightDirect) if Claude's session never left a report at all.
func GenerateScreenshotSpec(cfg *config.Config, appID, reportPath, project string) (specPath string, err error) {
	promptBytes, err := os.ReadFile(cfg.Prompts.MarketplaceScreenshots)
	if err != nil {
		return "", fmt.Errorf("read marketplace-screenshots prompt %s: %w", cfg.Prompts.MarketplaceScreenshots, err)
	}
	prompt := strings.NewReplacer(
		"{{EXT_ID}}", appID,
		"{{MAX_SCREENSHOTS}}", strconv.Itoa(cfg.Publish.MaxScreenshots),
		"{{PLAYWRIGHT_PROJECT}}", project,
	).Replace(string(promptBytes))

	result, err := claude.Run(claude.RunOptions{
		Prompt:       prompt,
		AllowedTools: marketplaceScreenshotsTools,
		Model:        cfg.Claude.VersionPin,
		WorkDir:      cfg.TargetRepo.Checkout,
		Env:          playwrightCaptureEnv(reportPath),
	})
	if err != nil {
		return "", fmt.Errorf("claude marketplace-screenshots run: %w", err)
	}
	if result.IsError {
		return "", fmt.Errorf("claude marketplace-screenshots returned error: %s", result.Result)
	}

	specPath = filepath.Join(cfg.TargetRepo.Checkout, "packages", "web-app-"+appID, screenshotSpecRelPath)
	if _, statErr := os.Stat(specPath); statErr != nil {
		return "", fmt.Errorf("claude did not write %s: %w", specPath, statErr)
	}
	return specPath, nil
}
