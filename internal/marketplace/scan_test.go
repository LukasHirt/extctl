package marketplace

import "testing"

func TestSplitTag(t *testing.T) {
	cases := []struct {
		tag     string
		wantID  string
		wantVer string
		wantOK  bool
	}{
		{"draw-io-v0.2.0", "draw-io", "0.2.0", true},
		{"group-management-v0.1.0", "group-management", "0.1.0", true},
		{"web-app-v-thing-v1.0.0", "web-app-v-thing", "1.0.0", true}, // app-id containing "-v" still parses on the LAST occurrence
		{"just-a-tag-nothing", "", "", false},                        // no "-v" substring at all
		{"-v1.0.0", "", "", false},                                   // empty app-id
		{"trailing-v", "", "", false},
		{"", "", "", false},
	}
	for _, c := range cases {
		id, ver, ok := splitTag(c.tag)
		if id != c.wantID || ver != c.wantVer || ok != c.wantOK {
			t.Errorf("splitTag(%q) = (%q, %q, %v), want (%q, %q, %v)",
				c.tag, id, ver, ok, c.wantID, c.wantVer, c.wantOK)
		}
	}
}

// TestSortResults_OldestVersionFirstWithinAppID matters for the
// per-run tags/minOCIS reuse cache: whichever version of an extension is
// processed first becomes what later versions reuse, so ordering must be
// deterministic and rooted on the oldest version, not GitHub API response
// order (typically newest-first) or accidental sort.Slice instability
// among equal AppID keys.
func TestSortResults_OldestVersionFirstWithinAppID(t *testing.T) {
	results := []Result{
		{AppID: "my-ext", Version: "0.10.0"}, // deliberately out of naive lexicographic order
		{AppID: "my-ext", Version: "0.2.0"},
		{AppID: "my-ext", Version: "0.1.0"},
		{AppID: "another-ext", Version: "1.0.0"},
	}
	sortResults(results)

	want := []string{"another-ext@1.0.0", "my-ext@0.1.0", "my-ext@0.2.0", "my-ext@0.10.0"}
	got := make([]string, len(results))
	for i, r := range results {
		got[i] = r.AppID + "@" + r.Version
	}
	if !stringSlicesEqual(got, want) {
		t.Errorf("sortResults order = %+v, want %+v", got, want)
	}
}

func TestFindZipAsset(t *testing.T) {
	assets := []string{"draw-io-0.2.0.zip", "md5sum.txt", "sha256sum.txt"}
	if got := findZipAsset(assets, "draw-io"); got != "draw-io-0.2.0.zip" {
		t.Errorf("findZipAsset = %q, want draw-io-0.2.0.zip", got)
	}
	if got := findZipAsset(assets, "other-ext"); got != "" {
		t.Errorf("findZipAsset for unrelated app-id = %q, want empty", got)
	}
	// A prefix match on the app-id alone must not false-positive against an
	// unrelated asset that merely starts with the same letters.
	if got := findZipAsset([]string{"draw-io-extra-0.1.0.zip"}, "draw-io"); got != "draw-io-extra-0.1.0.zip" {
		t.Errorf("findZipAsset = %q, want draw-io-extra-0.1.0.zip", got)
	}
}
