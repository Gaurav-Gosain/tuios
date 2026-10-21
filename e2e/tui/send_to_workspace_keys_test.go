package tuie2e

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// send_to_workspace_N sends the focused pane to workspace N and leaves the
// client where it is, the way move_and_follow_N does without the follow.
//
// How this could pass wrongly, written down first:
//   - The pane could move and the client follow it, which is
//     move_and_follow. The test reads the workspace the daemon says the
//     session shows and the one the client says it shows, and both must stay 1.
//   - The client could stay and the pane not move. The test reads the moved
//     pane's workspace from list-windows by its id, and counts both workspaces.
//   - Focus could stay on the pane that left, with nothing on screen to type
//     into. The test reads which pane is focused and wants one still on
//     workspace 1.
//   - The key could be a test-only binding. The test writes no keybindings
//     and presses the shipped default, ctrl+alt+N (ctrl+opt+N on macOS, which
//     the normalizer reads as ctrl+alt+N), sent as the CSI u sequence a
//     terminal with the kitty keyboard protocol sends for it.
//   - The key could work in one mode only. Terminal mode is where the swallow
//     predicates decide, so the first press is made there, and the second in
//     window mode.
//
// The screen and list-windows --json after each press are saved under
// artifactDir.

// ctrlAltDigit is the CSI u encoding of ctrl+alt and a digit key: the
// digit's code point, then modifiers 1 + alt (2) + ctrl (4).
func ctrlAltDigit(d rune) tuitest.Key {
	return tuitest.Key("\x1b[" + strconv.Itoa(int(d)) + ";7u")
}

// focusedPaneIn is the id of the pane list-windows marks focused in sess, or
// "" when none is.
func focusedPaneIn(t *testing.T, base, sess string) string {
	t.Helper()
	_, focused := focusedLayout(t, base, sess)
	return focused
}

// workspaceOfPane is the workspace list-windows puts pane id on, or 0.
func workspaceOfPane(t *testing.T, base, sess, id string) int {
	t.Helper()
	for _, r := range xpanesRowsIn(t, base, sess) {
		if r.ID == id {
			return r.Workspace
		}
	}
	return 0
}

// waitPaneWorkspace waits for pane id to be on workspace ws.
func waitPaneWorkspace(t *testing.T, term *tuitest.Terminal, base, sess, id string, ws int, what string) {
	t.Helper()
	deadline := time.Now().Add(uiTimeout)
	for workspaceOfPane(t, base, sess, id) != ws {
		if time.Now().After(deadline) {
			t.Fatalf("ASSERTION: %s: pane %s is on workspace %d, want %d\n%s",
				what, id, workspaceOfPane(t, base, sess, id), ws, term.Snapshot())
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// stayedOn checks the session and the client both still show ws, and the
// focused pane is one on ws other than the one that left.
func stayedOn(t *testing.T, term *tuitest.Terminal, base, sess string, ws int, left, what string) {
	t.Helper()
	// A follow would land a moment after the move, so give it the time to.
	time.Sleep(500 * time.Millisecond)
	if got := currentWorkspace(t, base, sess); got != ws {
		t.Errorf("ASSERTION: %s: the session shows workspace %d, want it to stay on %d\n%s", what, got, ws, term.Snapshot())
	}
	if got := clientWorkspace(t, base); got != ws {
		t.Errorf("ASSERTION: %s: the client shows workspace %d, want it to stay on %d", what, got, ws)
	}
	focused := focusedPaneIn(t, base, sess)
	if focused == left {
		t.Errorf("ASSERTION: %s: focus stayed on %s, the pane that left", what, left)
	}
	if got := workspaceOfPane(t, base, sess, focused); got != ws {
		t.Errorf("ASSERTION: %s: the focused pane %q is on workspace %d, want one on %d", what, focused, got, ws)
	}
}

func TestSendToWorkspaceKeyStaysHere(t *testing.T) {
	const sess = "mv"
	base := t.TempDir()
	dir := artifactDir(t)
	term := startIn(t, base, startOpts{cols: 120, rows: 40, args: []string{"new", sess}})
	waitBoot(t, term)
	for i := 1; i <= 3; i++ {
		newWindow(t, term)
	}
	waitWindowCount(t, term, 3, "setup")
	waitPanesOn(t, term, base, sess, 1, 3, "setup")

	// 1. In terminal mode, ctrl+alt+2 sends the focused pane to workspace 2.
	first := focusedPaneIn(t, base, sess)
	if first == "" {
		t.Fatalf("no pane is focused after setup\n%s", term.Snapshot())
	}
	enterTerminalMode(t, term)
	sendKeys(t, term, ctrlAltDigit('2'))
	waitPaneWorkspace(t, term, base, sess, first, 2, "ctrl+alt+2 in terminal mode")
	exactlyPanesOn(t, term, base, sess, 1, 2, "ctrl+alt+2 in terminal mode")
	exactlyPanesOn(t, term, base, sess, 2, 1, "ctrl+alt+2 in terminal mode")
	stayedOn(t, term, base, sess, 1, first, "ctrl+alt+2 in terminal mode")
	saveArtifact(t, term, dir, "1-terminal-mode")
	saveWindowList(t, base, sess, filepath.Join(dir, "1-terminal-mode.json"))

	// 2. In window mode, ctrl+alt+3 sends the pane focus went to on to
	// workspace 3.
	toWindowMode(t, term)
	second := focusedPaneIn(t, base, sess)
	sendKeys(t, term, ctrlAltDigit('3'))
	waitPaneWorkspace(t, term, base, sess, second, 3, "ctrl+alt+3 in window mode")
	exactlyPanesOn(t, term, base, sess, 1, 1, "ctrl+alt+3 in window mode")
	exactlyPanesOn(t, term, base, sess, 3, 1, "ctrl+alt+3 in window mode")
	stayedOn(t, term, base, sess, 1, second, "ctrl+alt+3 in window mode")
	if got := workspaceOfPane(t, base, sess, first); got != 2 {
		t.Errorf("ASSERTION: the first pane moved again, to workspace %d", got)
	}
	saveArtifact(t, term, dir, "2-window-mode")
	saveWindowList(t, base, sess, filepath.Join(dir, "2-window-mode.json"))
}

// A config written before send_to_workspace_N had a default can already hold
// ctrl+alt+N for another action. That binding keeps the key,
// send_to_workspace_N goes without one, and keybinds doctor says so.
//
// How this could pass wrongly, written down first:
//   - The new default could take the key and the doctor still list the yield.
//     The test presses the key and wants the user's action: the session
//     shows workspace 2 and no pane moves.
//   - The yield could drop every new default, not the one whose key is taken.
//     ctrl+alt+3 is free in this config, and it must still move a pane.
//   - The doctor could name the wrong action or holder. The test reads its
//     JSON and wants send_to_workspace_2 taken by switch_workspace_2, and no
//     other send_to_workspace row.
func TestSendToWorkspaceKeyYieldsATakenKey(t *testing.T) {
	const sess = "mvy"
	base := t.TempDir()
	dir := artifactDir(t)
	writeConfig(t, base, "[keybindings.workspaces]\nswitch_workspace_2 = [\"ctrl+alt+2\"]\n")

	out, err := tuiosCLI(t, base, "keybinds", "doctor", "--json")
	if err != nil {
		t.Fatalf("keybinds doctor: %v\n%s", err, out)
	}
	if err := os.WriteFile(filepath.Join(dir, "doctor.json"), []byte(out), 0o644); err != nil {
		t.Errorf("save doctor.json: %v", err)
	}
	var rep struct {
		Yielded []struct {
			Action  string `json:"action"`
			TakenBy string `json:"taken_by"`
		} `json:"yielded_defaults"`
	}
	if err := json.Unmarshal([]byte(out), &rep); err != nil {
		t.Fatalf("keybinds doctor json: %v\n%s", err, out)
	}
	var moves []string
	for _, y := range rep.Yielded {
		if strings.HasPrefix(y.Action, "send_to_workspace_") {
			moves = append(moves, y.Action+" taken by "+y.TakenBy)
		}
	}
	if len(moves) != 1 || moves[0] != "send_to_workspace_2 taken by switch_workspace_2" {
		t.Errorf("ASSERTION: keybinds doctor lists %q, want only send_to_workspace_2 taken by switch_workspace_2", moves)
	}

	term := startIn(t, base, startOpts{cols: 120, rows: 40, args: []string{"new", sess}})
	waitBoot(t, term)
	newWindow(t, term)
	newWindow(t, term)
	waitWindowCount(t, term, 2, "setup")
	waitPanesOn(t, term, base, sess, 1, 2, "setup")

	// 1. ctrl+alt+3 is free, so it still moves the focused pane.
	moved := focusedPaneIn(t, base, sess)
	sendKeys(t, term, ctrlAltDigit('3'))
	waitPaneWorkspace(t, term, base, sess, moved, 3, "ctrl+alt+3, a free key")
	stayedOn(t, term, base, sess, 1, moved, "ctrl+alt+3, a free key")

	// 2. ctrl+alt+2 is the user's switch, so it shows workspace 2 and moves
	// nothing.
	kept := focusedPaneIn(t, base, sess)
	sendKeys(t, term, ctrlAltDigit('2'))
	waitShowing(t, base, sess, 2, "", term)
	if got := workspaceOfPane(t, base, sess, kept); got != 1 {
		t.Errorf("ASSERTION: ctrl+alt+2 moved pane %s to workspace %d; the user's switch_workspace_2 holds that key", kept, got)
	}
	saveArtifact(t, term, dir, "after-ctrl-alt-2")
	saveWindowList(t, base, sess, filepath.Join(dir, "after-ctrl-alt-2.json"))
}
