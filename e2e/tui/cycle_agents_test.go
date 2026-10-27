package tuie2e

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// TestCycleAgentsWalksTheRailOrder drives next_agent and prev_agent from
// terminal mode over three agent panes in two sessions. Two work in alpha and
// one in bravo waits on an approval, so the rail's needs-you order (bravo's
// first, then alpha's two) differs from the order the panes were opened in,
// and a walk in tree order would fail the first step.
//
// From alpha's plain shell, next goes to the first row and crosses to bravo;
// next twice more comes back to alpha's two; next past the last wraps to the
// first and previous before the first wraps to the last. The leader's K walks
// back the same way. A line typed afterwards reaches the pane, so the keys
// never left terminal mode. The walk is written to cycle-agents.txt in the
// artifact directory, with a frame of the rail at each step.
//
// Negative controls are in NEGATIVE_CONTROLS.md: the sort cut from the order,
// the wrap clamped, and the actions dropped from the terminal-safe list each
// fail it.
func TestCycleAgentsWalksTheRailOrder(t *testing.T) {
	base := t.TempDir()
	writeConfig(t, base, "[appearance.sidebar]\nenabled = true\n")
	killDaemon(t, base)
	for _, name := range []string{"alpha", "bravo"} {
		if out, err := tuiosCLI(t, base, "new", name, "--detach"); err != nil {
			t.Fatalf("create %s: %v: %s", name, err, out)
		}
	}
	plain := focusedIn(t, base, "alpha")
	type pane struct{ sess, name, id string }
	open := func(sess, name string, state ...string) pane {
		out, err := tuiosCLI(t, base, "new-window", "-s", sess, "--no-focus", "--print-id", name)
		if err != nil {
			t.Fatalf("new-window %s: %v\n%s", name, err, out)
		}
		id := strings.TrimSpace(out)
		if i := strings.LastIndexByte(id, '\n'); i >= 0 {
			id = strings.TrimSpace(id[i+1:])
		}
		args := append([]string{"set-agent-state", "-s", sess, "-w", name}, state...)
		if out, err := tuiosCLI(t, base, args...); err != nil {
			t.Fatalf("set-agent-state %s: %v\n%s", name, err, out)
		}
		return pane{sess, name, id}
	}
	a1 := open("alpha", "ag-a1", "working", "--harness", "claude-code")
	a2 := open("alpha", "ag-a2", "working", "--harness", "claude-code")
	b1 := open("bravo", "ag-b1", "needs_input", "--kind", "approval", "--harness", "claude-code", "-m", "approve Bash: ls")
	rail := []pane{b1, a1, a2}

	term := startIn(t, base, startOpts{cols: 160, rows: 45, args: []string{"attach", "alpha"}})
	waitWindowCount(t, term, 3, "alpha's three panes")
	// The rail lists all three, in the order the walk has to follow.
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return railOrderIs(s, b1.name, a1.name, a2.name)
	}, uiTimeout); err != nil {
		t.Fatalf("the rail never listed %s, %s, %s in that order: %v\n%s", b1.name, a1.name, a2.name, err, term.Snapshot())
	}
	if got := focusedIn(t, base, "alpha"); got != plain {
		t.Fatalf("alpha's focus is %s, want its plain shell %s", got, plain)
	}
	windowManagementMode(t, term)
	enterTerminalMode(t, term)

	dir := artifactDir(t)
	var log strings.Builder
	fmt.Fprintf(&log, "rail order: %s/%s, %s/%s, %s/%s\n", b1.sess, b1.name, a1.sess, a1.name, a2.sess, a2.name)
	step := 0
	press := func(what string, keys []any, want pane) {
		t.Helper()
		step++
		if err := term.SendKeys(keys...); err != nil {
			t.Fatalf("step %d, %s: send: %v", step, what, err)
		}
		deadline := time.Now().Add(uiTimeout)
		for {
			attached := attachedSessions(base)
			if slices.Equal(attached, []string{want.sess}) && focusedIn(t, base, want.sess) == want.id {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("step %d, %s: want %s/%s focused, have attached %v with %s focused there\n%s",
					step, what, want.sess, want.name, attached, focusedIn(t, base, want.sess), term.Snapshot())
			}
			time.Sleep(100 * time.Millisecond)
		}
		fmt.Fprintf(&log, "%d. %s -> %s/%s\n", step, what, want.sess, want.name)
		saveArtifact(t, term, dir, fmt.Sprintf("step-%02d", step))
		// The next chord must not land inside the single-key guard.
		time.Sleep(insertGuard + 150*time.Millisecond)
	}
	next := []any{tuitest.Alt('J')}
	prev := []any{tuitest.Alt('K')}

	press("next from a plain pane goes to the first", next, rail[0])
	press("next crosses back to alpha", next, rail[1])
	press("next", next, rail[2])
	press("next past the last wraps to the first", next, rail[0])
	press("prev before the first wraps to the last", prev, rail[2])
	press("prev", prev, rail[1])
	press("leader K steps back across sessions", []any{tuitest.Ctrl('b'), "K"}, rail[0])
	press("leader J", []any{tuitest.Ctrl('b'), "J"}, rail[1])

	// Still in terminal mode: a typed line runs in the focused pane.
	if err := term.SendKeys("echo cycle-$((40+2))\r"); err != nil {
		t.Fatalf("type into the pane: %v", err)
	}
	deadline := time.Now().Add(shellTimeout)
	for {
		out, _ := tuiosOut(base, "capture-pane", "-s", a1.sess, "-w", a1.id)
		if strings.Contains(out, "cycle-42") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the typed line never ran in %s, so the keys left terminal mode:\n%s\n%s", a1.name, out, term.Snapshot())
		}
		time.Sleep(100 * time.Millisecond)
	}
	fmt.Fprintf(&log, "typed line ran in %s/%s: terminal mode held\n", a1.sess, a1.name)
	if err := os.WriteFile(filepath.Join(dir, "cycle-agents.txt"), []byte(log.String()), 0o644); err != nil {
		t.Errorf("save the walk: %v", err)
	}
}

// railOrderIs reports whether the rail's agents section lists each name, each
// on a row below the one before. Only the rail's column is read, left of its
// edge rule, and only below the section's header, so a pane's title cannot
// stand in for a row.
func railOrderIs(s tuitest.Screen, names ...string) bool {
	_, rows := s.Size()
	railText := func(y int) string {
		rail, _, _ := strings.Cut(s.Line(y), "│")
		return rail
	}
	line := -1
	for y := range rows {
		if strings.HasPrefix(strings.TrimSpace(railText(y)), "agents") {
			line = y
			break
		}
	}
	if line < 0 {
		return false
	}
	for _, name := range names {
		found := -1
		for y := line + 1; y < rows; y++ {
			if strings.Contains(railText(y), name) {
				found = y
				break
			}
		}
		if found < 0 {
			return false
		}
		line = found
	}
	return true
}
