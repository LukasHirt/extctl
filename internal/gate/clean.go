package gate

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// allowedRootPaths lists root-level paths outside packages/web-app-<extID>/ that a
// build/repair session may legitimately leave modified. Keep in sync with the
// hygiene allowlist in gate/run-gate.sh.
var allowedRootPaths = map[string]bool{
	"pnpm-lock.yaml":                 true,
	"docker-compose.yml":             true,
	"dev/docker/ocis.apps.yaml":      true,
	"support/actions/ocis.apps.yaml": true,
}

// strayPaths splits `git status --porcelain` output into paths that fall outside
// packages/web-app-<extID>/ and the allowed root paths: toRemove are untracked
// (safe to delete outright), toRestore are tracked files with uncommitted edits
// (safe to discard back to HEAD). Paths inside scope are left alone entirely.
func strayPaths(porcelain, extID string) (toRemove, toRestore []string) {
	packagePrefix := fmt.Sprintf("packages/web-app-%s/", extID)

	for _, line := range strings.Split(porcelain, "\n") {
		if len(line) < 4 {
			continue
		}
		status := line[:2]
		path := strings.TrimSpace(line[3:])
		// Renames report as "old -> new"; only the resulting path matters here.
		if idx := strings.Index(path, " -> "); idx != -1 {
			path = path[idx+len(" -> "):]
		}
		if strings.HasPrefix(path, packagePrefix) || allowedRootPaths[path] {
			continue
		}
		if status == "??" {
			toRemove = append(toRemove, path)
		} else {
			toRestore = append(toRestore, path)
		}
	}
	return toRemove, toRestore
}

// cleanStrayFiles removes untracked files/dirs and discards uncommitted edits to
// tracked files that fall outside packages/web-app-<extID>/ and the allowed root
// paths, before the hygiene stage gets a chance to see them. A build or repair
// session is sandboxed to Read/Edit/Write plus a narrow Bash allowlist and has no
// generic way to clean up an artifact it accidentally created outside its own
// package directory (e.g. a debug script written at the worktree root); left
// alone, that stray file fails hygiene every time and can tempt a repair session
// into working around it by editing shared repo config instead. Running this
// before every gate invocation removes the artifact automatically so hygiene
// never sees it and no workaround is ever needed.
func cleanStrayFiles(worktreePath, extID string) ([]string, error) {
	cmd := execCommand("git", "status", "--porcelain")
	cmd.Dir = worktreePath
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git status --porcelain: %w", err)
	}

	toRemove, toRestore := strayPaths(string(out), extID)

	var cleaned []string
	for _, path := range toRemove {
		if err := os.RemoveAll(filepath.Join(worktreePath, path)); err != nil {
			return cleaned, fmt.Errorf("remove stray file %s: %w", path, err)
		}
		cleaned = append(cleaned, path)
	}
	for _, path := range toRestore {
		restoreCmd := execCommand("git", "checkout", "--", path)
		restoreCmd.Dir = worktreePath
		if err := restoreCmd.Run(); err != nil {
			return cleaned, fmt.Errorf("restore stray edit to %s: %w", path, err)
		}
		cleaned = append(cleaned, path)
	}
	return cleaned, nil
}
