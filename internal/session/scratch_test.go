package session

import (
	"errors"
	"sync"
	"testing"
)

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

// Two creates that race cannot both add a scratch terminal: the check is made
// under the state lock, and the loser's shell is closed.
//
// Negative control, confirmed red: drop the check in AddDaemonWindowWith and
// both goroutines add one.
func TestScratchCreateIsAtomic(t *testing.T) {
	sess := newTestSession(t)
	var wg sync.WaitGroup
	errs := make([]error, 8)
	for i := range errs {
		wg.Go(func() {
			_, errs[i] = sess.AddDaemonWindowWith(NewWindowOptions{
				Title: "scratch", Popup: true, Scratch: true, Command: []string{"sleep", "30"},
			}, nil)
		})
	}
	wg.Wait()
	made := 0
	for _, w := range sess.GetState().Windows {
		if w.Scratch {
			made++
		}
	}
	refused := 0
	for _, err := range errs {
		if errors.Is(err, ErrScratchExists) {
			refused++
		}
	}
	if made != 1 || refused != len(errs)-1 {
		t.Fatalf("made %d scratch terminals, refused %d, want 1 and %d", made, refused, len(errs)-1)
	}
}

// The daemon's focus cycle never lands on a hidden scratch terminal, and a
// focus by id shows it on the current workspace first.
func TestDaemonFocusAndHiddenScratch(t *testing.T) {
	sess := newTestSession(t)
	state := &SessionState{CurrentWorkspace: 2, Windows: []WindowState{
		{ID: "a", Workspace: 2},
		{ID: "hidden", Workspace: 2, Popup: true, Scratch: true, Minimized: true},
		{ID: "b", Workspace: 2},
	}, FocusedWindowID: "a"}
	_ = sess.mutateState(func(s *SessionState) error { *s = *state; return nil })

	for range 4 {
		if err := sess.CycleDaemonFocus(1); err != nil {
			t.Fatal(err)
		}
		if got := sess.GetState().FocusedWindowID; got == "hidden" {
			t.Fatal("the focus cycle landed on the hidden scratch terminal")
		}
	}
	_ = sess.mutateState(func(s *SessionState) error { s.Windows[1].Workspace = 5; return nil })
	if err := sess.FocusDaemonWindow("hidden"); err != nil {
		t.Fatal(err)
	}
	st := sess.GetState()
	w := st.Windows[1]
	if st.FocusedWindowID != "hidden" || w.Minimized || w.Workspace != 2 || st.CurrentWorkspace != 2 {
		t.Fatalf("focus=%s minimized=%v workspace=%d current=%d, want it shown on workspace 2",
			st.FocusedWindowID, w.Minimized, w.Workspace, st.CurrentWorkspace)
	}
}

// A push may change the scratch terminal's size, which the show reads from
// [scratch]. It may not change another popup's.
func TestScratchSizeTravelsInAPush(t *testing.T) {
	canonical := &SessionState{Windows: []WindowState{
		{ID: "scratch", Popup: true, Scratch: true, PopupWidth: "80%", PopupHeight: "80%"},
		{ID: "fzf", Popup: true, PopupWidth: "60%", PopupHeight: "40%"},
	}}
	incoming := &SessionState{Windows: []WindowState{
		{ID: "scratch", PopupWidth: "50", PopupHeight: "10"},
		{ID: "fzf", PopupWidth: "50", PopupHeight: "10"},
	}}
	retainDaemonExclusive(incoming, canonical)
	if s := incoming.Windows[0]; s.PopupWidth != "50" || s.PopupHeight != "10" {
		t.Fatalf("scratch size = %s x %s, want the pushed 50 x 10", s.PopupWidth, s.PopupHeight)
	}
	if f := incoming.Windows[1]; f.PopupWidth != "60%" || f.PopupHeight != "40%" {
		t.Fatalf("popup size = %s x %s, want its own 60%% x 40%%", f.PopupWidth, f.PopupHeight)
	}
}

// A session has one scratch pane per name: the built-in one and a named one
// live side by side, and a second of either name is refused.
func TestScratchPanesByName(t *testing.T) {
	sess := newTestSession(t)
	add := func(name string) error {
		_, err := sess.AddDaemonWindowWith(NewWindowOptions{
			Title: "s", Popup: true, Scratch: true, ScratchName: name, Command: []string{"sleep", "30"},
		}, nil)
		return err
	}
	if err := add(""); err != nil {
		t.Fatal(err)
	}
	if err := add("lazygit"); err != nil {
		t.Fatalf("a named scratch pane beside the built-in one: %v", err)
	}
	if err := add("scratch"); !errors.Is(err, ErrScratchExists) {
		t.Fatalf("a second built-in scratch: %v", err)
	}
	if err := add("lazygit"); !errors.Is(err, ErrScratchExists) {
		t.Fatalf("a second lazygit scratch: %v", err)
	}
	names := map[string]bool{}
	for _, w := range sess.GetState().Windows {
		names[w.ScratchKey()] = w.Scratch
	}
	if !names["scratch"] || !names["lazygit"] {
		t.Fatalf("scratch panes = %v", names)
	}
}

// A restore keeps the built-in scratch terminal and drops a named one: the
// named one ran a command, and its key starts it again.
func TestRestoreDropsANamedScratch(t *testing.T) {
	tmpDir := t.TempDir()
	defer useResurrectionDir(tmpDir)()
	cwd := t.TempDir()
	saved := &SessionState{
		Name: "named-scratch", CurrentWorkspace: 1, Width: 120, Height: 40,
		Windows: []WindowState{
			{ID: "pane", Width: 60, Height: 40, Workspace: 1, PTYID: "d1", Cwd: cwd},
			{ID: "builtin", Width: 80, Height: 30, Workspace: 1, PTYID: "d2", Cwd: cwd, Popup: true, IsFloating: true, Scratch: true},
			{ID: "lazygit", Width: 80, Height: 30, Workspace: 1, PTYID: "d3", Cwd: cwd, Popup: true, IsFloating: true, Scratch: true, ScratchName: "lazygit"},
		},
	}
	if err := SaveSessionForResurrection(saved); err != nil {
		t.Fatal(err)
	}
	d := NewDaemon(&DaemonConfig{})
	d.restoreAllSessions()
	defer d.manager.Shutdown()
	sess := d.manager.GetSession("named-scratch")
	if sess == nil {
		t.Fatal("not restored")
	}
	ids := map[string]bool{}
	for _, w := range sess.GetState().Windows {
		ids[w.ID] = true
	}
	if !ids["builtin"] || ids["lazygit"] {
		t.Fatalf("restored = %v, want the built-in scratch and not lazygit", ids)
	}
}

// A scratch command that stops within the wait is closed before the answer,
// so a press right after the report starts it again instead of being refused
// as a second scratch of the name.
//
// Negative control, confirmed red: drop the CloseDaemonWindow call in
// verbPopup and the scratch window is still in the state when the answer
// arrives.
func TestStoppedScratchIsClosedBeforeTheAnswer(t *testing.T) {
	d, sp := startTestDaemon(t)
	sess := makeSessionWithWindow(t, d, "work")
	attachTUI(t, sp, "work")
	c := dialVerb(t, sp)
	call := `{"id":1,"verb":"popup","params":{"session":"work","scratch":true,"scratch_name":"broken","command":["sh","-c","exit 7"],"wait":true,"timeout":1500}}`
	for try := 1; try <= 2; try++ {
		res := result(t, c.call(t, call))
		if res["type"] != "popup_result" || res["exit_code"] != float64(7) {
			t.Fatalf("try %d: answer = %v", try, res)
		}
		for _, w := range sess.GetState().Windows {
			if w.Scratch && w.ScratchKey() == "broken" {
				t.Fatalf("try %d: the stopped scratch is still in the state at the answer", try)
			}
		}
	}
}
