package release

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/LukasHirt/extctl/internal/config"
)

func TestAppIDFromDir(t *testing.T) {
	cases := []struct {
		dir    string
		wantID string
		wantOK bool
	}{
		{"web-app-ai-doc-summary", "ai-doc-summary", true},
		{"web-app-chat", "chat", true},
		{"web-app-", "", false},
		{"design-system", "", false},
		{"node_modules", "", false},
	}
	for _, c := range cases {
		id, ok := appIDFromDir(c.dir)
		if id != c.wantID || ok != c.wantOK {
			t.Errorf("appIDFromDir(%q) = (%q, %v), want (%q, %v)", c.dir, id, ok, c.wantID, c.wantOK)
		}
	}
}

func TestParseVersion(t *testing.T) {
	if v, err := parseVersion([]byte(`{"name":"web-app-x","version":"1.2.3"}`)); err != nil || v != "1.2.3" {
		t.Errorf("valid: got (%q, %v), want (1.2.3, nil)", v, err)
	}
	if _, err := parseVersion([]byte(`{"name":"web-app-x"}`)); err == nil {
		t.Error("missing version: expected error, got nil")
	}
	if _, err := parseVersion([]byte(`{not json`)); err == nil {
		t.Error("malformed json: expected error, got nil")
	}
}

func TestIsReleased(t *testing.T) {
	if !isReleased([]string{"alpha-v0.1.0", ""}) {
		t.Error("expected released for non-empty tag list")
	}
	if isReleased([]string{"", "  ", ""}) {
		t.Error("expected not released for empty/whitespace tag list")
	}
	if isReleased(nil) {
		t.Error("expected not released for nil")
	}
}

func TestDeriveTag(t *testing.T) {
	if got := deriveTag("ai-doc-summary", "0.1.0"); got != "ai-doc-summary-v0.1.0" {
		t.Errorf("deriveTag = %q, want ai-doc-summary-v0.1.0", got)
	}
}

// --- integration: real git repos ---

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func writePkg(t *testing.T, dir, appID, version string) {
	t.Helper()
	pkgDir := filepath.Join(dir, "packages", "web-app-"+appID)
	if err := os.MkdirAll(pkgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	content := `{"name":"web-app-` + appID + `","version":"` + version + `"}`
	if err := os.WriteFile(filepath.Join(pkgDir, "package.json"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// setupRepos builds a bare remote with two extensions on branch main (one of
// which, "alpha", already has a release tag) and a checkout cloned from it.
// It returns a Config pointing at the checkout.
func setupRepos(t *testing.T) *config.Config {
	t.Helper()

	remote := t.TempDir()
	runGit(t, remote, "init", "--bare")

	src := t.TempDir()
	runGit(t, src, "init")
	runGit(t, src, "checkout", "-b", "main")
	runGit(t, src, "config", "user.email", "test@test.com")
	runGit(t, src, "config", "user.name", "Test")
	writePkg(t, src, "alpha", "0.1.0")
	writePkg(t, src, "beta", "0.2.0")
	runGit(t, src, "add", ".")
	runGit(t, src, "commit", "-m", "initial extensions")
	// alpha is already released; lightweight tag avoids needing a signing key
	// even when the host git config defaults to signed/annotated tags.
	runGit(t, src, "-c", "tag.gpgsign=false", "tag", "alpha-v0.1.0")
	runGit(t, src, "remote", "add", "origin", remote)
	runGit(t, src, "push", "origin", "main")
	runGit(t, src, "push", "origin", "--tags")

	checkout := t.TempDir()
	runGit(t, t.TempDir(), "clone", remote, checkout)

	return &config.Config{
		TargetRepo:    config.TargetRepo{Checkout: checkout},
		DefaultBranch: "main",
	}
}

func resultByID(results []Result, id string) *Result {
	for i := range results {
		if results[i].AppID == id {
			return &results[i]
		}
	}
	return nil
}

func TestScan_Integration(t *testing.T) {
	cfg := setupRepos(t)

	results, err := Scan(cfg)
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 extensions, got %d: %+v", len(results), results)
	}

	alpha := resultByID(results, "alpha")
	if alpha == nil || !alpha.AlreadyReleased {
		t.Errorf("alpha should be already released: %+v", alpha)
	}
	beta := resultByID(results, "beta")
	if beta == nil || beta.AlreadyReleased {
		t.Errorf("beta should be unreleased: %+v", beta)
	}
	if beta != nil && beta.Tag != "beta-v0.2.0" {
		t.Errorf("beta tag = %q, want beta-v0.2.0", beta.Tag)
	}
}

func TestRun_DryRun(t *testing.T) {
	cfg := setupRepos(t)

	var buf bytes.Buffer
	if err := Run(cfg, true, &buf); err != nil {
		t.Fatalf("Run dry-run: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "would   tag beta-v0.2.0") {
		t.Errorf("dry-run output missing beta tag line:\n%s", out)
	}
	if !strings.Contains(out, "skip    alpha") {
		t.Errorf("dry-run output missing alpha skip line:\n%s", out)
	}

	// Dry-run must not create any tags in the checkout.
	cmd := exec.Command("git", "tag", "-l", "beta-v*")
	cmd.Dir = cfg.TargetRepo.Checkout
	tagOut, err := cmd.Output()
	if err != nil {
		t.Fatalf("list tags: %v", err)
	}
	if strings.TrimSpace(string(tagOut)) != "" {
		t.Errorf("dry-run created a tag: %q", strings.TrimSpace(string(tagOut)))
	}
}
