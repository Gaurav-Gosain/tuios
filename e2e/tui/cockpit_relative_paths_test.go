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

	"github.com/Gaurav-Gosain/tuitest"
	"tuios.dev/shot"
)

// TestCockpitPathsAreRelativeToTheWorktree joins a transcript to a worktree
// agent whose edits name files by their absolute paths, the way Claude Code
// writes them, and reads its thread in the agents view. An edit in the
// worktree is named by its path in it ("cmd/main.go"), with no part of the
// worktree's own path. An edit outside it, Claude Code's plan file, stays in
// the thread with a ~ path but is not counted on the Changes tab, which says
// "Changes 1".
//
// Negative control: on main the edit reads "agent-paths/cmd/main.go", the
// last three parts of its absolute path, and the tab says "Changes 2".
func TestCockpitPathsAreRelativeToTheWorktree(t *testing.T) {
	base, repo := fanFixture(t)
	if out, err := tuiosCLI(t, base, "new", "home", "--detach"); err != nil {
		t.Fatalf("start the daemon: %v: %s", err, out)
	}
	if out, err := tuiosCLI(t, base, "fan", "1", "--agent", "claude", "--repo", repo, "--name", "agent/paths", "Add a json flag."); err != nil {
		t.Fatalf("fan: %v: %s", err, out)
	}
	rows := worktreeRows(t, base, "--group", "agent/paths")
	if len(rows) != 1 {
		t.Fatalf("worktree ls: %v", rows)
	}
	wt, _ := rows[0]["path"].(string)
	sess, _ := rows[0]["session"].(string)
	if wt == "" || sess == "" {
		t.Fatalf("worktree ls gave no path or session: %v", rows[0])
	}
	planFile := filepath.Join(xdgDir(base, "HOME"), ".claude", "plans", "json-flag.md")

	now := time.Now()
	at := func(ago time.Duration) string { return now.Add(-ago).UTC().Format("2006-01-02T15:04:05.000Z") }
	recs := []map[string]any{
		{"type": "user", "timestamp": at(3 * time.Minute), "message": map[string]any{"role": "user", "content": "Add a --json flag."}},
		{"type": "assistant", "timestamp": at(2*time.Minute + 50*time.Second), "message": map[string]any{"role": "assistant", "content": []any{
			map[string]any{"type": "tool_use", "id": "toolu_p", "name": "Write", "input": map[string]any{"file_path": planFile, "content": "# Plan\n1. Add the flag.\n"}},
		}}},
		{"type": "user", "timestamp": at(2*time.Minute + 49*time.Second), "message": map[string]any{"role": "user", "content": []any{
			map[string]any{"type": "tool_result", "tool_use_id": "toolu_p", "content": "File created."},
		}}},
		{"type": "assistant", "timestamp": at(2 * time.Minute), "message": map[string]any{"role": "assistant", "content": []any{
			map[string]any{"type": "tool_use", "id": "toolu_e", "name": "Edit", "input": map[string]any{"file_path": filepath.Join(wt, "cmd", "main.go"), "old_string": "func main() {}", "new_string": "func main() { flag.Parse() }"}},
		}}},
		{"type": "user", "timestamp": at(time.Minute + 59*time.Second), "message": map[string]any{"role": "user", "content": []any{
			map[string]any{"type": "tool_result", "tool_use_id": "toolu_e", "content": "The file has been updated."},
		}}},
		{"type": "assistant", "timestamp": at(time.Minute), "message": map[string]any{"role": "assistant", "content": []any{
			map[string]any{"type": "text", "text": "Added the flag."},
		}}},
	}
	var buf bytes.Buffer
	for i, r := range recs {
		r["uuid"] = fmt.Sprintf("u%d", i)
		r["isSidechain"] = false
		b, err := json.Marshal(r)
		if err != nil {
			t.Fatal(err)
		}
		buf.Write(b)
		buf.WriteByte('\n')
	}
	path := filepath.Join(atProjects(base), "work", "d1e2f3a4.jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	hook := exec.Command(tuiosBin, "agent-hook", "claude-code", "--session", sess, "--window", "0")
	hook.Dir = workDirIn(t, base)
	hook.Env = append(os.Environ(), "SHELL=/bin/sh")
	for _, key := range xdgKeys {
		hook.Env = append(hook.Env, key+"="+xdgDir(base, key))
	}
	hook.Stdin = bytes.NewReader(hookPayload(path, "d1e2f3a4"))
	if out, err := hook.CombinedOutput(); err != nil {
		t.Fatalf("agent-hook SessionStart: %v\n%s", err, out)
	}

	term := startIn(t, base, startOpts{args: []string{"attach", "home"}, cols: 140, rows: 44})
	if err := term.WaitFor(func(s tuitest.Screen) bool { return countWindows(s) >= 1 }, bootTimeout); err != nil {
		t.Fatalf("client never attached: %v\n%s", err, term.Snapshot())
	}
	windowManagementMode(t, term)
	sendKeys(t, term, tuitest.Ctrl('b'), "i")
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return screenHas(s, "Added the flag.", "Changes")
	}, 20*time.Second); err != nil {
		t.Fatalf("the agents view never read the thread: %v\n%s", err, term.Snapshot())
	}
	dir := artifactDir(t)
	// The Changes tab draws every edit of the last turn open.
	sendKeys(t, term, "]")
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return strings.Contains(s.Text(), "main.go") && strings.Contains(s.Text(), "open in review")
	}, uiTimeout); err != nil {
		t.Fatalf("the Changes tab never showed the edit: %v\n%s", err, term.Snapshot())
	}
	_ = term.WaitStable(uiTimeout)
	savePNGBelow(t, term.Screen(), "Thread    Changes", dir, "changes")
	saveFrame(t, term, "relative-paths-changes")
	text := term.Screen().Text()
	if !strings.Contains(text, "cmd/main.go") || strings.Contains(text, "agent-paths/cmd/main.go") || strings.Contains(text, wt+"/") {
		t.Errorf("ASSERTION: an edit in the worktree is not named by its path in it:\n%s", term.Snapshot())
	}
	if strings.Contains(text, "json-flag.md") || !strings.Contains(text, "Changes 1") {
		t.Errorf("ASSERTION: the plan file outside the worktree is on the Changes tab:\n%s", term.Snapshot())
	}

	// The thread keeps the plan file, named under ~.
	sendKeys(t, term, "[", "e")
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return screenHas(s, "cmd/main.go", "~/.claude/plans/json-flag.md")
	}, uiTimeout); err != nil {
		t.Fatalf("ASSERTION: the thread does not name the plan file under ~: %v\n%s", err, term.Snapshot())
	}
	savePNGBelow(t, term.Screen(), "Thread    Changes", dir, "thread")
	saveFrame(t, term, "relative-paths")
	alive(t, term, "after reading a thread with paths")
}

// savePNGBelow saves the screen from the row that holds mark down, less the
// dock, as a PNG: the header above it names the test's scratch folders.
func savePNGBelow(t *testing.T, s tuitest.Screen, mark, dir, name string) {
	t.Helper()
	g := screenGrid(s, shot.XTermPalette())
	top := 0
	for i, line := range strings.Split(s.Text(), "\n") {
		if strings.Contains(line, mark) {
			top = i
			break
		}
	}
	bottom := max(g.Rows-2, top+1)
	g.Cells, g.Rows = g.Cells[top:bottom], bottom-top
	saveGridPNG(t, g, dir, name)
}
