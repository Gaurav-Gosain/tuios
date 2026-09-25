package main

import (
	"io"
	"os"
	"slices"
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

	// And every topic, which is embedded by a glob rather than by name.
	entries, err := os.ReadDir("../../skills/tuios")
	if err != nil {
		t.Fatal(err)
	}
	var onDiskTopics []string
	for _, e := range entries {
		name, ok := strings.CutSuffix(e.Name(), ".md")
		if !ok || name == "SKILL" {
			continue
		}
		onDiskTopics = append(onDiskTopics, name)
		data, err := os.ReadFile("../../skills/tuios/" + e.Name())
		if err != nil {
			t.Fatal(err)
		}
		if got := skillText(t, name); got != string(data) {
			t.Errorf("the embedded topic %s differs from skills/tuios/%s", name, e.Name())
		}
	}
	if !slices.Equal(onDiskTopics, skills.TopicNames()) {
		t.Errorf("topics on disk %v, embedded %v", onDiskTopics, skills.TopicNames())
	}
}

// skillText returns what `tuios --skill <topic>` prints, failing the test for
// a topic that does not exist.
func skillText(t *testing.T, topic string) string {
	t.Helper()
	text, err := skills.Lookup(topic)
	if err != nil {
		t.Fatal(err)
	}
	return text
}

// runSkillFlag runs the root command with args, rewritten the way main
// rewrites them, and returns what it printed and the error it returned.
//
// The pipe is drained while the command runs, not after it returns. A pipe holds
// 64KB and the skill passed that, so a reader that waits for Execute deadlocks:
// the write blocks with the buffer full and the only thing that would empty it
// is the read that has not started.
func runSkillFlag(t *testing.T, args ...string) (string, error) {
	t.Helper()
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
	root.SetArgs(skillArgs(root, args))
	root.SilenceErrors = true
	runErr := root.Execute()
	_ = write.Close()

	got := <-done
	if got.err != nil {
		t.Fatalf("read stdout: %v", got.err)
	}
	return string(got.out), runErr
}

// TestSkillFlagPrintsATopic checks `tuios --skill <topic>` for every topic,
// written with a space as a person types it. mcp, hosts and tmux are also
// subcommands, so without skillArgs the topic would run the subcommand.
func TestSkillFlagPrintsATopic(t *testing.T) {
	topics := skills.Topics()
	if len(topics) < 10 {
		t.Fatalf("found %d topics, want the split skill", len(topics))
	}
	for _, topic := range topics {
		out, err := runSkillFlag(t, "--skill", topic.Name)
		if err != nil {
			t.Fatalf("tuios --skill %s failed: %v", topic.Name, err)
		}
		if out != topic.Text {
			t.Errorf("tuios --skill %s printed %d bytes, want the %d-byte topic", topic.Name, len(out), len(topic.Text))
		}
	}

	out, err := runSkillFlag(t, "--skill", "all")
	if err != nil {
		t.Fatalf("tuios --skill all failed: %v", err)
	}
	if out != skills.All() || !strings.HasPrefix(out, skills.TUIOS[:200]) {
		t.Error("tuios --skill all did not print the core and every topic")
	}
	for _, topic := range topics {
		if !strings.Contains(out, topic.Text) {
			t.Errorf("tuios --skill all is missing the %s topic", topic.Name)
		}
	}

	_, err = runSkillFlag(t, "--skill", "nosuchtopic")
	if err == nil || !strings.Contains(err.Error(), "mail") {
		t.Errorf("an unknown topic gave %v, want an error that lists the topics", err)
	}
}

// TestSkillArgs pins the rewrite: only the root's own --skill, and never an
// argument after a subcommand or after --.
func TestSkillArgs(t *testing.T) {
	root := newRootCommand()
	for _, c := range []struct{ in, want []string }{
		{[]string{"--skill"}, []string{"--skill"}},
		{[]string{"--skill", "mcp"}, []string{"--skill=mcp"}},
		{[]string{"--debug", "--skill", "hosts"}, []string{"--debug", "--skill=hosts"}},
		{[]string{"--skill", "--debug"}, []string{"--skill", "--debug"}},
		{[]string{"send-text", "--skill", "mcp"}, []string{"send-text", "--skill", "mcp"}},
		{[]string{"--", "--skill", "mcp"}, []string{"--", "--skill", "mcp"}},
	} {
		if got := skillArgs(root, c.in); !slices.Equal(got, c.want) {
			t.Errorf("skillArgs(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestSkillCoreIsTheCore keeps the core small enough to load every time, and
// holds its topic table to the topics that exist, in both directions.
func TestSkillCoreIsTheCore(t *testing.T) {
	lines := strings.Count(skills.TUIOS, "\n")
	if lines > 320 {
		t.Errorf("the core skill is %d lines; keep it under 320 and move detail to a topic", lines)
	}
	for _, topic := range skills.Topics() {
		if !strings.Contains(skills.TUIOS, "| `"+topic.Name+"` |") {
			t.Errorf("the core's topic table does not list %s", topic.Name)
		}
	}
	// And the other way: a row for a topic that does not exist is a command
	// that fails.
	for line := range strings.SplitSeq(skills.TUIOS[strings.Index(skills.TUIOS, "## Topics"):], "\n") {
		name, ok := strings.CutPrefix(line, "| `")
		if !ok {
			continue
		}
		name, _, _ = strings.Cut(name, "`")
		if _, err := skills.Lookup(name); err != nil {
			t.Errorf("the core's topic table lists %s: %v", name, err)
		}
	}
}

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
		if !strings.Contains(skillText(t, "errors"), want) {
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
