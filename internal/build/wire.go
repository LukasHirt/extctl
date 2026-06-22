package build

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// WireExtension registers the new extension in the four shared infra files:
//
//   - docker-compose.yml                (volume mount; mount target = extID, no prefix)
//   - .github/workflows/test.yml        (CI matrix; entry = web-app-{extID})
//   - support/actions/ocis.apps.yaml    (CI oCIS config; key = web-app-{extID})
//   - dev/docker/ocis.apps.yaml         (local oCIS config; key = extID, no prefix)
//
// extID is the short candidate ID without the "web-app-" prefix (e.g. "ai-doc-summary").
// This matches what the gate and build-stage prompts use as EXT_ID.
//
// All edits are idempotent — safe to call again on crash/resume. A chore commit
// is added only if at least one file actually changed.
// repoPath is the absolute path to the web-extensions worktree.
func WireExtension(repoPath, extID string) error {
	changed := false

	ok, err := wireDockerCompose(repoPath, extID)
	if err != nil {
		return fmt.Errorf("wire docker-compose.yml: %w", err)
	}
	changed = changed || ok

	ok, err = wireCI(repoPath, extID)
	if err != nil {
		return fmt.Errorf("wire test.yml: %w", err)
	}
	changed = changed || ok

	ok, err = wireAppsYAML(
		filepath.Join(repoPath, "support/actions/ocis.apps.yaml"),
		"web-app-"+extID, // CI mounts at /apps/web-app-{extID} — key must match
	)
	if err != nil {
		return fmt.Errorf("wire support/actions/ocis.apps.yaml: %w", err)
	}
	changed = changed || ok

	ok, err = wireAppsYAML(
		filepath.Join(repoPath, "dev/docker/ocis.apps.yaml"),
		extID, // docker-compose mounts at /web/apps/{extID} — key must match
	)
	if err != nil {
		return fmt.Errorf("wire dev/docker/ocis.apps.yaml: %w", err)
	}
	changed = changed || ok

	if !changed {
		return nil // already wired (idempotent re-entry after crash)
	}

	if err := wireGitRun(repoPath,
		"add",
		"docker-compose.yml",
		".github/workflows/test.yml",
		"support/actions/ocis.apps.yaml",
		"dev/docker/ocis.apps.yaml",
	); err != nil {
		return fmt.Errorf("git add wiring files: %w", err)
	}
	msg := fmt.Sprintf("chore(web-app-%s): register in docker-compose, CI matrix, and oCIS apps config", extID)
	if err := wireGitRun(repoPath, "commit", "-s", "-m", msg); err != nil {
		return fmt.Errorf("git commit wiring: %w", err)
	}
	return nil
}

// wireDockerCompose inserts the new extension's dist volume after the last
// existing app mount. Returns true if a change was made.
//
// Pattern: ./packages/web-app-{extID}/dist:/web/apps/{extID}
func wireDockerCompose(repoPath, extID string) (bool, error) {
	path := filepath.Join(repoPath, "docker-compose.yml")
	data, err := os.ReadFile(path)
	if err != nil {
		return false, fmt.Errorf("read: %w", err)
	}

	newLine := fmt.Sprintf("      - ./packages/web-app-%s/dist:/web/apps/%s", extID, extID)
	if strings.Contains(string(data), newLine) {
		return false, nil
	}

	lines := strings.Split(string(data), "\n")
	lastIdx := -1
	for i, line := range lines {
		if strings.HasPrefix(strings.TrimRight(line, " \t"), "      - ./packages/") {
			lastIdx = i
		}
	}
	if lastIdx == -1 {
		return false, fmt.Errorf("no app volume mounts found in docker-compose.yml")
	}

	out := make([]string, 0, len(lines)+1)
	out = append(out, lines[:lastIdx+1]...)
	out = append(out, newLine)
	out = append(out, lines[lastIdx+1:]...)
	return true, writeFileAtomic(path, []byte(strings.Join(out, "\n")))
}

// wireCI appends "web-app-{extID}" to the test.yml matrix.app list after the
// last existing entry. Returns true if a change was made.
func wireCI(repoPath, extID string) (bool, error) {
	path := filepath.Join(repoPath, ".github/workflows/test.yml")
	data, err := os.ReadFile(path)
	if err != nil {
		return false, fmt.Errorf("read: %w", err)
	}

	newLine := fmt.Sprintf("          - web-app-%s", extID)
	if strings.Contains(string(data), newLine) {
		return false, nil
	}

	lines := strings.Split(string(data), "\n")
	lastIdx := -1
	for i, line := range lines {
		// Matrix entries sit at exactly 10 spaces of indent followed by "- ".
		if strings.HasPrefix(line, "          - ") {
			lastIdx = i
		}
	}
	if lastIdx == -1 {
		return false, fmt.Errorf("no matrix entries found in test.yml")
	}

	out := make([]string, 0, len(lines)+1)
	out = append(out, lines[:lastIdx+1]...)
	out = append(out, newLine)
	out = append(out, lines[lastIdx+1:]...)
	return true, writeFileAtomic(path, []byte(strings.Join(out, "\n")))
}

// wireAppsYAML appends an LLM config entry to the given ocis.apps.yaml file
// using appKey as the top-level key. Returns true if a change was made.
//
// For support/actions/ocis.apps.yaml the caller passes "web-app-{extID}" (CI
// mounts at /apps/web-app-{extID}). For dev/docker/ocis.apps.yaml the caller
// passes "{extID}" (docker-compose mounts at /web/apps/{extID}).
func wireAppsYAML(path, appKey string) (bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return false, fmt.Errorf("read %s: %w", path, err)
	}

	keyPrefix := appKey + ":"
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(strings.TrimRight(line, " \t"), keyPrefix) {
			return false, nil // entry already present
		}
	}

	content := string(data)
	if !strings.HasSuffix(content, "\n") {
		content += "\n"
	}
	content += fmt.Sprintf(
		"\n%s:\n  config:\n    llm:\n      endpoint: 'https://localhost:9200/ai-llm-proxy/v1'\n      model: 'llama3.2'\n",
		appKey,
	)
	return true, writeFileAtomic(path, []byte(content))
}

func wireGitRun(repoPath string, args ...string) error {
	cmd := exec.Command("git", args...)
	cmd.Dir = repoPath
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git %s: %w\n%s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return nil
}
