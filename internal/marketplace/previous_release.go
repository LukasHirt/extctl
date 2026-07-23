package marketplace

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/LukasHirt/extctl/internal/config"
	gitpkg "github.com/LukasHirt/extctl/internal/git"
)

// PreviousRelease returns the extension.yaml from the most recent existing
// marketplace release of appID (by dotted version comparison), or nil if
// this is the first-ever submission for appID. Callers use it to reuse
// fields a human already curated once — tags (see ResolveTags) and minOCIS
// (see ResolveMinOCIS) — rather than re-guessing them for every new version
// of an already-published extension.
func PreviousRelease(checkout, branch, appID string) (*ExtensionYAML, error) {
	versions, err := listExistingVersions(checkout, branch, appID)
	if err != nil {
		return nil, err
	}
	if len(versions) == 0 {
		return nil, nil
	}
	latest := versions[len(versions)-1]

	raw, err := gitpkg.Output(checkout, "show",
		fmt.Sprintf("origin/%s:extensions/%s/releases/%s/extension.yaml", branch, appID, latest))
	if err != nil {
		return nil, fmt.Errorf("read extension.yaml for %s@%s: %w", appID, latest, err)
	}
	var prev ExtensionYAML
	if err := yaml.Unmarshal(raw, &prev); err != nil {
		return nil, fmt.Errorf("parse extension.yaml for %s@%s: %w", appID, latest, err)
	}
	return &prev, nil
}

// listExistingVersions returns the release version directory names already
// published for appID in the marketplace checkout (origin/<branch>), sorted
// oldest-first. Empty (not an error) if this is the first-ever submission
// for appID — the most common case, so a missing extensions/<appID>/
// path is not treated as a failure.
func listExistingVersions(checkout, branch, appID string) ([]string, error) {
	out, err := gitpkg.Output(checkout, "ls-tree", "--name-only",
		fmt.Sprintf("origin/%s:extensions/%s/releases", branch, appID))
	if err != nil {
		return nil, nil
	}
	var versions []string
	for line := range strings.SplitSeq(string(out), "\n") {
		if v := strings.TrimSpace(line); v != "" {
			versions = append(versions, v)
		}
	}
	sort.Slice(versions, func(i, j int) bool { return compareVersions(versions[i], versions[j]) < 0 })
	return versions, nil
}

// compareVersions compares two dotted version strings numerically per
// segment (e.g. "0.2.0" < "0.10.0", where a plain string compare would get
// it backwards), falling back to a plain string compare on any segment
// that isn't purely numeric (pre-release suffixes etc.).
func compareVersions(a, b string) int {
	as := strings.Split(a, ".")
	bs := strings.Split(b, ".")
	for i := 0; i < len(as) || i < len(bs); i++ {
		var av, bv string
		if i < len(as) {
			av = as[i]
		}
		if i < len(bs) {
			bv = bs[i]
		}
		an, aErr := strconv.Atoi(av)
		bn, bErr := strconv.Atoi(bv)
		if aErr == nil && bErr == nil {
			if an != bn {
				return an - bn
			}
			continue
		}
		if av != bv {
			return strings.Compare(av, bv)
		}
	}
	return 0
}

// MinOCISSource labels where an extension's minOCIS value came from.
type MinOCISSource string

const (
	MinOCISSourcePreviousRelease MinOCISSource = "previous-release"
	// MinOCISSourceThisRun mirrors TagSourceThisRun: another, earlier-
	// processed version of the same extension in this same `publish` run
	// resolved minOCIS and this version reused it — see Run's metadata
	// cache.
	MinOCISSourceThisRun MinOCISSource = "this-run"
	MinOCISSourceHistory MinOCISSource = "history-inferred"
	MinOCISSourceNone    MinOCISSource = "none"
)

// ResolveMinOCIS decides minOCIS for a submission, trying progressively less
// reliable sources and never erroring:
//
//  1. Reuse minOCIS from prev (this extension's own most recent prior
//     marketplace release, if any) — already human-approved.
//  2. Infer it from history (InferMinOCISFromHistory): the latest stable
//     oCIS release that existed on or before this extension's first commit
//     in web-extensions — a deterministic, git-derived proxy, not a
//     verified compatibility check.
//  3. Leave it empty (source "none") if neither is available —
//     extension.yaml's minOCIS is optional, so this is a legitimate final
//     state, not a failure. Unlike tags, minOCIS is never guessed by Claude
//     or a global config constant: it's a hard compatibility claim, not a
//     soft categorization, so a wrong value is worse than an absent one.
//
// FormatPRBody flags sources 2 and 3 in the PR for a human to sanity-check
// or fill in.
func ResolveMinOCIS(cfg *config.Config, appID string, prev *ExtensionYAML, printf func(string, ...any)) (string, MinOCISSource) {
	if prev != nil && prev.MinOCIS != "" {
		return prev.MinOCIS, MinOCISSourcePreviousRelease
	}

	if v, err := InferMinOCISFromHistory(cfg.TargetRepo.Checkout, cfg.DefaultBranch, appID); err != nil {
		printf("  warning: could not infer minOCIS from oCIS release history: %v\n", err)
	} else {
		return v, MinOCISSourceHistory
	}

	return "", MinOCISSourceNone
}
