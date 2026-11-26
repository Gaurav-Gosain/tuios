package tuie2e

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/testutil"
	"github.com/Gaurav-Gosain/tuitest"
	"tuios.dev/shot"
)

// TestReviewAsksTheAgentsOwnEdits runs two turns of a Claude Code pane whose
// transcript the daemon reads, and opens the review:
//
//   - In turn 1 the agent edits main.go with its Edit tool, and the person
//     writes another line while the turn runs. The turn runs no tool that can
//     write, so the person's line says "you · during turn 1".
//   - Between the turns the person writes a line, which says "you · not in a
//     turn".
//   - In turn 2 the agent runs a shell command, and a line no edit of its
//     shows up. It may be the command's, so the hunk says "turn 2 ?", or "you
//     ?" when the command started before the turn start's snapshot ended, and
//     the status line says why when the cursor is on it.
//
// How it could pass wrongly, written down first:
//   - Every line of turn 1 could say "you" because the transcript is not
//     read at all. The agent's own line must say "turn 1".
//   - The "?" could be on every hunk of turn 2. The line the agent's Edit
//     made in turn 2 must say "turn 2" with no "?".
//   - Each turn's Edit follows its start at once, so the turn start's
//     snapshot may already hold the edit. The edit must still say its turn,
//     and not "you".
func TestReviewAsksTheAgentsOwnEdits(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)
	repo := testutil.GitRepo(t)
	file := filepath.Join(repo, "main.go")
	lines := map[int]string{}
	write := func() {
		t.Helper()
		var b strings.Builder
		for i := 1; i <= 80; i++ {
			if s, ok := lines[i]; ok {
				b.WriteString(s)
			} else {
				fmt.Fprintf(&b, "line %d", i)
			}
			b.WriteByte('\n')
		}
		if err := os.WriteFile(file, []byte(b.String()), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write()
	testutil.Git(t, repo, "add", "main.go")
	testutil.Git(t, repo, "commit", "-q", "-m", "main")
	if out, err := tuiosCLIIn(t, base, repo, "new", "agent", "--detach"); err != nil {
		t.Fatalf("new session in the repository: %v: %s", err, out)
	}
	cli := func(args ...string) {
		t.Helper()
		if out, err := tuiosCLI(t, base, args...); err != nil {
			t.Fatalf("%v: %v\n%s", args, err, out)
		}
	}

	// The transcript: Claude Code's format, joined to the pane by its
	// SessionStart hook.
	tpath := filepath.Join(atProjects(base), "work", "e7f8a9b0.jsonl")
	if err := os.MkdirAll(filepath.Dir(tpath), 0o700); err != nil {
		t.Fatal(err)
	}
	var recs bytes.Buffer
	n := 0
	record := func(r map[string]any) {
		t.Helper()
		r["uuid"] = fmt.Sprintf("u%d", n)
		r["isSidechain"] = false
		r["timestamp"] = time.Now().UTC().Format("2006-01-02T15:04:05.000Z")
		n++
		b, err := json.Marshal(r)
		if err != nil {
			t.Fatal(err)
		}
		recs.Write(b)
		recs.WriteByte('\n')
		if err := os.WriteFile(tpath, recs.Bytes(), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	tool := func(name string, input map[string]any) {
		t.Helper()
		id := fmt.Sprintf("toolu_%d", n)
		record(map[string]any{"type": "assistant", "message": map[string]any{"role": "assistant", "content": []any{
			map[string]any{"type": "tool_use", "id": id, "name": name, "input": input},
		}}})
		record(map[string]any{"type": "user", "message": map[string]any{"role": "user", "content": []any{
			map[string]any{"type": "tool_result", "tool_use_id": id, "content": "ok"},
		}}})
	}
	record(map[string]any{"type": "user", "message": map[string]any{"role": "user", "content": "Do the thing."}})
	hook := exec.Command(tuiosBin, "agent-hook", "claude-code", "--session", "agent", "--window", "0")
	hook.Dir = workDirIn(t, base)
	hook.Env = append(os.Environ(), "SHELL=/bin/sh")
	for _, key := range xdgKeys {
		hook.Env = append(hook.Env, key+"="+xdgDir(base, key))
	}
	hook.Stdin = bytes.NewReader(hookPayload(tpath, "e7f8a9b0"))
	if out, err := hook.CombinedOutput(); err != nil {
		t.Fatalf("agent-hook SessionStart: %v\n%s", err, out)
	}

	daemonLog := filepath.Join(xdgDir(base, "XDG_STATE_HOME"), "tuios", "daemon.log")
	started := 0
	// begin starts a turn. The agent's edit follows at once, as a fast one
	// does, so the turn start's snapshot may already hold it.
	begin := func() {
		t.Helper()
		cli("set-agent-state", "working", "-s", "agent", "--harness", "claude-code")
		started++
	}
	// snapped waits for the turn start's snapshot, before a line that must
	// come after it.
	snapped := func() {
		t.Helper()
		waitUntil(t, fmt.Sprintf("turn start %d is looked at", started), uiTimeout, func() (bool, string) {
			b, _ := os.ReadFile(daemonLog)
			return strings.Count(string(b), "Turn start of window") >= started, string(b)
		})
	}
	end := func(turn int) {
		t.Helper()
		cli("set-agent-state", "done", "-s", "agent", "--harness", "claude-code", "-m", fmt.Sprintf("turn %d", turn))
		waitUntil(t, fmt.Sprintf("checkpoint %d is taken", turn), uiTimeout, func() (bool, string) {
			out, _ := tuiosCLI(t, base, "call", "list-checkpoints", `{"session":"agent"}`)
			var got struct {
				Checkpoints []struct{} `json:"checkpoints"`
			}
			_ = json.Unmarshal([]byte(out), &got)
			return len(got.Checkpoints) >= turn, out
		})
	}

	// Turn 1: the agent's Edit, and the person's line, with no command.
	begin()
	lines[10] = "line 10 BY-AGENT-EDIT"
	write()
	tool("Edit", map[string]any{"file_path": file, "old_string": "line 10", "new_string": "line 10 BY-AGENT-EDIT"})
	snapped()
	lines[30] = "line 30 BY-PERSON-IN-TURN"
	write()
	end(1)

	// Between the turns: the person's line.
	lines[40] = "line 40 BETWEEN-TURNS"
	write()

	// Turn 2: an Edit, and a command that leaves a line no edit shows.
	begin()
	lines[50] = "line 50 TURN2-EDIT"
	write()
	tool("Edit", map[string]any{"file_path": file, "old_string": "line 50", "new_string": "line 50 TURN2-EDIT"})
	tool("Bash", map[string]any{"command": "make generate"})
	lines[70] = "line 70 BY-A-COMMAND"
	write()
	end(2)

	term := attachIn(t, base, "agent", startOpts{cols: 150, rows: 60})
	sendKeys(t, term, tuitest.Ctrl('b'), "v")
	waitScreen(t, term, "the review never opened", "Review", "BY-AGENT-EDIT", "BY-A-COMMAND")
	chipOf := func(s tuitest.Screen, hunkLine string) string {
		row, _, ok := textAt(s, hunkLine, 0)
		for y := row - 1; ok && y >= 0 && y > row-6; y-- {
			if strings.Contains(s.Line(y), "@@") {
				return s.Line(y)
			}
		}
		return ""
	}
	// The command of turn 2 and its line race the turn start's snapshot,
	// as a hook's command does. Before the snapshot ends, the command's line
	// and the person's line between the turns read "you ?". After it, the
	// command's line reads "turn 2 ?" and the person's "you · not in a turn".
	unsure := func(ch string) bool { return strings.Contains(ch, "turn 2 ?") || strings.Contains(ch, "you ?") }
	for _, c := range []struct {
		line, want string
		ok         func(chip string) bool
	}{
		{"BY-AGENT-EDIT", "turn 1, no ?", func(ch string) bool { return strings.Contains(ch, "turn 1") && !strings.Contains(ch, "?") }},
		{"BY-PERSON-IN-TURN", "you · during turn 1", func(ch string) bool { return strings.Contains(ch, "you · during turn 1") }},
		{"BETWEEN-TURNS", "you · not in a turn, or you ?", func(ch string) bool {
			return strings.Contains(ch, "you · not in a turn") || strings.Contains(ch, "you ?")
		}},
		{"TURN2-EDIT", "turn 2, no ?", func(ch string) bool { return strings.Contains(ch, "turn 2") && !strings.Contains(ch, "?") }},
		{"BY-A-COMMAND", "turn 2 ? or you ?", unsure},
	} {
		if err := term.WaitFor(func(s tuitest.Screen) bool { return c.ok(chipOf(s, c.line)) }, uiTimeout); err != nil {
			t.Errorf("ASSERTION: the hunk of %s does not say %s: %v\n%s", c.line, c.want, err, term.Snapshot())
		}
	}
	art := artifactDir(t)
	saveArtifact(t, term, art, "1-unsure-chip")
	savePNG(t, term.Screen(), shot.XTermPalette(), art, "1-unsure-chip")

	// The cursor on the unsure hunk: the status line says why.
	row, col := findText(t, term, "BY-A-COMMAND")
	leftClick(t, term, col, row)
	waitScreen(t, term, "the status line never explained the ?", "Not in the agent edits. The agent may have run a tool on it, or you changed it.")
	saveArtifact(t, term, art, "2-unsure-hint")
	savePNG(t, term.Screen(), shot.XTermPalette(), art, "2-unsure-hint")
}
