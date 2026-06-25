package build

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeStages(t *testing.T, dir, content string) string {
	t.Helper()
	p := filepath.Join(dir, "stages.md")
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func readStages(t *testing.T, p string) string {
	t.Helper()
	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestParseStages(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    []string
	}{
		{
			name:    "empty file",
			content: "",
			want:    nil,
		},
		{
			name:    "unchecked stages",
			content: "- [ ] 1. Write tests\n- [ ] 2. Ship it\n",
			want:    []string{"Write tests", "Ship it"},
		},
		{
			name:    "mixed checked and unchecked",
			content: "- [x] 1. Done\n- [ ] 2. Todo\n",
			want:    []string{"Done", "Todo"},
		},
		{
			name:    "non-stage lines ignored",
			content: "# Header\n\nSome prose\n- [ ] 1. Stage One\n",
			want:    []string{"Stage One"},
		},
		{
			name:    "windows line endings",
			content: "- [ ] 1. First\r\n- [ ] 2. Second\r\n",
			want:    []string{"First", "Second"},
		},
		{
			name:    "three stages",
			content: "- [ ] 1. One\n- [ ] 2. Two\n- [ ] 3. Three\n",
			want:    []string{"One", "Two", "Three"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			p := writeStages(t, dir, tt.content)
			got, err := ParseStages(p)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(got) != len(tt.want) {
				t.Fatalf("got %d stages %v, want %d %v", len(got), got, len(tt.want), tt.want)
			}
			for i, w := range tt.want {
				if got[i] != w {
					t.Errorf("stage[%d] = %q, want %q", i, got[i], w)
				}
			}
		})
	}
}

func TestCheckStage(t *testing.T) {
	t.Run("mark stage 1", func(t *testing.T) {
		dir := t.TempDir()
		p := writeStages(t, dir, "- [ ] 1. First\n- [ ] 2. Second\n")
		if err := CheckStage(p, 1); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		content := readStages(t, p)
		if !strings.Contains(content, "- [x] 1. First") {
			t.Errorf("stage 1 not checked: %q", content)
		}
		if !strings.Contains(content, "- [ ] 2. Second") {
			t.Errorf("stage 2 should still be unchecked: %q", content)
		}
	})

	t.Run("mark stage 2", func(t *testing.T) {
		dir := t.TempDir()
		p := writeStages(t, dir, "- [ ] 1. First\n- [ ] 2. Second\n")
		if err := CheckStage(p, 2); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		content := readStages(t, p)
		if !strings.Contains(content, "- [ ] 1. First") {
			t.Errorf("stage 1 should remain unchecked: %q", content)
		}
		if !strings.Contains(content, "- [x] 2. Second") {
			t.Errorf("stage 2 not checked: %q", content)
		}
	})

	t.Run("already checked returns error", func(t *testing.T) {
		dir := t.TempDir()
		p := writeStages(t, dir, "- [x] 1. Already Done\n")
		if err := CheckStage(p, 1); err == nil {
			t.Error("expected error for already-checked stage, got nil")
		}
	})

	t.Run("stage not present returns error", func(t *testing.T) {
		dir := t.TempDir()
		p := writeStages(t, dir, "- [ ] 1. Only One\n")
		if err := CheckStage(p, 3); err == nil {
			t.Error("expected error for missing stage 3, got nil")
		}
	})

	t.Run("no tmp file left on success", func(t *testing.T) {
		dir := t.TempDir()
		p := writeStages(t, dir, "- [ ] 1. Stage\n")
		if err := CheckStage(p, 1); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range entries {
			if e.Name() != "stages.md" {
				t.Errorf("unexpected file left: %s", e.Name())
			}
		}
	})
}

func TestAppendDocStage(t *testing.T) {
	docText := "Update README.md (and CLAUDE.md if present) for the extension"

	t.Run("one stage appends as stage 2", func(t *testing.T) {
		dir := t.TempDir()
		p := writeStages(t, dir, "- [ ] 1. Write code\n")
		if err := AppendDocStage(p); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		content := readStages(t, p)
		if !strings.Contains(content, "- [ ] 2. "+docText) {
			t.Errorf("doc stage not appended correctly:\n%s", content)
		}
	})

	t.Run("two stages appends as stage 3", func(t *testing.T) {
		dir := t.TempDir()
		p := writeStages(t, dir, "- [ ] 1. One\n- [ ] 2. Two\n")
		if err := AppendDocStage(p); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		content := readStages(t, p)
		if !strings.Contains(content, "- [ ] 3. "+docText) {
			t.Errorf("doc stage not appended as stage 3:\n%s", content)
		}
	})

	t.Run("idempotent when doc stage already last", func(t *testing.T) {
		dir := t.TempDir()
		initial := "- [ ] 1. One\n- [ ] 2. " + docText + "\n"
		p := writeStages(t, dir, initial)
		if err := AppendDocStage(p); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		content := readStages(t, p)
		// Should still only have 2 stages.
		stages, err := ParseStages(p)
		if err != nil {
			t.Fatal(err)
		}
		if len(stages) != 2 {
			t.Errorf("got %d stages after idempotent call, want 2:\n%s", len(stages), content)
		}
	})

	t.Run("file without trailing newline", func(t *testing.T) {
		dir := t.TempDir()
		p := writeStages(t, dir, "- [ ] 1. One")
		if err := AppendDocStage(p); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		content := readStages(t, p)
		if !strings.Contains(content, "- [ ] 2. "+docText) {
			t.Errorf("doc stage not appended:\n%s", content)
		}
	})
}
