package build

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// createWireFixtures sets up the four files that WireExtension reads and edits.
func createWireFixtures(t *testing.T, dir string) {
	t.Helper()
	os.MkdirAll(filepath.Join(dir, ".github/workflows"), 0o755) //nolint:errcheck
	os.MkdirAll(filepath.Join(dir, "support/actions"), 0o755)   //nolint:errcheck
	os.MkdirAll(filepath.Join(dir, "dev/docker"), 0o755)        //nolint:errcheck
	os.MkdirAll(filepath.Join(dir, "packages"), 0o755)          //nolint:errcheck

	writeFixture(t, dir, "docker-compose.yml",
		"services:\n  ocis:\n    volumes:\n      - ./packages/web-app-existing/dist:/web/apps/existing\n")
	writeFixture(t, dir, ".github/workflows/test.yml",
		"jobs:\n  test:\n    strategy:\n      matrix:\n        app:\n          - web-app-existing\n")
	writeFixture(t, dir, "support/actions/ocis.apps.yaml",
		"web-app-existing:\n  config:\n    llm:\n      endpoint: 'http://localhost'\n")
	writeFixture(t, dir, "dev/docker/ocis.apps.yaml",
		"existing:\n  config:\n    llm:\n      endpoint: 'http://localhost'\n")
}

// initGitRepo creates a git repo, makes an initial commit with the given files,
// and returns the repo path.
func initGitRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	mustRunGit(t, dir, "init")
	mustRunGit(t, dir, "config", "user.email", "test@test.com")
	mustRunGit(t, dir, "config", "user.name", "Test")
	return dir
}

func mustRunGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func commitAll(t *testing.T, dir, msg string) {
	t.Helper()
	mustRunGit(t, dir, "add", "-A")
	mustRunGit(t, dir, "commit", "-m", msg)
}

// makeWireRepo sets up a git repo with wire fixtures already committed.
func makeWireRepo(t *testing.T) string {
	t.Helper()
	dir := initGitRepo(t)
	createWireFixtures(t, dir)
	commitAll(t, dir, "initial")
	return dir
}

func TestWireExtension_DockerCompose(t *testing.T) {
	dir := makeWireRepo(t)

	if err := WireExtension(dir, "new-ext"); err != nil {
		t.Fatalf("WireExtension: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dir, "docker-compose.yml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "web-app-new-ext/dist:/web/apps/new-ext") {
		t.Errorf("docker-compose.yml missing new mount:\n%s", data)
	}
}

func TestWireExtension_CIWorkflow(t *testing.T) {
	dir := makeWireRepo(t)

	if err := WireExtension(dir, "new-ext"); err != nil {
		t.Fatalf("WireExtension: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dir, ".github/workflows/test.yml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "- web-app-new-ext") {
		t.Errorf("test.yml missing new CI entry:\n%s", data)
	}
}

func TestWireExtension_SupportAppsYAML(t *testing.T) {
	dir := makeWireRepo(t)

	if err := WireExtension(dir, "new-ext"); err != nil {
		t.Fatalf("WireExtension: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dir, "support/actions/ocis.apps.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "web-app-new-ext:") {
		t.Errorf("support/actions/ocis.apps.yaml missing new entry:\n%s", data)
	}
}

func TestWireExtension_DevAppsYAML(t *testing.T) {
	dir := makeWireRepo(t)

	if err := WireExtension(dir, "new-ext"); err != nil {
		t.Fatalf("WireExtension: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dir, "dev/docker/ocis.apps.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "new-ext:") {
		t.Errorf("dev/docker/ocis.apps.yaml missing new entry:\n%s", data)
	}
}

func TestWireExtension_Idempotent(t *testing.T) {
	dir := makeWireRepo(t)

	if err := WireExtension(dir, "new-ext"); err != nil {
		t.Fatalf("first WireExtension: %v", err)
	}

	// Second call must succeed and not duplicate entries.
	if err := WireExtension(dir, "new-ext"); err != nil {
		t.Fatalf("second WireExtension: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dir, "docker-compose.yml"))
	if err != nil {
		t.Fatal(err)
	}
	count := strings.Count(string(data), "web-app-new-ext")
	if count != 1 {
		t.Errorf("expected 1 occurrence of 'web-app-new-ext' in docker-compose.yml, got %d:\n%s", count, data)
	}
}

func TestWireExtension_CommitsFiles(t *testing.T) {
	dir := makeWireRepo(t)

	if err := WireExtension(dir, "new-ext"); err != nil {
		t.Fatalf("WireExtension: %v", err)
	}

	// Count commits: initial + wiring = 2
	cmd := exec.Command("git", "log", "--oneline")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git log: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(lines) < 2 {
		t.Errorf("expected at least 2 commits (initial + wiring), got %d:\n%s", len(lines), out)
	}
	if !strings.Contains(string(out), "web-app-new-ext") {
		t.Errorf("wiring commit message should mention web-app-new-ext:\n%s", out)
	}
}

func TestWireExtension_PreservesExistingEntries(t *testing.T) {
	dir := makeWireRepo(t)

	if err := WireExtension(dir, "new-ext"); err != nil {
		t.Fatalf("WireExtension: %v", err)
	}

	// The original "existing" entry should still be there.
	data, err := os.ReadFile(filepath.Join(dir, "docker-compose.yml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "web-app-existing") {
		t.Errorf("docker-compose.yml lost the existing entry:\n%s", data)
	}
}
