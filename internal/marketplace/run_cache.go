package marketplace

import "github.com/LukasHirt/extctl/internal/config"

// runMetadataCache remembers tags/minOCIS resolved for each app-id during a
// single Run, so that when multiple unpublished versions of the same
// extension are attempted in one run, only the first (oldest, per Scan's
// sort) independently derives them — via PreviousRelease/Claude/history —
// and later versions of that same extension reuse the result instead of
// each re-deriving independently. See Run's doc comment for why this is
// needed: PreviousRelease only sees what's merged, and publishing goes
// through an unmerged PR, so sibling versions in one batch would otherwise
// never see each other's freshly-chosen values.
type runMetadataCache struct {
	tags    map[string]cachedTags
	minOCIS map[string]cachedMinOCIS
}

type cachedTags struct {
	tags   []string
	source TagSource
}

type cachedMinOCIS struct {
	value  string
	source MinOCISSource
}

func newRunMetadataCache() *runMetadataCache {
	return &runMetadataCache{
		tags:    map[string]cachedTags{},
		minOCIS: map[string]cachedMinOCIS{},
	}
}

// resolveTags checks prev (a merged marketplace release, if any) first —
// that's always authoritative and human-approved — then this run's own
// cache for appID, and only falls through to ResolveTags's Claude/fallback
// chain if neither has an answer yet. The result (whichever tier it came
// from) is cached for subsequent versions of the same appID in this run.
func (c *runMetadataCache) resolveTags(cfg *config.Config, appID string, prev *ExtensionYAML, printf func(string, ...any)) ([]string, TagSource) {
	if prev != nil && len(prev.Tags) > 0 {
		return prev.Tags, TagSourcePreviousRelease
	}
	if cached, ok := c.tags[appID]; ok {
		return cached.tags, TagSourceThisRun
	}
	tags, source := ResolveTags(cfg, appID, nil, printf)
	c.tags[appID] = cachedTags{tags: tags, source: source}
	return tags, source
}

// resolveMinOCIS mirrors resolveTags for minOCIS.
func (c *runMetadataCache) resolveMinOCIS(cfg *config.Config, appID string, prev *ExtensionYAML, printf func(string, ...any)) (string, MinOCISSource) {
	if prev != nil && prev.MinOCIS != "" {
		return prev.MinOCIS, MinOCISSourcePreviousRelease
	}
	if cached, ok := c.minOCIS[appID]; ok {
		return cached.value, MinOCISSourceThisRun
	}
	value, source := ResolveMinOCIS(cfg, appID, nil, printf)
	c.minOCIS[appID] = cachedMinOCIS{value: value, source: source}
	return value, source
}
