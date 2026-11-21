package tuie2e

import (
	"testing"

	"github.com/Gaurav-Gosain/tuitest"
)

// TestMoveKeepsItsWorkspaceWhileThePaneAnnouncesFolders: a pane that moves to
// another workspace stays there while its shell reports new folders. Each
// report makes the daemon publish its state with a new version and no change
// (PublishLiveFacts). A push built before that version was taken as stale,
// and the stale merge put each window back on the workspace the daemon had:
// the move was undone, and the client went back with it. It was the step 3
// flake of TestEmptyWorkspaceOpensAPane, where a new shell's first folder
// report met the move key.
//
// The shell here reports a new folder every 20 ms for about 3 s, and the move
// key is pressed while it does.
//
// Negative control: in PublishLiveFacts (internal/session/remote_pane.go),
// cut the noteTreeOpLocked call. The session shows workspace 1 again.
func TestMoveKeepsItsWorkspaceWhileThePaneAnnouncesFolders(t *testing.T) {
	const sess = "mf"
	base := t.TempDir()
	writeConfig(t, base, "[keybindings.workspaces]\nmove_and_follow_3 = [\"alt+m\"]\n")
	term := startHome(t, base, sess)
	loop := `for i in $(seq 150); do printf '\033]7;file://%s/tmp/mf%d\033\\' "$(hostname)" "$i"; sleep 0.02; done; echo LOOP-$((5*9))`
	if err := term.SendKeys(loop, tuitest.Enter); err != nil {
		t.Fatalf("type the folder loop: %v", err)
	}
	leaveTerminalMode(t, term)
	if err := term.SendKeys(tuitest.Alt('m')); err != nil {
		t.Fatalf("press alt+m: %v", err)
	}
	waitShowing(t, base, sess, 2, "", term)
	if err := term.WaitForText("LOOP-45", shellTimeout); err != nil {
		t.Fatalf("the folder loop never ended: %v\n%s", err, term.Snapshot())
	}
	if ws := currentWorkspace(t, base, sess); ws != 2 {
		t.Fatalf("ASSERTION: the session shows workspace %d after the move, want 2\n%s", ws, term.Snapshot())
	}
	rows := xpanesRowsIn(t, base, sess)
	if len(rows) != 1 || rows[0].Workspace != 2 {
		t.Fatalf("ASSERTION: the pane is not on workspace 2 after the move: %+v\n%s", rows, term.Snapshot())
	}
	saveFrame(t, term, "move-keeps-workspace")
	alive(t, term, "after the move while the pane announces folders")
}
