package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestAgentGuideCommandsResolve resolves every tuios command in the fences of
// the guide's front part, the part a person follows to set things up, against
// the real command tree.
func TestAgentGuideCommandsResolve(t *testing.T) {
	doc := readDoc(t, "AGENT_STATE.md")
	front, _, ok := strings.Cut(doc, "\n## Reference\n")
	if !ok {
		t.Fatal("AGENT_STATE.md has no Reference section to end the guide")
	}
	// The setup steps are a numbered list, so their fences are indented.
	var flat strings.Builder
	for line := range strings.SplitSeq(front, "\n") {
		flat.WriteString(strings.TrimLeft(line, " "))
		flat.WriteString("\n")
	}
	commands := tuiosCommandsIn(strings.ReplaceAll(flat.String(), "```bash", "```sh"))
	if len(commands) < 4 {
		t.Fatalf("found %d commands in the guide, want its setup steps", len(commands))
	}
	for _, args := range commands {
		root := newRootCommand()
		cmd, rest, err := root.Find(args)
		if err != nil {
			t.Errorf("tuios %v: no such command: %v", args, err)
			continue
		}
		if err := cmd.ParseFlags(rest); err != nil {
			t.Errorf("tuios %v: flags rejected: %v", args, err)
			continue
		}
		if err := cmd.ValidateArgs(cmd.Flags().Args()); err != nil {
			t.Errorf("tuios %v: arguments rejected: %v", args, err)
		}
	}
}

// readDoc returns a file under docs/.
func readDoc(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "docs", name))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}
