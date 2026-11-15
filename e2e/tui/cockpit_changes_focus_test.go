package tuie2e

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// TestCockpitChangesTabGetsTheFocus reads the Changes tab of an agent at
// rest that edited four files, at 120x40. The tab is for the diff: the Done
// card folds to its title line, the closed reply to one line, and the diff
// opens at the first file. Back on the Thread tab, the reply is whole again.
//
// Negative control: on main the card keeps its recap and the reply its
// three rows, the diff opens at the end of the last file, and the first
// file's header is off screen.
func TestCockpitChangesTabGetsTheFocus(t *testing.T) {
	base, repo := fanFixture(t)
	if out, err := tuiosCLI(t, base, "new", "home", "--detach"); err != nil {
		t.Fatalf("start the daemon: %v: %s", err, out)
	}
	if out, err := tuiosCLI(t, base, "fan", "1", "--agent", "claude", "--repo", repo, "--name", "agent/diff", "Add a json flag."); err != nil {
		t.Fatalf("fan: %v: %s", err, out)
	}
	rows := worktreeRows(t, base, "--group", "agent/diff")
	if len(rows) != 1 {
		t.Fatalf("worktree ls: %v", rows)
	}
	wt, _ := rows[0]["path"].(string)
	sess, _ := rows[0]["session"].(string)
	if wt == "" || sess == "" {
		t.Fatalf("worktree ls gave no path or session: %v", rows[0])
	}

	now := time.Now()
	at := func(ago time.Duration) string { return now.Add(-ago).UTC().Format("2006-01-02T15:04:05.000Z") }
	recs := []map[string]any{
		{"type": "user", "timestamp": at(5 * time.Minute), "message": map[string]any{"role": "user", "content": "Add a --json flag."}},
	}
	files := []string{"alpha", "bravo", "charlie", "delta"}
	for i, name := range files {
		var added []string
		for n := range 8 {
			added = append(added, fmt.Sprintf("\t%sLine%d := %d", name, n, n))
		}
		id := fmt.Sprintf("toolu_%d", i)
		ago := time.Duration(4*60-i*20) * time.Second
		recs = append(recs,
			map[string]any{"type": "assistant", "timestamp": at(ago), "message": map[string]any{"role": "assistant", "content": []any{
				map[string]any{"type": "tool_use", "id": id, "name": "Edit", "input": map[string]any{
					"file_path":  filepath.Join(wt, "cmd", name+".go"),
					"old_string": "func " + name + "() {\n}",
					"new_string": "func " + name + "() {\n" + strings.Join(added, "\n") + "\n}",
				}},
			}}},
			map[string]any{"type": "user", "timestamp": at(ago - time.Second), "message": map[string]any{"role": "user", "content": []any{
				map[string]any{"type": "tool_result", "tool_use_id": id, "content": "The file has been updated."},
			}}},
		)
	}
	recs = append(recs, map[string]any{"type": "assistant", "timestamp": at(time.Minute), "message": map[string]any{"role": "assistant", "content": []any{
		map[string]any{"type": "text", "text": "Added the flag in four files."},
	}}})
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
	path := filepath.Join(atProjects(base), "work", "c4a5b6d7.jsonl")
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
	hook.Stdin = bytes.NewReader(hookPayload(path, "c4a5b6d7"))
	if out, err := hook.CombinedOutput(); err != nil {
		t.Fatalf("agent-hook SessionStart: %v\n%s", err, out)
	}
	// The fake agent answers the fan's prompt with a turn of its own, a
	// moment after it starts. A new turn closes the Done item set below, as
	// it must, so that turn ends first.
	waitUntil(t, "the fake agent's own turn ends", 20*time.Second, func() (bool, string) {
		pane, _ := tuiosCLI(t, base, "capture-pane", "-s", sess)
		wins, _ := tuiosCLI(t, base, "list-windows", "-s", sess)
		return strings.Contains(pane, "GOT: ") && !strings.Contains(wins, "working"), pane + "\n" + wins
	})
	for _, args := range [][]string{
		{"set-agent-state", "working", "-s", sess, "-w", "0", "--harness", "claude-code"},
		{"set-agent-state", "done", "-s", sess, "-w", "0", "--harness", "claude-code", "-m", "Flag added in four files"},
	} {
		if out, err := tuiosCLI(t, base, args...); err != nil {
			t.Fatalf("%v: %v\n%s", args, err, out)
		}
	}

	term := startIn(t, base, startOpts{args: []string{"attach", "home"}, cols: 120, rows: 40})
	if err := term.WaitFor(func(s tuitest.Screen) bool { return countWindows(s) >= 1 }, bootTimeout); err != nil {
		t.Fatalf("client never attached: %v\n%s", err, term.Snapshot())
	}
	windowManagementMode(t, term)
	sendKeys(t, term, tuitest.Ctrl('b'), "i")
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return screenHas(s, "Added the flag in four files.", "Changes", "Write a reply.")
	}, 20*time.Second); err != nil {
		t.Fatalf("the agents view never read the thread: %v\n%s", err, term.Snapshot())
	}
	_ = term.WaitStable(uiTimeout)
	saveFrame(t, term, "changes-thread-before")
	// The recap counts the fake's own turn too, so the count is not fixed.
	recap := regexp.MustCompile(`\b\d+ turns?\.`)
	cardWords := recap.MatchString(term.Screen().Text())

	sendKeys(t, term, "]")
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return strings.Contains(s.Text(), "open in review") && !strings.Contains(s.Text(), "Added the flag in four files.")
	}, uiTimeout); err != nil {
		t.Fatalf("the Changes tab never showed the edits: %v\n%s", err, term.Snapshot())
	}
	_ = term.WaitStable(uiTimeout)
	saveFrame(t, term, "changes-tab")
	savePNGBelow(t, term.Screen(), "Thread    Changes", artifactDir(t), "changes-tab")
	text := term.Screen().Text()
	if !strings.Contains(text, "cmd/alpha.go") {
		t.Errorf("ASSERTION: the Changes tab does not open at the first file:\n%s", term.Snapshot())
	}
	if strings.Contains(text, "Write a reply.") || !strings.Contains(text, "Reply to") {
		t.Errorf("ASSERTION: the closed reply is not one line on the Changes tab:\n%s", term.Snapshot())
	}
	if !cardWords || recap.MatchString(text) || !strings.Contains(text, "Done") {
		t.Errorf("ASSERTION: the Done card keeps its recap on the Changes tab:\n%s", term.Snapshot())
	}
	// A diff row starts with its line number. On main the diff gets 10.
	diffRows := 0
	numbered := regexp.MustCompile(`\s\d+ [ +-] `)
	for _, line := range strings.Split(text, "\n") {
		if numbered.MatchString(line) {
			diffRows++
		}
	}
	if diffRows < 16 {
		t.Errorf("ASSERTION: the diff gets %d rows of 40, want 16 or more:\n%s", diffRows, term.Snapshot())
	}

	// Back on the Thread tab, the reply is whole.
	sendKeys(t, term, "[")
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return screenHas(s, "Added the flag in four files.", "Write a reply.")
	}, uiTimeout); err != nil {
		t.Fatalf("ASSERTION: the Thread tab did not get the reply back: %v\n%s", err, term.Snapshot())
	}
	alive(t, term, "after the Changes tab")
}
