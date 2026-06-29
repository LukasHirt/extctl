package git

import (
	"fmt"
	"os/exec"
	"strings"
)

// FetchTags fetches origin including all tags so the local view of branches and
// tags is current before scanning for releases.
func FetchTags(repoPath string) error {
	return run(repoPath, "fetch", "origin", "--tags")
}

// Output runs a read-only git command in the given repo and returns its stdout.
// Used for inspection commands (ls-tree, show, tag -l) whose output we parse.
func Output(repoPath string, args ...string) ([]byte, error) {
	out, err := exec.Command("git", append([]string{"-C", repoPath}, args...)...).Output()
	if err != nil {
		stderr := ""
		if ee, ok := err.(*exec.ExitError); ok {
			stderr = strings.TrimSpace(string(ee.Stderr))
		}
		return nil, fmt.Errorf("git %s: %w\n%s", strings.Join(args, " "), err, stderr)
	}
	return out, nil
}

// CreateSignedTag creates an annotated, signed tag (git tag -s) in the repo.
func CreateSignedTag(repoPath, tag, message string) error {
	return run(repoPath, "tag", "-s", tag, "-m", message)
}

// PushTags pushes the named tags to origin. It is a no-op when no tags are given
// so callers can pass the result of a scan without guarding empty slices.
func PushTags(repoPath string, tags ...string) error {
	if len(tags) == 0 {
		return nil
	}
	return run(repoPath, append([]string{"push", "origin"}, tags...)...)
}
