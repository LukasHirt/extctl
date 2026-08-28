package marketplace

import (
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"sort"
	"sync"
)

// dockerHubTagsURL is the Docker Hub Hub API v2 endpoint listing
// owncloud/ocis image tags. page_size=100 is Hub's own cap, minimizing
// pagination round-trips over the ~700+ tags that repo currently carries.
const dockerHubTagsURL = "https://hub.docker.com/v2/repositories/owncloud/ocis/tags?page_size=100"

// httpGet is replaced in tests to avoid real network calls.
var httpGet = http.Get

type dockerHubTagsPage struct {
	Next    string         `json:"next"`
	Results []dockerHubTag `json:"results"`
}

type dockerHubTag struct {
	Name string `json:"name"`
}

// stableSemverTag matches a plain "X.Y.Z" oCIS Docker Hub tag — a real
// release image, as opposed to "latest", floating major/minor tags ("8",
// "8.2"), prerelease tags ("8.2.0-rc.1"), or the date-suffixed rebuild tags
// ("8.2.0-20260823") Docker Hub's retention keeps alongside the plain one.
var stableSemverTag = regexp.MustCompile(`^\d+\.\d+\.\d+$`)

// fetchDockerHubTags fetches every tag name from url, following "next"
// pages until exhausted.
func fetchDockerHubTags(url string) ([]string, error) {
	var names []string
	for url != "" {
		resp, err := httpGet(url) //nolint:gosec // url is dockerHubTagsURL or its own "next" page, not user input
		if err != nil {
			return nil, fmt.Errorf("GET %s: %w", url, err)
		}
		var page dockerHubTagsPage
		err = json.NewDecoder(resp.Body).Decode(&page)
		_ = resp.Body.Close()
		if err != nil {
			return nil, fmt.Errorf("decode docker hub tags page: %w", err)
		}
		for _, t := range page.Results {
			names = append(names, t.Name)
		}
		url = page.Next
	}
	return names, nil
}

// filterStableSemverVersions keeps only plain "X.Y.Z" tags from names,
// dedupes, and returns them sorted ascending (compareVersions — the same
// dotted-numeric ordering release directories use). This is the candidate
// list BisectMinOCIS searches over.
//
// Docker Hub is missing an entire major series — confirmed: oCIS 6.x has
// zero images, plain or suffixed, of any kind, unlike every other series
// from 1.x through 8.x. Deliberately not special-cased here: whatever gap
// exists in the source data just isn't a candidate, so a bisection search
// below or above it naturally lands on the nearest version that IS
// available instead of erroring or needing a hardcoded exception.
func filterStableSemverVersions(names []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, n := range names {
		if !stableSemverTag.MatchString(n) || seen[n] {
			continue
		}
		seen[n] = true
		out = append(out, n)
	}
	sort.Slice(out, func(i, j int) bool { return compareVersions(out[i], out[j]) < 0 })
	return out
}

var (
	ocisImageVersionsOnce sync.Once
	ocisImageVersionsData []string
	ocisImageVersionsErr  error
)

// AvailableOCISImageVersions returns every stable "X.Y.Z" owncloud/ocis
// Docker Hub tag, sorted ascending — the candidate set VerifyMinOCIS
// bisects over. Cached for the process lifetime, the same reasoning as
// cachedOCISReleases: the data can't meaningfully change within one
// publish/verify run.
func AvailableOCISImageVersions() ([]string, error) {
	ocisImageVersionsOnce.Do(func() {
		names, err := fetchDockerHubTags(dockerHubTagsURL)
		if err != nil {
			ocisImageVersionsErr = err
			return
		}
		ocisImageVersionsData = filterStableSemverVersions(names)
	})
	return ocisImageVersionsData, ocisImageVersionsErr
}
