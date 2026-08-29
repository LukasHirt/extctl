package marketplace

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/LukasHirt/extctl/internal/config"
	gitpkg "github.com/LukasHirt/extctl/internal/git"
)

// PackageJSON holds the package.json fields extension.yaml generation reads.
type PackageJSON struct {
	Description string `json:"description"`
	License     string `json:"license"`
}

// ReadPackageJSON reads packages/web-app-<appID>/package.json from
// origin/<branch> in the web-extensions checkout — the same git-show
// pattern internal/release.Scan uses for the same file.
func ReadPackageJSON(checkout, branch, appID string) (PackageJSON, error) {
	raw, err := gitpkg.Output(checkout, "show",
		fmt.Sprintf("origin/%s:packages/web-app-%s/package.json", branch, appID))
	if err != nil {
		return PackageJSON{}, fmt.Errorf("read package.json for %s: %w", appID, err)
	}
	var pkg PackageJSON
	if err := json.Unmarshal(raw, &pkg); err != nil {
		return PackageJSON{}, fmt.Errorf("decode package.json for %s: %w", appID, err)
	}
	return pkg, nil
}

// ExtensionYAML is the extension.yaml schema owncloud/marketplace expects
// for an oCIS web extension release (verified against a real submission,
// extensions/draw-io/releases/0.2.0/extension.yaml).
type ExtensionYAML struct {
	ID                 string     `yaml:"id"`
	Name               string     `yaml:"name"`
	Subtitle           string     `yaml:"subtitle"`
	License            string     `yaml:"license"`
	Version            string     `yaml:"version"`
	Authors            []Author   `yaml:"authors"`
	Tags               []string   `yaml:"tags"`
	MinOCIS            string     `yaml:"minOCIS,omitempty"`
	Resources          []Resource `yaml:"resources,omitempty"`
	Cover              bool       `yaml:"cover,omitempty"`
	ScreenshotCaptions []string   `yaml:"screenshotCaptions,omitempty"`
}

type Author struct {
	Name string `yaml:"name"`
	URL  string `yaml:"url,omitempty"`
}

type Resource struct {
	Label string `yaml:"label"`
	Icon  string `yaml:"icon,omitempty"`
	URL   string `yaml:"url"`
}

// BuildExtensionYAML derives extension.yaml fields from cfg.Publish (fixed,
// org-wide metadata), the extension's own package.json (license, subtitle
// fallback), tags/minOCIS already resolved by ResolveTags/ResolveMinOCIS,
// the screenshot capture step's captions, and prev — this extension's own
// most recent prior marketplace release, if any (see PreviousRelease).
//
// license is read verbatim from pkg.License — if that field is empty,
// BuildExtensionYAML returns an error rather than guessing a default value;
// callers must treat this as a hard failure for that extension (it should
// not be submitted with a potentially wrong license).
//
// name and subtitle reuse prev's curated values when prev is non-nil and
// has them set, falling through to the humanized app-id / package.json
// description only for an extension's first-ever submission (or if a prior
// release genuinely left one of them empty). Before this, every
// republish — including one that only touches a later version's own
// package.json — clobbered whatever a human had already curated back to
// the generator's own guesses; see owncloud/marketplace#240's sibling bug
// report for the same pattern in minOCIS.
//
// cover is true only when hasCoverBytes is true (the caller — stageOne —
// only sets that after actually retrieving real cover.png bytes to write
// alongside this extension.yaml via previousCoverBytes). Never set cover
// true from prev.Cover directly: a submission whose cover.png failed to
// fetch would otherwise commit a dangling `cover: true` with no file to
// back it, reproducing owncloud/marketplace#238's class of bug for cover
// art specifically instead of fixing it.
//
// tags and minOCIS are passed through as-is, including empty — extension.yaml
// requires at least one tag but BuildExtensionYAML does not enforce that
// itself (ResolveTags may legitimately return none; FormatPRBody is what
// flags an incomplete submission for a human to finish, rather than this
// function inventing a placeholder).
func BuildExtensionYAML(cfg *config.Config, appID, version string, pkg PackageJSON, tags []string, minOCIS string, captions []string, prev *ExtensionYAML, hasCoverBytes bool) (ExtensionYAML, error) {
	license := strings.TrimSpace(pkg.License)
	if license == "" {
		return ExtensionYAML{}, fmt.Errorf("package.json for %s has no license field — refusing to guess one", appID)
	}

	authors := make([]Author, len(cfg.Publish.Authors))
	for i, a := range cfg.Publish.Authors {
		authors[i] = Author{Name: a.Name, URL: a.URL}
	}

	name := humanizeAppID(appID)
	subtitle := pkg.Description
	if prev != nil {
		if prev.Name != "" {
			name = prev.Name
		}
		if prev.Subtitle != "" {
			subtitle = prev.Subtitle
		}
	}

	return ExtensionYAML{
		ID:       "com.github.owncloud.web-extensions." + appID,
		Name:     name,
		Subtitle: subtitle,
		License:  license,
		Version:  version,
		Authors:  authors,
		Tags:     tags,
		MinOCIS:  minOCIS,
		Resources: []Resource{{
			Label: "GitHub",
			Icon:  "github",
			URL:   "https://github.com/owncloud/web-extensions/tree/main/packages/web-app-" + appID,
		}},
		Cover:              hasCoverBytes,
		ScreenshotCaptions: captions,
	}, nil
}

// humanizeAppID turns a hyphenated app-id into a display name fallback (e.g.
// "draw-io" -> "Draw Io") — used by BuildExtensionYAML only when prev has no
// curated name to reuse (this extension's first-ever submission, or a prior
// release that itself somehow has no name). extctl has no dedicated
// display-name source today (package.json has no such field); printReviewNotes
// flags this case for a human to sanity-check/correct before approving.
func humanizeAppID(appID string) string {
	parts := strings.Split(appID, "-")
	for i, p := range parts {
		if p == "" {
			continue
		}
		parts[i] = strings.ToUpper(p[:1]) + p[1:]
	}
	return strings.Join(parts, " ")
}
