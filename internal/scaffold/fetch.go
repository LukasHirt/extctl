package scaffold

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// FetchOptions configures a scaffold fetch operation.
type FetchOptions struct {
	// Source is the Git URL to clone (e.g. https://github.com/owncloud/web-app-skeleton).
	Source string
	// Exclude is a list of path prefixes (relative to the clone root) to skip.
	// A prefix ending in "/" matches the whole directory tree; otherwise it
	// matches the exact relative path.
	Exclude []string
	// DestDir is the local scaffold directory to copy files into.
	DestDir string
	// Depth is the git clone depth. 0 means --depth 1.
	Depth int
}

// Fetch clones Source into a temp dir, removes .git/, applies exclusions, and
// copies the remaining files into DestDir. Existing files in DestDir that are
// not present in the clone are left untouched (our custom additions survive).
func Fetch(opts FetchOptions) error {
	depth := opts.Depth
	if depth <= 0 {
		depth = 1
	}

	// Clone into a temp dir.
	tmpDir, err := os.MkdirTemp("", "extctl-scaffold-*")
	if err != nil {
		return fmt.Errorf("create temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	fmt.Printf("scaffold: cloning %s (depth %d)…\n", opts.Source, depth)
	cmd := exec.Command("git", "clone",
		"--depth", fmt.Sprintf("%d", depth),
		"--single-branch",
		opts.Source,
		tmpDir,
	)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("git clone %s: %w", opts.Source, err)
	}

	// Strip the nested .git directory so the scaffold directory is not a repo.
	if err := os.RemoveAll(filepath.Join(tmpDir, ".git")); err != nil {
		return fmt.Errorf("remove .git: %w", err)
	}

	// Ensure dest dir exists.
	if err := os.MkdirAll(opts.DestDir, 0o755); err != nil {
		return fmt.Errorf("mkdir dest: %w", err)
	}

	// Walk the clone and copy files that pass the exclusion filter.
	copied, skipped := 0, 0
	err = filepath.WalkDir(tmpDir, func(srcPath string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		rel, relErr := filepath.Rel(tmpDir, srcPath)
		if relErr != nil {
			return relErr
		}
		if rel == "." {
			return nil
		}

		if isExcluded(rel, opts.Exclude) {
			skipped++
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}

		dstPath := filepath.Join(opts.DestDir, rel)
		if d.IsDir() {
			return os.MkdirAll(dstPath, 0o755)
		}

		if err := copyFile(srcPath, dstPath); err != nil {
			return fmt.Errorf("copy %s: %w", rel, err)
		}
		copied++
		return nil
	})
	if err != nil {
		return fmt.Errorf("walk clone: %w", err)
	}

	fmt.Printf("scaffold: copied %d files (%d paths excluded)\n", copied, skipped)
	fmt.Printf("scaffold: dest: %s\n", opts.DestDir)
	return nil
}

// isExcluded reports whether the given relative path should be skipped.
// Pattern rules:
//   - "foo/"  → skip anything under the directory named foo (and the dir itself)
//   - "foo"   → skip the exact relative path foo
func isExcluded(rel string, exclude []string) bool {
	rel = filepath.ToSlash(rel)

	for _, pattern := range exclude {
		p := filepath.ToSlash(pattern)

		if dirName, ok := strings.CutSuffix(p, "/"); ok {
			// Directory prefix: skip dir itself and all descendants.
			if rel == dirName || strings.HasPrefix(rel, dirName+"/") {
				return true
			}
		} else {
			if rel == p {
				return true
			}
		}
	}
	return false
}

func copyFile(src, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	info, err := in.Stat()
	if err != nil {
		return err
	}

	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, info.Mode())
	if err != nil {
		return err
	}
	defer out.Close()

	_, err = io.Copy(out, in)
	return err
}
