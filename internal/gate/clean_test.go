package gate

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
)

func TestStrayPaths(t *testing.T) {
	tests := []struct {
		name        string
		porcelain   string
		extID       string
		wantRemove  []string
		wantRestore []string
	}{
		{
			name:      "clean tree",
			porcelain: "",
			extID:     "foo",
		},
		{
			name: "in-scope changes are ignored",
			porcelain: "" +
				" M packages/web-app-foo/src/index.ts\n" +
				"?? packages/web-app-foo/tests/unit/new.spec.ts\n",
			extID: "foo",
		},
		{
			name: "allowed root files are ignored",
			porcelain: "" +
				" M pnpm-lock.yaml\n" +
				" M docker-compose.yml\n" +
				" M dev/docker/ocis.apps.yaml\n" +
				" M support/actions/ocis.apps.yaml\n",
			extID: "foo",
		},
		{
			name:       "untracked stray file at worktree root",
			porcelain:  "?? scratch-debug.cjs\n",
			extID:      "foo",
			wantRemove: []string{"scratch-debug.cjs"},
		},
		{
			name:        "uncommitted edit to shared config is restored",
			porcelain:   " M .gitignore\n",
			extID:       "foo",
			wantRestore: []string{".gitignore"},
		},
		{
			name: "mixed in-scope and out-of-scope changes",
			porcelain: "" +
				" M packages/web-app-foo/src/index.ts\n" +
				"?? scratch-debug.cjs\n" +
				" M .gitignore\n",
			extID:       "foo",
			wantRemove:  []string{"scratch-debug.cjs"},
			wantRestore: []string{".gitignore"},
		},
		{
			name:       "rename reports only the new path",
			porcelain:  "R  old-scratch.cjs -> scratch-debug.cjs\n",
			extID:      "foo",
			wantRemove: nil,
			// Renames of tracked files show status "R ", not "??", so they're a restore, not a remove.
			wantRestore: []string{"scratch-debug.cjs"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotRemove, gotRestore := strayPaths(tt.porcelain, tt.extID)
			if !reflect.DeepEqual(gotRemove, tt.wantRemove) {
				t.Errorf("toRemove = %v, want %v", gotRemove, tt.wantRemove)
			}
			if !reflect.DeepEqual(gotRestore, tt.wantRestore) {
				t.Errorf("toRestore = %v, want %v", gotRestore, tt.wantRestore)
			}
		})
	}
}

func TestCleanStrayFiles_RemovesUntrackedAndRestoresTracked(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "scratch-debug.cjs"), []byte("junk"), 0o644); err != nil {
		t.Fatal(err)
	}

	var checkoutArgs [][]string
	origExec := execCommand
	t.Cleanup(func() { execCommand = origExec })
	execCommand = func(name string, args ...string) *exec.Cmd {
		stdout := ""
		if len(args) > 0 && args[0] == "status" {
			stdout = "?? scratch-debug.cjs\n M .gitignore\n"
		} else {
			checkoutArgs = append(checkoutArgs, append([]string{}, args...))
		}
		cmd := exec.Command(os.Args[0], "-test.run=TestHelperProcess", "--")
		cmd.Env = append(os.Environ(), "GO_WANT_HELPER_PROCESS=1", "GO_HELPER_STDOUT="+stdout)
		return cmd
	}

	cleaned, err := cleanStrayFiles(dir, "foo")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if _, err := os.Stat(filepath.Join(dir, "scratch-debug.cjs")); !os.IsNotExist(err) {
		t.Errorf("expected scratch-debug.cjs to be removed, stat err = %v", err)
	}

	wantCleaned := []string{"scratch-debug.cjs", ".gitignore"}
	if !reflect.DeepEqual(cleaned, wantCleaned) {
		t.Errorf("cleaned = %v, want %v", cleaned, wantCleaned)
	}

	wantCheckout := [][]string{{"checkout", "--", ".gitignore"}}
	if !reflect.DeepEqual(checkoutArgs, wantCheckout) {
		t.Errorf("checkout calls = %v, want %v", checkoutArgs, wantCheckout)
	}
}

func TestCleanStrayFiles_NothingToClean(t *testing.T) {
	dir := t.TempDir()
	execCommand = outputExec(t, "")

	cleaned, err := cleanStrayFiles(dir, "foo")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cleaned) != 0 {
		t.Errorf("cleaned = %v, want empty", cleaned)
	}
}
