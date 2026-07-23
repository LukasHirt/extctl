package marketplace

import (
	"path/filepath"
	"testing"

	"github.com/LukasHirt/extctl/internal/config"
)

func testCacheConfig(t *testing.T) *config.Config {
	t.Helper()
	return &config.Config{
		Prompts: config.Prompts{InferTags: filepath.Join(t.TempDir(), "does-not-exist.md")},
	}
}

// TestRunMetadataCache_ReusesAcrossVersions reproduces the exact scenario
// the user asked about: three unpublished versions of the same extension in
// one run should share tags/minOCIS, not independently re-derive them.
func TestRunMetadataCache_ReusesAcrossVersions(t *testing.T) {
	cfg := testCacheConfig(t)
	cache := newRunMetadataCache()

	// v0.1.0: no merged previous release (prev=nil), Claude inference will
	// fail (prompt missing) — falls to the hardcoded "no tags" tier, cached.
	tags1, source1 := cache.resolveTags(cfg, "my-ext", nil, func(string, ...any) {})
	if source1 != TagSourceFallback {
		t.Fatalf("v0.1.0 source = %q, want %q", source1, TagSourceFallback)
	}
	if len(tags1) != 0 {
		t.Fatalf("v0.1.0 tags = %+v, want none", tags1)
	}

	// v0.2.0 and v0.3.0: still no merged previous release, but the cache
	// now has an entry for "my-ext" — must reuse it, not re-attempt Claude.
	tags2, source2 := cache.resolveTags(cfg, "my-ext", nil, func(string, ...any) {})
	if source2 != TagSourceThisRun {
		t.Errorf("v0.2.0 source = %q, want %q", source2, TagSourceThisRun)
	}
	if !stringSlicesEqual(tags1, tags2) {
		t.Errorf("v0.2.0 tags = %+v, want the same as v0.1.0 (%+v)", tags2, tags1)
	}

	tags3, source3 := cache.resolveTags(cfg, "my-ext", nil, func(string, ...any) {})
	if source3 != TagSourceThisRun {
		t.Errorf("v0.3.0 source = %q, want %q", source3, TagSourceThisRun)
	}
	if !stringSlicesEqual(tags1, tags3) {
		t.Errorf("v0.3.0 tags = %+v, want the same as v0.1.0 (%+v)", tags3, tags1)
	}
}

func TestRunMetadataCache_MinOCISReusesAcrossVersions(t *testing.T) {
	cfg := testCacheConfig(t)
	cfg.TargetRepo.Checkout = t.TempDir() // not a git repo — FirstCommitDate errors, falls to "none"
	cfg.DefaultBranch = "main"
	cache := newRunMetadataCache()

	v1, source1 := cache.resolveMinOCIS(cfg, "my-ext", nil, func(string, ...any) {})
	if source1 != MinOCISSourceNone {
		t.Fatalf("v0.1.0 source = %q, want %q", source1, MinOCISSourceNone)
	}

	v2, source2 := cache.resolveMinOCIS(cfg, "my-ext", nil, func(string, ...any) {})
	if source2 != MinOCISSourceThisRun {
		t.Errorf("v0.2.0 source = %q, want %q", source2, MinOCISSourceThisRun)
	}
	if v1 != v2 {
		t.Errorf("v0.2.0 minOCIS = %q, want the same as v0.1.0 (%q)", v2, v1)
	}
}

// TestRunMetadataCache_MergedPreviousReleaseAlwaysWins ensures a genuinely
// merged prior release (prev != nil) is never shadowed by a stale in-run
// cache entry for a DIFFERENT app-id, and that per-app-id caching doesn't
// cross-contaminate.
func TestRunMetadataCache_DifferentAppIDsDoNotShareCache(t *testing.T) {
	cfg := testCacheConfig(t)
	cache := newRunMetadataCache()

	tagsA, sourceA := cache.resolveTags(cfg, "ext-a", nil, func(string, ...any) {})
	tagsB, sourceB := cache.resolveTags(cfg, "ext-b", nil, func(string, ...any) {})
	if sourceA != TagSourceFallback || sourceB != TagSourceFallback {
		t.Fatalf("expected both fresh app-ids to independently resolve, got %q and %q", sourceA, sourceB)
	}
	_ = tagsA
	_ = tagsB

	// A second version of ext-a must now reuse the cache; ext-b's presence
	// in the cache must not affect it.
	_, sourceA2 := cache.resolveTags(cfg, "ext-a", nil, func(string, ...any) {})
	if sourceA2 != TagSourceThisRun {
		t.Errorf("ext-a v2 source = %q, want %q", sourceA2, TagSourceThisRun)
	}
}

// TestRunMetadataCache_MergedPreviousReleaseTakesPriority confirms a merged
// previous release always wins over the in-run cache, even if the cache
// already holds a (worse) value for this app-id from an earlier version
// processed without a merged release visible yet.
func TestRunMetadataCache_MergedPreviousReleaseTakesPriority(t *testing.T) {
	cfg := testCacheConfig(t)
	cache := newRunMetadataCache()

	// Simulate an earlier version resolving without a merged release.
	cache.resolveTags(cfg, "my-ext", nil, func(string, ...any) {})

	// A later version DOES have a merged previous release visible (e.g. an
	// older version's PR merged in between) — that must win over whatever
	// got cached from the unmerged-state resolution.
	prev := &ExtensionYAML{Tags: []string{"editor", "viewer"}}
	tags, source := cache.resolveTags(cfg, "my-ext", prev, func(string, ...any) {})
	if source != TagSourcePreviousRelease {
		t.Errorf("source = %q, want %q (merged release must take priority over the in-run cache)", source, TagSourcePreviousRelease)
	}
	if !stringSlicesEqual(tags, prev.Tags) {
		t.Errorf("tags = %+v, want prev.Tags (%+v)", tags, prev.Tags)
	}
}
