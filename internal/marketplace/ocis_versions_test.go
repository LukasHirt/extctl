package marketplace

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"testing"
)

func TestFilterStableSemverVersions(t *testing.T) {
	// Mirrors the real owncloud/ocis Docker Hub tag shapes observed this
	// session: plain releases, floating major/minor tags, date-suffixed
	// rebuilds, prereleases, and "latest" — only the plain "X.Y.Z" tags
	// should survive, deduped and sorted ascending.
	in := []string{
		"8.2.0", "latest", "8", "8.2", "8.2.0-20260823", "8.1.0-rc.1",
		"1.0.0", "7.3.2", "8.0.0", "8.2.0", // duplicate 8.2.0
	}
	want := []string{"1.0.0", "7.3.2", "8.0.0", "8.2.0"}

	got := filterStableSemverVersions(in)
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("got[%d] = %q, want %q (full: %v)", i, got[i], want[i], got)
		}
	}
}

func TestFilterStableSemverVersions_Empty(t *testing.T) {
	got := filterStableSemverVersions(nil)
	if len(got) != 0 {
		t.Errorf("got %v, want empty", got)
	}
}

// fakeRoundTripper serves canned JSON pages keyed by URL, so
// fetchDockerHubTags's pagination loop can be tested without a real network
// call.
type fakeRoundTripper struct {
	pages map[string]dockerHubTagsPage
}

func (f *fakeRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	page, ok := f.pages[req.URL.String()]
	if !ok {
		return nil, io.ErrUnexpectedEOF
	}
	body, err := json.Marshal(page)
	if err != nil {
		return nil, err
	}
	return &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(bytes.NewReader(body)),
		Header:     make(http.Header),
	}, nil
}

func TestFetchDockerHubTags_Paginates(t *testing.T) {
	const page1URL = "https://hub.docker.com/v2/repositories/owncloud/ocis/tags?page=1"
	const page2URL = "https://hub.docker.com/v2/repositories/owncloud/ocis/tags?page=2"

	client := &http.Client{Transport: &fakeRoundTripper{pages: map[string]dockerHubTagsPage{
		page1URL: {
			Next: page2URL,
			Results: []dockerHubTag{
				{Name: "8.2.0"}, {Name: "8.1.0"},
			},
		},
		page2URL: {
			Next: "",
			Results: []dockerHubTag{
				{Name: "8.0.0"},
			},
		},
	}}}

	orig := httpGet
	httpGet = client.Get
	defer func() { httpGet = orig }()

	got, err := fetchDockerHubTags(page1URL)
	if err != nil {
		t.Fatalf("fetchDockerHubTags: %v", err)
	}
	want := []string{"8.2.0", "8.1.0", "8.0.0"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("got[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}
