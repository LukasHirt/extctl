package build

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/LukasHirt/extctl/internal/config"
)

// ScaffoldOptions configures a scaffold run.
type ScaffoldOptions struct {
	Config       *config.Config
	CandidateID  string // e.g. "ai-folder-brief-sidebar"
	Title        string // e.g. "AI Folder Brief Sidebar"
	Description  string // short description for package.json
	WorktreePath string // absolute path to the git worktree
	LogPrefix    string
}

// ScaffoldExtension copies the scaffold template directory into
// packages/web-app-{ID}/ inside the worktree, substitutes template variables,
// edits the three registration files, runs pnpm install, and commits.
// It is idempotent: if the package directory already exists it returns nil.
func ScaffoldExtension(opts ScaffoldOptions) error {
	pkgDir := filepath.Join(opts.WorktreePath, "packages", "web-app-"+opts.CandidateID)

	// Idempotency: skip if already scaffolded.
	if _, err := os.Stat(pkgDir); err == nil {
		return nil
	}

	port, err := nextVitePort(opts.WorktreePath)
	if err != nil {
		return fmt.Errorf("detect vite port: %w", err)
	}

	vars := map[string]string{
		"{{EXT_ID}}":          opts.CandidateID,
		"{{EXT_TITLE}}":       opts.Title,
		"{{EXT_DESCRIPTION}}": opts.Description,
		"{{VITE_PORT}}":       strconv.Itoa(port),
	}

	scaffoldDir := opts.Config.ScaffoldDir
	if !filepath.IsAbs(scaffoldDir) {
		// ScaffoldDir is relative to the extctl working directory, not the worktree.
		abs, err := filepath.Abs(scaffoldDir)
		if err != nil {
			return fmt.Errorf("resolve scaffold dir: %w", err)
		}
		scaffoldDir = abs
	}

	if err := copyScaffold(scaffoldDir, pkgDir, vars); err != nil {
		return fmt.Errorf("copy scaffold: %w", err)
	}

	if err := addDockerMount(opts.WorktreePath, opts.CandidateID); err != nil {
		return fmt.Errorf("docker-compose.yml: %w", err)
	}
	if err := addDevAppsEntry(opts.WorktreePath, opts.CandidateID); err != nil {
		return fmt.Errorf("dev/docker/ocis.apps.yaml: %w", err)
	}
	if err := addSupportAppsEntry(opts.WorktreePath, opts.CandidateID); err != nil {
		return fmt.Errorf("support/actions/ocis.apps.yaml: %w", err)
	}

	if err := runCmd(opts.WorktreePath, "pnpm", "install"); err != nil {
		return fmt.Errorf("pnpm install: %w", err)
	}

	if err := gitCommitScaffold(opts.WorktreePath, opts.CandidateID); err != nil {
		return fmt.Errorf("git commit: %w", err)
	}

	return nil
}

// copyScaffold walks src and copies every file to dst, substituting vars.
func copyScaffold(src, dst string, vars map[string]string) error {
	return filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		return copyFileWithSubstitution(path, target, vars)
	})
}

// copyFileWithSubstitution copies src to dst, applying template variable substitution.
func copyFileWithSubstitution(src, dst string, vars map[string]string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	content := renderTemplate(string(data), vars)
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	return os.WriteFile(dst, []byte(content), 0o644)
}

// portRe matches lines like `port: 9731,` or `port: 9731` inside vite.config.ts.
var portRe = regexp.MustCompile(`(?m)^\s*port:\s*(\d+)`)

// nextVitePort scans packages/*/vite.config.ts for port: N values and returns max+1.
// Falls back to 9730 if no packages are found.
func nextVitePort(worktreePath string) (int, error) {
	pattern := filepath.Join(worktreePath, "packages", "*", "vite.config.ts")
	matches, err := filepath.Glob(pattern)
	if err != nil {
		return 0, err
	}

	max := 9729 // first extension gets 9730
	for _, f := range matches {
		data, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		for _, m := range portRe.FindAllStringSubmatch(string(data), -1) {
			n, err := strconv.Atoi(m[1])
			if err == nil && n > max {
				max = n
			}
		}
	}
	return max + 1, nil
}

// addDockerMount inserts a volume mount for the new extension into docker-compose.yml.
// It finds the last existing `./packages/web-app-*/dist:` line and inserts after it.
func addDockerMount(worktreePath, id string) error {
	path := filepath.Join(worktreePath, "docker-compose.yml")
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	content := string(data)

	newMount := fmt.Sprintf("      - ./packages/web-app-%s/dist:/web/apps/%s", id, id)
	if strings.Contains(content, newMount) {
		return nil // already present
	}

	lines := strings.Split(content, "\n")
	lastIdx := -1
	for i, line := range lines {
		if strings.Contains(line, "./packages/web-app-") && strings.Contains(line, "/dist:") {
			lastIdx = i
		}
	}
	if lastIdx == -1 {
		return fmt.Errorf("could not find existing web-app volume mount to insert after")
	}

	updated := make([]string, 0, len(lines)+1)
	updated = append(updated, lines[:lastIdx+1]...)
	updated = append(updated, newMount)
	updated = append(updated, lines[lastIdx+1:]...)

	return writeFileAtomic(path, []byte(strings.Join(updated, "\n")))
}

// addDevAppsEntry appends an LLM config entry to dev/docker/ocis.apps.yaml.
func addDevAppsEntry(worktreePath, id string) error {
	path := filepath.Join(worktreePath, "dev", "docker", "ocis.apps.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	content := string(data)

	key := id + ":"
	if strings.Contains(content, "\n"+key) || strings.HasPrefix(content, key) {
		return nil // already present
	}

	entry := fmt.Sprintf("\n%s:\n  config:\n    llm:\n      endpoint: 'https://host.docker.internal:9200/ai-llm-proxy/v1'\n      model: 'llama3.2'\n", id)
	if !strings.HasSuffix(content, "\n") {
		content += "\n"
	}
	return writeFileAtomic(path, []byte(content+entry))
}

// addSupportAppsEntry appends an LLM config entry to support/actions/ocis.apps.yaml.
func addSupportAppsEntry(worktreePath, id string) error {
	path := filepath.Join(worktreePath, "support", "actions", "ocis.apps.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	content := string(data)

	key := "web-app-" + id + ":"
	if strings.Contains(content, "\n"+key) || strings.HasPrefix(content, key) {
		return nil // already present
	}

	entry := fmt.Sprintf("\nweb-app-%s:\n  config:\n    llm:\n      endpoint: 'https://localhost:9200/ai-llm-proxy/v1'\n      model: 'llama3.2'\n", id)
	if !strings.HasSuffix(content, "\n") {
		content += "\n"
	}
	return writeFileAtomic(path, []byte(content+entry))
}

// runCmd runs a command in dir, piping stdout/stderr to the parent process.
// Replaced in tests to avoid invoking real pnpm/git binaries.
var runCmd = func(dir, name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Stdout = io.Discard
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// gitCommitScaffold stages scaffold files and creates the scaffold commit.
func gitCommitScaffold(worktreePath, id string) error {
	pkgPath := filepath.Join("packages", "web-app-"+id) + string(filepath.Separator)
	addArgs := []string{"add",
		pkgPath,
		"docker-compose.yml",
		"dev/docker/ocis.apps.yaml",
		"support/actions/ocis.apps.yaml",
		"pnpm-lock.yaml",
	}
	if err := runCmd(worktreePath, "git", addArgs...); err != nil {
		return fmt.Errorf("git add: %w", err)
	}
	msg := fmt.Sprintf("chore(web-app-%s): scaffold package", id)
	if err := runCmd(worktreePath, "git", "commit", "-s", "-m", msg); err != nil {
		return fmt.Errorf("git commit: %w", err)
	}
	return nil
}
