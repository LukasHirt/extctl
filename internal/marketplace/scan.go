// Package marketplace implements `extctl publish`: it scans
// owncloud/web-extensions' GitHub Releases (created by `extctl release` +
// web-extensions' own release.yml CI) for extensions not yet present in
// owncloud/marketplace, and for each one downloads the release bundle,
// captures screenshots from the extension's own e2e acceptance spec,
// generates extension.yaml, and opens a PR against the marketplace repo.
package marketplace

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os/exec"
	"sort"
	"strings"

	"github.com/LukasHirt/extctl/internal/config"
	gitpkg "github.com/LukasHirt/extctl/internal/git"
)

// execCommand is the exec.Command function used to invoke the gh CLI.
// Replaced in tests to avoid calling the real binary.
var execCommand = exec.Command

// Result describes one owncloud/web-extensions GitHub Release and whether it
// has already been published to owncloud/marketplace.
type Result struct {
	AppID     string // e.g. "draw-io" (no web-app- prefix)
	Version   string // e.g. "0.2.0"
	Tag       string // the release tag, e.g. "draw-io-v0.2.0"
	AssetName string // release zip asset name, e.g. "draw-io-0.2.0.zip"
	Published bool   // extensions/<AppID>/releases/<Version>/ already exists in the marketplace checkout
}

// ghRelease mirrors the fields this package needs from the GitHub REST API's
// release object (repos/<owner>/<repo>/releases). Fetched via `gh api`
// rather than `gh release list`, because `gh release list --json` does not
// support an "assets" field at all (verified against the installed gh CLI —
// its supported fields are createdAt, isDraft, isLatest, isPrerelease, name,
// publishedAt, tagName); the REST API's release object has it directly.
type ghRelease struct {
	TagName    string   `json:"tag_name"`
	Draft      bool     `json:"draft"`
	Prerelease bool     `json:"prerelease"`
	Assets     []string `json:"assets"` // asset file names only — trimmed via --jq below
}

// splitTag splits a release tag "<app-id>-v<version>" on its LAST "-v"
// occurrence, so app-ids that themselves contain "-v" still parse correctly.
func splitTag(tag string) (appID, version string, ok bool) {
	idx := strings.LastIndex(tag, "-v")
	if idx <= 0 || idx+2 >= len(tag) {
		return "", "", false
	}
	return tag[:idx], tag[idx+2:], true
}

// Scan lists every non-draft, non-prerelease GitHub Release on
// cfg.TargetRepo.Remote and cross-references cfg.MarketplaceRepo.Checkout
// (already fetched+hard-reset onto origin/<branch> by EnsureCheckout) to
// determine which ones are already published. It does not download, create,
// or push anything.
func Scan(cfg *config.Config) ([]Result, error) {
	releases, err := listReleases(cfg.TargetRepo.Remote)
	if err != nil {
		return nil, err
	}

	var results []Result
	for _, rel := range releases {
		if rel.Draft || rel.Prerelease {
			continue
		}
		appID, version, ok := splitTag(rel.TagName)
		if !ok {
			continue
		}
		assetName := findZipAsset(rel.Assets, appID)
		if assetName == "" {
			continue // release build hasn't produced its zip asset yet
		}

		published, err := isPublished(cfg.MarketplaceRepo.Checkout, cfg.MarketplaceRepo.DefaultBranch, appID, version)
		if err != nil {
			return nil, fmt.Errorf("check published state for %s: %w", appID, err)
		}

		results = append(results, Result{
			AppID:     appID,
			Version:   version,
			Tag:       rel.TagName,
			AssetName: assetName,
			Published: published,
		})
	}

	sortResults(results)
	return results, nil
}

// sortResults orders by AppID, then by version ascending within the same
// AppID — so when multiple unpublished versions of one extension are
// attempted in the same Run, they process oldest-first. Run's per-appID
// metadata cache (tags/minOCIS) relies on this: whichever version is
// resolved first becomes what later versions of the same extension reuse,
// and the oldest version is the sensible one to root that on.
func sortResults(results []Result) {
	sort.Slice(results, func(i, j int) bool {
		if results[i].AppID != results[j].AppID {
			return results[i].AppID < results[j].AppID
		}
		return compareVersions(results[i].Version, results[j].Version) < 0
	})
}

func findZipAsset(assets []string, appID string) string {
	prefix := appID + "-"
	for _, name := range assets {
		if strings.HasPrefix(name, prefix) && strings.HasSuffix(name, ".zip") {
			return name
		}
	}
	return ""
}

// listReleases fetches every release on remote via `gh api
// repos/<remote>/releases --paginate`, filtered down to just the fields this
// package needs. --paginate follows every page; --jq applied per page with
// `.[]` emits one compact JSON object per release rather than one JSON array
// per page, so the concatenated output across all pages is a valid stream of
// back-to-back JSON values that json.Decoder can read regardless of how many
// pages were fetched.
func listReleases(remote string) ([]ghRelease, error) {
	cmd := execCommand("gh", "api",
		fmt.Sprintf("repos/%s/releases", remote),
		"--paginate",
		"--jq", `.[] | {tag_name, draft, prerelease, assets: [.assets[].name]}`,
	)
	out, err := cmd.Output()
	if err != nil {
		stderr := ""
		if ee, ok := err.(*exec.ExitError); ok {
			stderr = strings.TrimSpace(string(ee.Stderr))
		}
		return nil, fmt.Errorf("gh api repos/%s/releases: %w\n%s", remote, err, stderr)
	}

	var releases []ghRelease
	dec := json.NewDecoder(bytes.NewReader(out))
	for dec.More() {
		var r ghRelease
		if err := dec.Decode(&r); err != nil {
			return nil, fmt.Errorf("parse gh api releases output: %w", err)
		}
		releases = append(releases, r)
	}
	return releases, nil
}

// isPublished checks whether extensions/<appID>/releases/<version>/ already
// exists on origin/<branch> in the marketplace checkout. A git error here
// (most commonly: extensions/<appID>/ doesn't exist yet at that revision) is
// treated as "not published" — that is by far the common case for an
// extension that has never been submitted before, not a real failure.
func isPublished(checkout, branch, appID, version string) (bool, error) {
	out, err := gitpkg.Output(checkout, "ls-tree", "--name-only",
		fmt.Sprintf("origin/%s:extensions/%s/releases", branch, appID))
	if err != nil {
		return false, nil
	}
	for line := range strings.SplitSeq(string(out), "\n") {
		if strings.TrimSpace(line) == version {
			return true, nil
		}
	}
	return false, nil
}
