package marketplace

import (
	"fmt"
	"os"
	"strings"

	"github.com/LukasHirt/extctl/internal/claude"
	"github.com/LukasHirt/extctl/internal/config"
)

// TagSource labels where an extension's tags ultimately came from, so
// FormatPRBody can explain (or not) why a human should double-check them.
type TagSource string

const (
	TagSourcePreviousRelease TagSource = "previous-release"
	// TagSourceThisRun means another, earlier-processed version of the same
	// extension in this same `publish` run resolved tags (via
	// TagSourcePreviousRelease or TagSourceClaude) and this version reused
	// them rather than independently re-deriving — see Run's metadata
	// cache. Distinct from TagSourcePreviousRelease because the earlier
	// version's own PR may not be merged (or even reviewed) yet.
	TagSourceThisRun  TagSource = "this-run"
	TagSourceClaude   TagSource = "claude"
	TagSourceFallback TagSource = "fallback"
)

// ResolveTags decides tags for a submission, trying progressively less
// reliable sources and never erroring — every step degrades to the next:
//
//  1. Reuse tags from prev (this extension's own most recent prior
//     marketplace release, if any — see PreviousRelease) — already
//     human-approved, no guessing needed.
//  2. Ask Claude to infer 2-4 tags from the extension's own
//     package.json/README (best-effort; only reached when prev is nil or
//     has no tags).
//  3. No tags at all, if both of the above come up empty (no prior release,
//     Claude inference failed). extension.yaml requires at least one tag, so
//     this leaves the submission incomplete on purpose rather than inventing
//     a meaningless placeholder — FormatPRBody flags it prominently so a
//     human adds real tags before merging (marketplace CI will otherwise
//     reject the PR for the missing tags entry).
func ResolveTags(cfg *config.Config, appID string, prev *ExtensionYAML, printf func(string, ...any)) ([]string, TagSource) {
	if prev != nil && len(prev.Tags) > 0 {
		return prev.Tags, TagSourcePreviousRelease
	}

	if tags, err := InferTags(cfg, appID); err != nil {
		printf("  warning: could not infer tags via claude: %v\n", err)
	} else {
		return tags, TagSourceClaude
	}

	return nil, TagSourceFallback
}

// inferTagsTools is Read-only, matching gen-specs.md's read-only scanning
// convention — tag inference only classifies an existing extension, it never
// edits anything.
var inferTagsTools = []string{"Read", "Grep", "Glob"}

// InferTags asks Claude to read the extension's package.json/README (in
// cfg.TargetRepo.Checkout) and suggest 2-4 free-form marketplace category
// tags. Best-effort: any failure (prompt file missing, claude error,
// unparseable output) returns an error for the caller to log and fall back
// from — never treated as fatal to the whole publish run.
func InferTags(cfg *config.Config, appID string) ([]string, error) {
	promptBytes, err := os.ReadFile(cfg.Prompts.InferTags)
	if err != nil {
		return nil, fmt.Errorf("read infer-tags prompt %s: %w", cfg.Prompts.InferTags, err)
	}
	prompt := strings.ReplaceAll(string(promptBytes), "{{EXT_ID}}", appID)

	result, err := claude.Run(claude.RunOptions{
		Prompt:          prompt,
		AllowedTools:    inferTagsTools,
		DisallowedTools: []string{"Bash"},
		Model:           cfg.Claude.VersionPin,
		WorkDir:         cfg.TargetRepo.Checkout,
	})
	if err != nil {
		return nil, fmt.Errorf("claude infer-tags run: %w", err)
	}

	tags := parseTagLine(result.Result)
	if len(tags) == 0 {
		return nil, fmt.Errorf("claude returned no parseable tags: %q", result.Result)
	}
	return tags, nil
}

// parseTagLine extracts a comma-separated tag list from Claude's free-text
// response — takes the LAST non-empty line (in case the model added
// preamble despite the prompt's single-line instruction), splits on commas,
// and normalizes each tag (trim, lowercase).
func parseTagLine(text string) []string {
	lines := strings.Split(strings.TrimSpace(text), "\n")
	var lastNonEmpty string
	for _, l := range lines {
		if t := strings.TrimSpace(l); t != "" {
			lastNonEmpty = t
		}
	}
	if lastNonEmpty == "" {
		return nil
	}
	var tags []string
	for _, part := range strings.Split(lastNonEmpty, ",") {
		if t := strings.ToLower(strings.TrimSpace(part)); t != "" {
			tags = append(tags, t)
		}
	}
	return tags
}
