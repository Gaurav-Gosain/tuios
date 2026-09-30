package session

import "testing"

// The mark is the daemon's. A push that omits it keeps it, and a push that
// claims it for another pane does not get it.
func TestScratchMarkIsDaemonOwned(t *testing.T) {
	canonical := &SessionState{Windows: []WindowState{
		{ID: "scratch", Popup: true, IsFloating: true, Scratch: true},
		{ID: "picker", Popup: true, IsFloating: true},
		{ID: "pane"},
	}}
	incoming := &SessionState{Windows: []WindowState{
		{ID: "scratch"},
		{ID: "picker", Popup: true, Scratch: true},
		{ID: "pane", Scratch: true},
	}}
	retainDaemonExclusive(incoming, canonical)
	got := map[string]bool{}
	for _, w := range incoming.Windows {
		got[w.ID] = w.Scratch
	}
	if !got["scratch"] || got["picker"] || got["pane"] {
		t.Fatalf("scratch marks after the merge = %v, want only the daemon's scratch popup", got)
	}
}

// The popup verb opens the scratch terminal with no command, as a shell. A
// session has one: a second call, which is a double press that raced the
// first, is refused. list-windows marks it. A popup that is not the scratch
// terminal still needs its command.
//
// Negative control, confirmed red: drop the "already has a scratch terminal"
// loop in verbPopup and the second call opens a second pane.
func TestScratchPopupVerbOpensOneShell(t *testing.T) {
	d, sp := startTestDaemon(t)
	makeSessionWithWindow(t, d, "work")
	attachTUI(t, sp, "work")
	c := dialVerb(t, sp)

	res := result(t, c.call(t, `{"id":1,"verb":"popup","params":{"session":"work","name":"scratch","scratch":true}}`))
	if res["type"] != "popup_opened" {
		t.Fatalf("scratch popup = %v, want popup_opened", res)
	}
	id, _ := res["window_id"].(string)

	if code := errCode(t, c.call(t, `{"id":2,"verb":"popup","params":{"session":"work","scratch":true}}`)); code != ErrVerbInvalidParams {
		t.Fatalf("a second scratch terminal: code %q, want %q", code, ErrVerbInvalidParams)
	}
	if code := errCode(t, c.call(t, `{"id":3,"verb":"popup","params":{"session":"work"}}`)); code != ErrVerbInvalidParams {
		t.Fatalf("a plain popup with no command: code %q, want %q", code, ErrVerbInvalidParams)
	}

	list := result(t, c.call(t, `{"id":4,"verb":"list-windows","params":{"session":"work"}}`))
	rows, _ := list["windows"].([]any)
	scratch := 0
	for _, r := range rows {
		row, _ := r.(map[string]any)
		if row["scratch"] == true {
			scratch++
			if row["window_id"] != id {
				t.Errorf("list-windows marks %v, want %s", row["window_id"], id)
			}
		}
	}
	if len(rows) != 2 || scratch != 1 {
		t.Fatalf("list-windows = %d rows, %d marked scratch, want 2 and 1: %v", len(rows), scratch, rows)
	}
}

// The scratch terminal survives a daemon restart, hidden. It runs a shell, so
// the respawned shell is what it held. Any other popup is still dropped.
//
// Negative control, confirmed red: restore the old `if w.Popup { continue }`
// guard and the scratch terminal is gone after the restore.
func TestScratchTerminalSurvivesTheDaemonHidden(t *testing.T) {
	tmpDir := t.TempDir()
	defer useResurrectionDir(tmpDir)()

	cwd := t.TempDir()
	saved := &SessionState{
		Name:             "keeps-scratch",
		CurrentWorkspace: 1,
		Width:            120,
		Height:           40,
		Windows: []WindowState{
			{ID: "pane", Title: "shell", Width: 60, Height: 40, Workspace: 1, PTYID: "dead-1", Cwd: cwd},
			{ID: "scratch", Title: "scratch", Width: 80, Height: 30, Workspace: 2, PTYID: "dead-2", Cwd: cwd,
				Popup: true, IsFloating: true, Scratch: true, PopupWidth: "80%", PopupHeight: "80%"},
			{ID: "fzf", Title: "fzf", Width: 60, Height: 20, Workspace: 1, PTYID: "dead-3", Cwd: cwd,
				Popup: true, IsFloating: true},
		},
	}
	if err := SaveSessionForResurrection(saved); err != nil {
		t.Fatalf("failed to save state: %v", err)
	}

	d := NewDaemon(&DaemonConfig{})
	d.restoreAllSessions()
	defer d.manager.Shutdown()

	sess := d.manager.GetSession("keeps-scratch")
	if sess == nil {
		t.Fatal("session was not restored")
	}
	byID := map[string]WindowState{}
	for _, w := range sess.GetState().Windows {
		byID[w.ID] = w
	}
	if _, ok := byID["fzf"]; ok {
		t.Error("a plain popup came back from the dead")
	}
	s, ok := byID["scratch"]
	if !ok {
		t.Fatalf("the scratch terminal did not come back: %v", byID)
	}
	if !s.Scratch || !s.Popup || !s.Minimized || s.PTYID == "" || s.PTYID == "dead-2" {
		t.Fatalf("restored scratch = scratch %v popup %v minimized %v pty %q, want a hidden scratch popup with a new shell",
			s.Scratch, s.Popup, s.Minimized, s.PTYID)
	}
}
