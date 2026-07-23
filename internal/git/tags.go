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

// FetchTag fetches exactly one already-known tag from origin, unlike
// FetchTags' "every tag in the remote" — used when a single specific
// release tag needs to be reachable locally without re-fetching the whole
// tag set.
func FetchTag(repoPath, tag string) error {
	return run(repoPath, "fetch", "origin", "tag", tag)
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

// PushTags pushes the named tags to origin, one tag per push. It is a no-op when
// no tags are given so callers can pass the result of a scan without guarding
// empty slices.
//
// Tags are pushed individually on purpose: GitHub does not deliver the `push`
// event (and therefore does not trigger tag-pattern workflows like release.yml)
// when more than three tags arrive in a single push. One push per tag keeps each
// release tag triggering its own workflow run.
func PushTags(repoPath string, tags ...string) error {
	for _, tag := range tags {
		if err := run(repoPath, "push", "origin", tag); err != nil {
			return fmt.Errorf("push tag %s: %w", tag, err)
		}
	}
	return nil
}
