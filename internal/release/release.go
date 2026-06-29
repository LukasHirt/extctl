// Package release implements `extctl release`: it scans the web-extensions
// checkout for extensions that have been merged to the default branch but never
// released, and creates + pushes a signed git tag (<app-id>-v<version>) for each.
// A GitHub Action in the web-extensions repo picks up the pushed tag and builds
// the actual release — extctl's only job is to push the tag.
package release

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/LukasHirt/extctl/internal/config"
	gitpkg "github.com/LukasHirt/extctl/internal/git"
)

// pkgPrefix is the directory/package-name prefix every extension carries in the
// web-extensions monorepo (e.g. packages/web-app-ai-doc-summary).
const pkgPrefix = "web-app-"

// Result describes one extension discovered on the default branch and whether it
// already has a release tag.
type Result struct {
	AppID           string // candidate ID, e.g. "ai-doc-summary" (no web-app- prefix)
	Version         string // version from package.json, e.g. "0.1.0"
	Tag             string // the tag <AppID>-v<Version>
	AlreadyReleased bool   // a tag with prefix <AppID>-v already exists
	Created         bool   // a new signed tag was created this run
}

// appIDFromDir strips the web-app- prefix from a package directory name. The
// bool is false for entries that are not extension packages.
func appIDFromDir(dir string) (string, bool) {
	if !strings.HasPrefix(dir, pkgPrefix) {
		return "", false
	}
	id := strings.TrimPrefix(dir, pkgPrefix)
	if id == "" {
		return "", false
	}
	return id, true
}

// parseVersion extracts the version field from a package.json payload.
func parseVersion(pkgJSON []byte) (string, error) {
	var pkg struct {
		Version string `json:"version"`
	}
	if err := json.Unmarshal(pkgJSON, &pkg); err != nil {
		return "", fmt.Errorf("decode package.json: %w", err)
	}
	if pkg.Version == "" {
		return "", fmt.Errorf("package.json has no version field")
	}
	return pkg.Version, nil
}

// isReleased reports whether any non-empty tag line was returned by
// `git tag -l <app-id>-v*`.
func isReleased(tags []string) bool {
	for _, t := range tags {
		if strings.TrimSpace(t) != "" {
			return true
		}
	}
	return false
}

// deriveTag builds the release tag name for an app and version.
func deriveTag(appID, version string) string {
	return appID + "-v" + version
}

// Scan fetches the target repo and returns the full inventory of extensions on
// the default branch, each annotated with whether it already has a release tag.
// It does not create or push anything and never touches the working tree.
func Scan(cfg *config.Config) ([]Result, error) {
	repo := cfg.TargetRepo.Checkout
	branch := cfg.DefaultBranch

	if err := gitpkg.FetchTags(repo); err != nil {
		return nil, fmt.Errorf("fetch origin: %w", err)
	}

	dirs, err := listPackageDirs(repo, branch)
	if err != nil {
		return nil, err
	}

	var results []Result
	for _, dir := range dirs {
		appID, ok := appIDFromDir(dir)
		if !ok {
			continue
		}

		pkgJSON, err := gitpkg.Output(repo, "show",
			fmt.Sprintf("origin/%s:packages/%s/package.json", branch, dir))
		if err != nil {
			return nil, fmt.Errorf("read package.json for %s: %w", appID, err)
		}
		version, err := parseVersion(pkgJSON)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", appID, err)
		}

		tagOut, err := gitpkg.Output(repo, "tag", "-l", appID+"-v*")
		if err != nil {
			return nil, fmt.Errorf("list tags for %s: %w", appID, err)
		}
		released := isReleased(strings.Split(string(tagOut), "\n"))

		results = append(results, Result{
			AppID:           appID,
			Version:         version,
			Tag:             deriveTag(appID, version),
			AlreadyReleased: released,
		})
	}

	sort.Slice(results, func(i, j int) bool { return results[i].AppID < results[j].AppID })
	return results, nil
}

// listPackageDirs lists the immediate entries of packages/ on origin/<branch>.
func listPackageDirs(repo, branch string) ([]string, error) {
	out, err := gitpkg.Output(repo, "ls-tree", "--name-only",
		fmt.Sprintf("origin/%s:packages", branch))
	if err != nil {
		return nil, fmt.Errorf("list packages on origin/%s: %w", branch, err)
	}
	var dirs []string
	for _, line := range strings.Split(string(out), "\n") {
		if d := strings.TrimSpace(line); d != "" {
			dirs = append(dirs, d)
		}
	}
	return dirs, nil
}

// Run scans the target repo, creates signed tags for merged-but-unreleased
// extensions, and pushes the newly created tags. Under dryRun it only reports
// what would be tagged. A summary is written to w.
func Run(cfg *config.Config, dryRun bool, w io.Writer) error {
	results, err := Scan(cfg)
	if err != nil {
		return err
	}

	printf := func(format string, a ...any) {
		_, _ = fmt.Fprintf(w, format, a...)
	}

	repo := cfg.TargetRepo.Checkout
	var created []string

	for i := range results {
		r := &results[i]
		if r.AlreadyReleased {
			printf("skip    %s (already released)\n", r.AppID)
			continue
		}
		if dryRun {
			printf("would   tag %s\n", r.Tag)
			continue
		}
		if err := gitpkg.CreateSignedTag(repo, r.Tag, r.Tag); err != nil {
			return fmt.Errorf("tag %s: %w", r.Tag, err)
		}
		r.Created = true
		created = append(created, r.Tag)
		printf("tagged  %s\n", r.Tag)
	}

	if !dryRun {
		if err := gitpkg.PushTags(repo, created...); err != nil {
			return fmt.Errorf("push tags: %w", err)
		}
	}

	if dryRun {
		printf("\n%d extension(s) scanned, %d would be released\n",
			len(results), countUnreleased(results))
	} else {
		printf("\n%d extension(s) scanned, %d released\n", len(results), len(created))
	}
	return nil
}

func countUnreleased(results []Result) int {
	n := 0
	for _, r := range results {
		if !r.AlreadyReleased {
			n++
		}
	}
	return n
}
