package build

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/LukasHirt/extctl/internal/config"
)

// createWorktreeFixtures sets up the minimum directory structure that
// ScaffoldExtension expects to find in the worktree.
func createWorktreeFixtures(t *testing.T, dir string) {
	t.Helper()
	dirs := []string{
		"packages",
		"dev/docker",
		"support/actions",
	}
	for _, d := range dirs {
		if err := os.MkdirAll(filepath.Join(dir, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	dockerCompose := `services:
  ocis:
    volumes:
      - ./packages/web-app-existing/dist:/web/apps/existing
`
	writeFixture(t, dir, "docker-compose.yml", dockerCompose)

	devApps := "existing:\n  config:\n    llm:\n      endpoint: 'http://localhost'\n"
	writeFixture(t, dir, "dev/docker/ocis.apps.yaml", devApps)

	supportApps := "web-app-existing:\n  config:\n    llm:\n      endpoint: 'http://localhost'\n"
	writeFixture(t, dir, "support/actions/ocis.apps.yaml", supportApps)
}

func writeFixture(t *testing.T, base, rel, content string) {
	t.Helper()
	path := filepath.Join(base, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// scaffoldDir returns the path to the testdata scaffold template.
func scaffoldDir(t *testing.T) string {
	t.Helper()
	// internal/build/testdata/scaffold
	dir, err := filepath.Abs("testdata/scaffold")
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestScaffoldExtension_CopiesFiles(t *testing.T) {
	dir := t.TempDir()
	createWorktreeFixtures(t, dir)

	// Replace runCmd to avoid real pnpm/git calls.
	original := runCmd
	t.Cleanup(func() { runCmd = original })
	runCmd = func(workDir, name string, args ...string) error {
		return nil
	}

	cfg := &config.Config{ScaffoldDir: scaffoldDir(t)}
	err := ScaffoldExtension(ScaffoldOptions{
		Config:       cfg,
		CandidateID:  "my-test-ext",
		Title:        "My Test Extension",
		Description:  "A test",
		WorktreePath: dir,
	})
	if err != nil {
		t.Fatalf("ScaffoldExtension: %v", err)
	}

	pkgDir := filepath.Join(dir, "packages", "web-app-my-test-ext")
	if _, err := os.Stat(pkgDir); err != nil {
		t.Fatalf("package dir not created: %v", err)
	}

	// Verify template substitution in package.json
	data, err := os.ReadFile(filepath.Join(pkgDir, "package.json"))
	if err != nil {
		t.Fatalf("package.json not copied: %v", err)
	}
	if !strings.Contains(string(data), "my-test-ext") {
		t.Errorf("{{EXT_ID}} not substituted in package.json: %s", data)
	}
	if strings.Contains(string(data), "{{EXT_ID}}") {
		t.Error("{{EXT_ID}} placeholder still present in package.json")
	}
}

func TestScaffoldExtension_SubstitutesTitle(t *testing.T) {
	dir := t.TempDir()
	createWorktreeFixtures(t, dir)

	original := runCmd
	t.Cleanup(func() { runCmd = original })
	runCmd = func(workDir, name string, args ...string) error { return nil }

	cfg := &config.Config{ScaffoldDir: scaffoldDir(t)}
	err := ScaffoldExtension(ScaffoldOptions{
		Config:       cfg,
		CandidateID:  "my-test-ext",
		Title:        "My Test Extension",
		Description:  "A test",
		WorktreePath: dir,
	})
	if err != nil {
		t.Fatalf("ScaffoldExtension: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dir, "packages", "web-app-my-test-ext", "src", "index.ts"))
	if err != nil {
		t.Fatalf("src/index.ts not copied: %v", err)
	}
	if !strings.Contains(string(data), "My Test Extension") {
		t.Errorf("{{EXT_TITLE}} not substituted in src/index.ts: %s", data)
	}
}

func TestScaffoldExtension_RunsPnpmInstall(t *testing.T) {
	dir := t.TempDir()
	createWorktreeFixtures(t, dir)

	var pnpmCalled bool
	original := runCmd
	t.Cleanup(func() { runCmd = original })
	runCmd = func(workDir, name string, args ...string) error {
		if name == "pnpm" && len(args) > 0 && args[0] == "install" {
			pnpmCalled = true
		}
		return nil
	}

	cfg := &config.Config{ScaffoldDir: scaffoldDir(t)}
	if err := ScaffoldExtension(ScaffoldOptions{
		Config: cfg, CandidateID: "my-ext", Title: "T", Description: "D", WorktreePath: dir,
	}); err != nil {
		t.Fatalf("ScaffoldExtension: %v", err)
	}
	if !pnpmCalled {
		t.Error("pnpm install not called")
	}
}

func TestScaffoldExtension_CommitsViaGit(t *testing.T) {
	dir := t.TempDir()
	createWorktreeFixtures(t, dir)

	var gitAddCalled, gitCommitCalled bool
	original := runCmd
	t.Cleanup(func() { runCmd = original })
	runCmd = func(workDir, name string, args ...string) error {
		if name == "git" {
			if len(args) > 0 && args[0] == "add" {
				gitAddCalled = true
			}
			if len(args) > 0 && args[0] == "commit" {
				gitCommitCalled = true
			}
		}
		return nil
	}

	cfg := &config.Config{ScaffoldDir: scaffoldDir(t)}
	if err := ScaffoldExtension(ScaffoldOptions{
		Config: cfg, CandidateID: "my-ext", Title: "T", Description: "D", WorktreePath: dir,
	}); err != nil {
		t.Fatalf("ScaffoldExtension: %v", err)
	}
	if !gitAddCalled {
		t.Error("git add not called")
	}
	if !gitCommitCalled {
		t.Error("git commit not called")
	}
}

func TestScaffoldExtension_Idempotent(t *testing.T) {
	dir := t.TempDir()
	createWorktreeFixtures(t, dir)

	callCount := 0
	original := runCmd
	t.Cleanup(func() { runCmd = original })
	runCmd = func(workDir, name string, args ...string) error {
		callCount++
		return nil
	}

	cfg := &config.Config{ScaffoldDir: scaffoldDir(t)}
	opts := ScaffoldOptions{
		Config: cfg, CandidateID: "my-ext", Title: "T", Description: "D", WorktreePath: dir,
	}

	if err := ScaffoldExtension(opts); err != nil {
		t.Fatalf("first call: %v", err)
	}
	first := callCount

	if err := ScaffoldExtension(opts); err != nil {
		t.Fatalf("second call: %v", err)
	}
	// Second call should be a no-op — runCmd not called again.
	if callCount != first {
		t.Errorf("runCmd called %d extra times on second call (want 0)", callCount-first)
	}
}

func TestScaffoldExtension_DockerMountAdded(t *testing.T) {
	dir := t.TempDir()
	createWorktreeFixtures(t, dir)

	original := runCmd
	t.Cleanup(func() { runCmd = original })
	runCmd = func(workDir, name string, args ...string) error { return nil }

	cfg := &config.Config{ScaffoldDir: scaffoldDir(t)}
	if err := ScaffoldExtension(ScaffoldOptions{
		Config: cfg, CandidateID: "new-ext", Title: "T", Description: "D", WorktreePath: dir,
	}); err != nil {
		t.Fatalf("ScaffoldExtension: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dir, "docker-compose.yml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "web-app-new-ext/dist:/web/apps/new-ext") {
		t.Errorf("docker-compose.yml missing new mount:\n%s", data)
	}
}
