package marketplace

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/LukasHirt/extctl/internal/config"
	gitpkg "github.com/LukasHirt/extctl/internal/git"
	githubpkg "github.com/LukasHirt/extctl/internal/github"
)

// existingPR is a GitHub PR found for a given head branch.
type existingPR struct {
	URL   string `json:"url"`
	State string `json:"state"` // "OPEN" | "CLOSED" | "MERGED"
}

// findExistingPR checks for any PR (open, closed, or merged) on branch, so a
// rerun after a partial failure (commit+push succeeded, PR creation didn't)
// doesn't open a duplicate, and so `approve` on a branch whose PR was
// closed without merging opens a fresh one instead of erroring. Prefers an
// OPEN result if more than one PR somehow exists for the same head branch;
// otherwise reports the most recent one (gh pr list returns newest first).
func findExistingPR(repoSlug, branch string) (*existingPR, error) {
	cmd := execCommand("gh", "pr", "list",
		"--repo", repoSlug,
		"--head", branch,
		"--state", "all",
		"--json", "url,state",
	)
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	var prs []existingPR
	if err := json.Unmarshal(out, &prs); err != nil {
		return nil, err
	}
	return pickPR(prs), nil
}

// pickPR chooses which PR findExistingPR should report when a head branch
// has more than one match: an OPEN one always wins; otherwise the most
// recent (gh pr list returns newest first, so prs[0]). nil if prs is empty.
func pickPR(prs []existingPR) *existingPR {
	if len(prs) == 0 {
		return nil
	}
	for i := range prs {
		if prs[i].State == "OPEN" {
			return &prs[i]
		}
	}
	return &prs[0]
}

// parseIDArg splits "<app-id>" or "<app-id>@<version>" (the form
// `extctl publish approve`/`retry-screenshots` accept) into its parts.
// version is "" if the arg didn't include an "@".
func parseIDArg(arg string) (appID, version string) {
	if i := strings.LastIndex(arg, "@"); i >= 0 {
		return arg[:i], arg[i+1:]
	}
	return arg, ""
}

// findPendingVersions lists the versions of appID that have a local
// publish/<appID>-v<version> branch in checkout, sorted oldest-first.
func findPendingVersions(checkout, appID string) ([]string, error) {
	out, err := gitpkg.Output(checkout, "branch", "--list",
		"publish/"+appID+"-v*", "--format=%(refname:short)")
	if err != nil {
		return nil, fmt.Errorf("list pending branches for %s: %w", appID, err)
	}
	prefix := "publish/" + appID + "-v"
	var versions []string
	for line := range strings.SplitSeq(strings.TrimSpace(string(out)), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		versions = append(versions, strings.TrimPrefix(line, prefix))
	}
	sort.Slice(versions, func(i, j int) bool { return compareVersions(versions[i], versions[j]) < 0 })
	return versions, nil
}

// ResolvePendingBranch resolves idArg ("<app-id>" or "<app-id>@<version>",
// see parseIDArg) to the exact locally-staged publish/<app-id>-v<version>
// branch created by a prior `extctl publish` run. If idArg omits the
// version and exactly one is pending, that one is used; zero or several
// pending versions is an error requiring the caller to disambiguate with
// "<app-id>@<version>".
func ResolvePendingBranch(checkout, idArg string) (appID, version, branch string, err error) {
	appID, version = parseIDArg(idArg)
	if version != "" {
		branch = "publish/" + appID + "-v" + version
		if !branchExistsLocally(checkout, branch) {
			return "", "", "", fmt.Errorf("no pending submission found for %s@%s (branch %s does not exist — run `extctl publish --id %s` first)", appID, version, branch, appID)
		}
		return appID, version, branch, nil
	}

	versions, err := findPendingVersions(checkout, appID)
	if err != nil {
		return "", "", "", err
	}
	switch len(versions) {
	case 0:
		return "", "", "", fmt.Errorf("no pending submission found for %s — run `extctl publish --id %s` first", appID, appID)
	case 1:
		version = versions[0]
	default:
		return "", "", "", fmt.Errorf("multiple pending versions for %s (%s) — specify which one with %s@<version>",
			appID, strings.Join(versions, ", "), appID)
	}
	branch = "publish/" + appID + "-v" + version
	return appID, version, branch, nil
}

// PendingSubmission identifies one locally-staged
// publish/<app-id>-v<version> branch in the marketplace checkout.
type PendingSubmission struct {
	AppID   string
	Version string
	Branch  string
}

// listPendingSubmissions lists every local publish/<app-id>-v<version>
// branch in checkout, sorted by AppID then version ascending — the same
// order Scan/Run process extensions in.
func listPendingSubmissions(checkout string) ([]PendingSubmission, error) {
	out, err := gitpkg.Output(checkout, "branch", "--list",
		"publish/*", "--format=%(refname:short)")
	if err != nil {
		return nil, fmt.Errorf("list pending branches: %w", err)
	}
	var subs []PendingSubmission
	for line := range strings.SplitSeq(strings.TrimSpace(string(out)), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		// splitTag expects "<app-id>-v<version>" (it splits release tags of
		// that same shape) — a publish branch name is exactly that after
		// stripping the "publish/" prefix.
		appID, version, ok := splitTag(strings.TrimPrefix(line, "publish/"))
		if !ok {
			continue
		}
		subs = append(subs, PendingSubmission{AppID: appID, Version: version, Branch: line})
	}
	sort.Slice(subs, func(i, j int) bool {
		if subs[i].AppID != subs[j].AppID {
			return subs[i].AppID < subs[j].AppID
		}
		return compareVersions(subs[i].Version, subs[j].Version) < 0
	})
	return subs, nil
}

// ApproveAllResult is the outcome of one submission's Approve call within an
// ApproveAll batch.
type ApproveAllResult struct {
	AppID   string
	Version string
	PRURL   string // empty if Err is set
	Err     error
}

// ApproveAll runs Approve for every locally-staged
// publish/<app-id>-v<version> branch in cfg.MarketplaceRepo.Checkout, so a
// batch of staged submissions doesn't need approving one `extctl publish
// approve <id>` call at a time. A single submission failing (e.g. Approve's
// own PR-creation call erroring) does not stop the rest — each is recorded
// in the returned results and the caller decides what to report.
func ApproveAll(cfg *config.Config, w io.Writer) ([]ApproveAllResult, error) {
	printf := func(format string, a ...any) { _, _ = fmt.Fprintf(w, format, a...) }
	checkout := cfg.MarketplaceRepo.Checkout

	subs, err := listPendingSubmissions(checkout)
	if err != nil {
		return nil, err
	}
	if len(subs) == 0 {
		printf("no staged submissions pending approval\n")
		return nil, nil
	}

	results := make([]ApproveAllResult, 0, len(subs))
	for _, s := range subs {
		idArg := s.AppID + "@" + s.Version
		printf("approve %s…\n", idArg)
		prURL, err := Approve(cfg, idArg, w)
		if err != nil {
			printf("failed  %s: %v\n", idArg, err)
			results = append(results, ApproveAllResult{AppID: s.AppID, Version: s.Version, Err: err})
			continue
		}
		printf("approved %s — %s\n", idArg, prURL)
		results = append(results, ApproveAllResult{AppID: s.AppID, Version: s.Version, PRURL: prURL})
	}
	return results, nil
}

// readExtensionYAMLFile reads and parses an extension.yaml from disk —
// used to recover a staged submission's already-resolved fields (license,
// tags, minOCIS, authors, ...) in Approve/RetryScreenshots without
// re-deriving them.
func readExtensionYAMLFile(path string) (ExtensionYAML, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return ExtensionYAML{}, fmt.Errorf("read %s: %w", path, err)
	}
	var ext ExtensionYAML
	if err := yaml.Unmarshal(raw, &ext); err != nil {
		return ExtensionYAML{}, fmt.Errorf("parse %s: %w", path, err)
	}
	return ext, nil
}

// Approve pushes the already-committed local branch for a staged submission
// (built by a prior `extctl publish` run) and opens its marketplace PR.
// idArg is "<app-id>" if exactly one version is pending, or
// "<app-id>@<version>" to disambiguate. Idempotent: if the branch already
// has an open PR (e.g. a previous approve call succeeded but this
// invocation is a retry), returns that PR's URL rather than erroring or
// duplicating it.
func Approve(cfg *config.Config, idArg string, w io.Writer) (string, error) {
	printf := func(format string, a ...any) { _, _ = fmt.Fprintf(w, format, a...) }
	checkout := cfg.MarketplaceRepo.Checkout

	appID, version, branch, err := ResolvePendingBranch(checkout, idArg)
	if err != nil {
		return "", err
	}

	if existing, err := findExistingPR(cfg.MarketplaceRepo.Remote, branch); err != nil {
		printf("warning: could not check for an existing PR: %v\n", err)
	} else if existing != nil && existing.State == "OPEN" {
		return existing.URL, nil
	}

	if err := runGit(checkout, "checkout", branch); err != nil {
		return "", fmt.Errorf("checkout %s: %w", branch, err)
	}

	extPath := filepath.Join(checkout, "extensions", appID, "releases", version, "extension.yaml")
	ext, err := readExtensionYAMLFile(extPath)
	if err != nil {
		return "", fmt.Errorf("read staged extension.yaml: %w", err)
	}

	if err := gitpkg.PushBranch(checkout, branch); err != nil {
		return "", fmt.Errorf("push branch: %w", err)
	}

	r := Result{AppID: appID, Version: version, Tag: appID + "-v" + version}
	pr, err := githubpkg.Create(githubpkg.PROptions{
		RepoSlug: cfg.MarketplaceRepo.Remote,
		Branch:   branch,
		Title:    fmt.Sprintf("feat(%s): add %s to the marketplace", appID, version),
		Body:     FormatPRBody(r, ext),
		Labels:   []string{},
		Draft:    false,
	})
	if err != nil {
		return "", fmt.Errorf("create PR: %w", err)
	}
	return pr.URL, nil
}

// RetryScreenshots regenerates a fresh screenshot spec via Claude and
// recaptures screenshots for an already-staged submission, reusing the
// already-downloaded bundle and already-resolved tags/minOCIS/license from
// that submission's commit — no re-download, no re-Claude-call for tags, no
// new branch. Internally goes through the same fresh-spec-until-fully-passing
// loop as the initial staging step — see maxFreshSpecAttempts's doc comment.
// The result is amended onto the existing commit so `extctl publish approve`
// still pushes exactly one commit.
func RetryScreenshots(cfg *config.Config, idArg string, w io.Writer) error {
	printf := func(format string, a ...any) { _, _ = fmt.Fprintf(w, format, a...) }
	checkout := cfg.MarketplaceRepo.Checkout

	appID, version, branch, err := ResolvePendingBranch(checkout, idArg)
	if err != nil {
		return err
	}
	if err := runGit(checkout, "checkout", branch); err != nil {
		return fmt.Errorf("checkout %s: %w", branch, err)
	}

	relDir := filepath.Join("extensions", appID, "releases", version)
	bundlePath := filepath.Join(checkout, relDir, "bundle.zip")
	if _, err := os.Stat(bundlePath); err != nil {
		return fmt.Errorf("staged bundle.zip not found at %s: %w", bundlePath, err)
	}

	reviewDir := filepath.Join(cfg.RunsDir, "publish", appID+"-"+version)
	r := Result{AppID: appID, Version: version, Tag: appID + "-v" + version}
	screenshotPaths, captions := captureScreenshotsWithRetry(cfg, r, bundlePath, reviewDir, printf)

	if err := AmendSubmissionScreenshots(checkout, appID, version, screenshotPaths, captions); err != nil {
		return fmt.Errorf("amend submission: %w", err)
	}

	if len(screenshotPaths) == 0 {
		printf("no screenshots captured this attempt — see %s\n", reviewDir)
		return nil
	}
	printf("captured %d screenshot(s):\n", len(screenshotPaths))
	for i, p := range screenshotPaths {
		printf("  %d. %s — %q\n", i+1, p, captions[i])
	}
	return nil
}
