package marketplace

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"time"

	gitpkg "github.com/LukasHirt/extctl/internal/git"
)

// ocisRepo is the fixed upstream repo whose release history is used to
// infer a starting-point minOCIS — not user-configurable, same convention
// as the hardcoded GitHub link in BuildExtensionYAML's resources field.
const ocisRepo = "owncloud/ocis"

// FirstCommitDate returns the author date of the earliest commit on
// origin/<branch> that touched packages/web-app-<appID>/ in checkout —
// i.e. when the extension was first added to web-extensions.
func FirstCommitDate(checkout, branch, appID string) (time.Time, error) {
	path := "packages/web-app-" + appID + "/"
	out, err := gitpkg.Output(checkout, "log", "--reverse", "--format=%aI",
		"origin/"+branch, "--", path)
	if err != nil {
		return time.Time{}, fmt.Errorf("git log for %s: %w", path, err)
	}
	first, _, _ := strings.Cut(strings.TrimSpace(string(out)), "\n")
	if first == "" {
		return time.Time{}, fmt.Errorf("no commits found for %s", path)
	}
	t, err := time.Parse(time.RFC3339, first)
	if err != nil {
		return time.Time{}, fmt.Errorf("parse commit date %q: %w", first, err)
	}
	return t, nil
}

type ocisRelease struct {
	TagName     string `json:"tag_name"`
	PublishedAt string `json:"published_at"`
	Draft       bool   `json:"draft"`
	Prerelease  bool   `json:"prerelease"`
}

// ocisReleasesCache avoids re-fetching all ~190 oCIS releases from the
// GitHub API once per extension in a batch publish run — the data can't
// meaningfully change within one process's lifetime.
var (
	ocisReleasesOnce sync.Once
	ocisReleasesData []ocisRelease
	ocisReleasesErr  error
)

func cachedOCISReleases() ([]ocisRelease, error) {
	ocisReleasesOnce.Do(func() {
		ocisReleasesData, ocisReleasesErr = listOCISReleases()
	})
	return ocisReleasesData, ocisReleasesErr
}

// listOCISReleases fetches every release of owncloud/ocis via `gh api
// repos/owncloud/ocis/releases --paginate` — same JSONL-over-pages pattern
// as listReleases in scan.go, for the same reason (gh's --jq '.[] | {...}'
// emits one compact object per release regardless of how many pages were
// fetched, which json.Decoder can stream regardless of pagination).
func listOCISReleases() ([]ocisRelease, error) {
	cmd := execCommand("gh", "api",
		fmt.Sprintf("repos/%s/releases", ocisRepo),
		"--paginate",
		"--jq", `.[] | {tag_name, published_at, draft, prerelease}`,
	)
	out, err := cmd.Output()
	if err != nil {
		stderr := ""
		if ee, ok := err.(*exec.ExitError); ok {
			stderr = strings.TrimSpace(string(ee.Stderr))
		}
		return nil, fmt.Errorf("gh api repos/%s/releases: %w\n%s", ocisRepo, err, stderr)
	}

	var releases []ocisRelease
	dec := json.NewDecoder(bytes.NewReader(out))
	for dec.More() {
		var r ocisRelease
		if err := dec.Decode(&r); err != nil {
			return nil, fmt.Errorf("parse gh api releases output: %w", err)
		}
		releases = append(releases, r)
	}
	return releases, nil
}

// pickLatestStableBefore returns the highest stable (non-draft,
// non-prerelease) release version published on or before cutoff, with its
// "v" tag prefix stripped, or "" if none qualify. Pure function, no network
// — kept separate from InferMinOCISFromHistory so it's testable without
// mocking gh.
func pickLatestStableBefore(releases []ocisRelease, cutoff time.Time) string {
	var best string
	for _, r := range releases {
		if r.Draft || r.Prerelease {
			continue
		}
		pubAt, err := time.Parse(time.RFC3339, r.PublishedAt)
		if err != nil || pubAt.After(cutoff) {
			continue
		}
		version := strings.TrimPrefix(r.TagName, "v")
		if best == "" || compareVersions(version, best) > 0 {
			best = version
		}
	}
	return best
}

// InferMinOCISFromHistory finds the latest stable oCIS release that existed
// on or before this extension's first commit date in the web-extensions
// checkout — a defensible, deterministic proxy for "what oCIS version this
// extension was actually built against," used only when there's no prior
// marketplace release to reuse minOCIS from (see ResolveMinOCIS). This is
// explicitly a heuristic, not a verified compatibility check — an extension
// could work on an older oCIS than whatever was current when it was
// written, or (less likely, since extension-point APIs are generally
// additive) require something newer. FormatPRBody flags it for a human to
// sanity-check or remove, same as it does for Claude-inferred tags.
func InferMinOCISFromHistory(targetCheckout, targetBranch, appID string) (string, error) {
	firstCommit, err := FirstCommitDate(targetCheckout, targetBranch, appID)
	if err != nil {
		return "", err
	}

	releases, err := cachedOCISReleases()
	if err != nil {
		return "", err
	}

	version := pickLatestStableBefore(releases, firstCommit)
	if version == "" {
		return "", fmt.Errorf("no stable %s release found published on or before %s (extension's first commit)",
			ocisRepo, firstCommit.Format("2006-01-02"))
	}
	return version, nil
}
