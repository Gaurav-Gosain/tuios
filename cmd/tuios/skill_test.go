package main

import (
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/skills"
)

// A skill that documents a command the binary does not have is worse than no
// skill: an agent follows it, the command fails, and the agent has no way to
// tell a typo in the document from a broken tuios. These tests hold the skill to
// the CLI it describes.

// TestSkillCommandsResolve parses every tuios command the skill shows, in the
// core and in every topic, and resolves it against the real command tree: the
// subcommand must exist, its flags must exist, and its argument count must be
// accepted.
func TestSkillCommandsResolve(t *testing.T) {
	commands := tuiosCommandsIn(skills.All())
	if len(commands) < 150 {
		t.Fatalf("expected the skill to show many commands, found %d", len(commands))
	}

	for _, args := range commands {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			root := newRootCommand()
			cmd, rest, err := root.Find(args)
			if err != nil {
				t.Fatalf("no such command: %v", err)
			}
			if cmd == root && len(rest) > 0 && !strings.HasPrefix(rest[0], "-") {
				t.Fatalf("%q is not a tuios command", rest[0])
			}
			if err := cmd.ParseFlags(rest); err != nil {
				t.Fatalf("flags rejected: %v", err)
			}
			if err := cmd.ValidateArgs(cmd.Flags().Args()); err != nil {
				t.Fatalf("arguments rejected: %v", err)
			}
		})
	}
}

// TestSkillInlineCommandsResolve resolves the commands the skill names in prose
// rather than in a fence, so a rename cannot strand them.
func TestSkillInlineCommandsResolve(t *testing.T) {
	for _, name := range []string{"start-server", "attach", "run-command"} {
		root := newRootCommand()
		if _, _, err := root.Find([]string{name}); err != nil {
			t.Errorf("the skill names %q, which the tree does not have: %v", name, err)
		}
	}
}

// tuiosCommandsIn extracts every `tuios ...` invocation from the skill's shell
// code fences.
func tuiosCommandsIn(doc string) [][]string {
	var found [][]string
	for _, block := range shellBlocks(doc) {
		for _, words := range shellCalls(block) {
			if len(words) >= 2 && words[0] == "tuios" {
				found = append(found, words[1:])
			}
		}
	}
	return found
}

// shellBlocks returns the contents of the skill's ```sh fences.
func shellBlocks(doc string) []string {
	var blocks []string
	var current strings.Builder
	inBlock := false
	for line := range strings.SplitSeq(doc, "\n") {
		switch {
		case !inBlock && strings.HasPrefix(line, "```sh"):
			inBlock = true
			current.Reset()
		case inBlock && strings.HasPrefix(line, "```"):
			inBlock = false
			blocks = append(blocks, current.String())
		case inBlock:
			current.WriteString(line)
			current.WriteString("\n")
		}
	}
	return blocks
}

// shellCalls splits a shell snippet into argument lists the way a shell would:
// quotes group words and may span lines, and an unquoted newline, pipe or
// semicolon ends the call. It is not a shell parser, it is enough of one to read
// the commands a skill shows.
func shellCalls(block string) [][]string {
	var (
		calls     [][]string
		words     []string
		word      strings.Builder
		quote     rune
		staged    bool
		inComment bool
	)

	endWord := func() {
		if staged {
			words = append(words, word.String())
			word.Reset()
			staged = false
		}
	}
	endCall := func() {
		endWord()
		if len(words) > 0 {
			calls = append(calls, words)
			words = nil
		}
	}

	for _, r := range block {
		switch {
		case inComment:
			if r == '\n' {
				inComment = false
				endCall()
			}
		case quote != 0:
			if r == quote {
				quote = 0
				continue
			}
			word.WriteRune(r)
		case r == '\'' || r == '"':
			quote = r
			staged = true
		case r == '#' && !staged:
			inComment = true
		case r == ' ' || r == '\t':
			endWord()
		case r == '\n' || r == ';' || r == '|' || r == '&':
			endCall()
		default:
			word.WriteRune(r)
			staged = true
		}
	}
	endCall()
	return calls
}
