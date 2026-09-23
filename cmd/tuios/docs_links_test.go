package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"unicode"
)

// TestAgentGuideLinksResolve holds docs/AGENT_STATE.md, the guide to agents
// in tuios, to the anchors it links. The guide's map sends a reader to a
// section by name, here and in the other docs, and a heading renamed anywhere
// would leave the map pointing at nothing without a word from the build.
func TestAgentGuideLinksResolve(t *testing.T) {
	const guide = "AGENT_STATE.md"
	doc := readDoc(t, guide)
	links := regexp.MustCompile(`\]\(([A-Za-z_]*\.md)?#([^)]+)\)`).FindAllStringSubmatch(doc, -1)
	if len(links) < 40 {
		t.Fatalf("found %d anchor links in %s, want the guide's map and the reference's cross links", len(links), guide)
	}
	for _, m := range links {
		file, anchor := m[1], m[2]
		if file == "" {
			file = guide
		}
		if !hasAnchor(readDoc(t, file), anchor) {
			t.Errorf("%s links %s#%s, which has no such heading", guide, file, anchor)
		}
	}
}

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

// hasAnchor reports whether a markdown document has a heading whose GitHub
// anchor is anchor: lower case, backticks and punctuation dropped, spaces
// turned into hyphens.
func hasAnchor(doc, anchor string) bool {
	inFence := false
	for line := range strings.SplitSeq(doc, "\n") {
		if strings.HasPrefix(line, "```") {
			inFence = !inFence
			continue
		}
		if inFence || !strings.HasPrefix(line, "#") {
			continue
		}
		text := strings.TrimSpace(strings.TrimLeft(line, "#"))
		var slug strings.Builder
		for _, r := range strings.ToLower(text) {
			switch {
			case r == ' ':
				slug.WriteRune('-')
			case r == '-' || r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r):
				slug.WriteRune(r)
			}
		}
		if slug.String() == anchor {
			return true
		}
	}
	return false
}
