package main

import (
	"io"
	"os"
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/Gaurav-Gosain/tuios/skills"
)

// A skill that documents a command the binary does not have is worse than no
// skill: an agent follows it, the command fails, and the agent has no way to
// tell a typo in the document from a broken tuios. These tests hold the skill to
// the CLI it describes.

// TestSkillIsEmbeddedFromTheRepoFile fails if the embed ever stops pointing at
// the file in the tree, which is the only way the printed copy and the reviewed
// copy could disagree.
func TestSkillIsEmbeddedFromTheRepoFile(t *testing.T) {
	onDisk, err := os.ReadFile("../../skills/tuios/SKILL.md")
	if err != nil {
		t.Fatalf("read skills/tuios/SKILL.md: %v", err)
	}
	if skills.TUIOS != string(onDisk) {
		t.Error("the embedded skill differs from skills/tuios/SKILL.md")
	}
	if !strings.HasPrefix(skills.TUIOS, "---\nname: tuios\n") {
		t.Error("the skill is missing its frontmatter")
	}
}

// TestSkillFlagPrintsTheSkill runs the root command with --skill and checks it
// writes the skill and nothing else, without reaching the code that would draw
// an interface.
//
// The pipe is drained while the command runs, not after it returns. A pipe holds
// 64KB and the skill passed that, so a reader that waits for Execute deadlocks:
// the write blocks with the buffer full and the only thing that would empty it
// is the read that has not started. Nothing about the claim changes, and the
// test no longer fails the day the document gets longer.
func TestSkillFlagPrintsTheSkill(t *testing.T) {
	read, write, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	stdout := os.Stdout
	os.Stdout = write
	defer func() { os.Stdout = stdout }()

	type capture struct {
		out []byte
		err error
	}
	done := make(chan capture, 1)
	go func() {
		out, err := io.ReadAll(read)
		done <- capture{out: out, err: err}
	}()

	root := newRootCommand()
	root.SetArgs([]string{"--skill"})
	runErr := root.Execute()
	_ = write.Close()

	got := <-done
	if got.err != nil {
		t.Fatalf("read stdout: %v", got.err)
	}
	if runErr != nil {
		t.Fatalf("tuios --skill failed: %v", runErr)
	}
	if string(got.out) != skills.TUIOS {
		t.Errorf("--skill printed %d bytes, want the %d-byte skill", len(got.out), len(skills.TUIOS))
	}
}

// TestSkillCommandsResolve parses every tuios command the skill shows and
// resolves it against the real command tree: the subcommand must exist, its
// flags must exist, and its argument count must be accepted.
func TestSkillCommandsResolve(t *testing.T) {
	commands := tuiosCommandsIn(skills.TUIOS)
	if len(commands) < 25 {
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

// TestSkillDocumentsTheReportingPath keeps the one thing the skill exists to
// make happen from being edited away: a pane telling tuios what it is doing,
// through a hook shim, an OSC 9;4 progress report, or a call by hand.
func TestSkillDocumentsTheReportingPath(t *testing.T) {
	for _, want := range []string{
		"tuios set-agent-state working",
		"$TUIOS_PANE_ID",
		"--harness",
		"TUIOS_ENV",
		"integrations/claude-code/",
		"OSC 9;4",
		"--source",
		"Not applied: a higher-ranked source owns this pane",
	} {
		if !strings.Contains(skills.TUIOS, want) {
			t.Errorf("the skill no longer mentions %q", want)
		}
	}
}

// TestSkillDocumentsTheDiskLifecycle holds the skill to the daemon lifecycle
// contract a scripted caller depends on: the ls exit code that distinguishes a
// stopped daemon, the saved and restored markers with the wording the code
// prints, and the command that restores without attaching.
func TestSkillDocumentsTheDiskLifecycle(t *testing.T) {
	for _, want := range []string{
		"exit",
		session.SavedNote,
		session.RestoredNote,
		"tuios start-server",
		"tuios attach",
	} {
		if !strings.Contains(skills.TUIOS, want) {
			t.Errorf("the skill no longer mentions %q", want)
		}
	}
}

// TestSkillDocumentsRicing holds the ricing section to the two things that make
// it usable by an agent rather than a person: where a theme file goes, and that
// what it just applied can be measured instead of looked at. Both were absent,
// and a theme was the one part of the appearance the skill never mentioned at
// all.
func TestSkillDocumentsRicing(t *testing.T) {
	for _, want := range []string{
		"tuios list-themes",
		"tuios import-theme",
		"themes dir:",
		"illegible",
		"### What this cannot do",
	} {
		if !strings.Contains(skills.TUIOS, want) {
			t.Errorf("the skill no longer mentions %q", want)
		}
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
