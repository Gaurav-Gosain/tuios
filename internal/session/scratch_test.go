package session

import (
	"errors"
	"strings"
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

// Every scratch group survives a daemon restart, hidden: its panes come back
// as fresh shells on the group's workspace, with the group's tree. A scratch
// pane saved as a popup, before groups had a workspace, moves to one. Any
// other popup is still dropped.
//
// Negative control, confirmed red: drop the scratch branch in restoreSession
// and the legacy popup stays a popup on workspace 2.
func TestScratchTerminalSurvivesTheDaemonHidden(t *testing.T) {
	tmpDir := t.TempDir()
	defer useResurrectionDir(tmpDir)()

	cwd := t.TempDir()
	ws := ScratchWorkspaceBase
	saved := &SessionState{
		Name:             "keeps-scratch",
		CurrentWorkspace: 1,
		Width:            120,
		Height:           40,
		Windows: []WindowState{
			{ID: "pane", Title: "shell", Width: 60, Height: 40, Workspace: 1, PTYID: "dead-1", Cwd: cwd},
			{ID: "legacy", Title: "scratch", Width: 80, Height: 30, Workspace: 2, PTYID: "dead-2", Cwd: cwd,
				Popup: true, IsFloating: true, Scratch: true, Minimized: true},
			{ID: "g1", Width: 40, Height: 30, Workspace: ws + 1, PTYID: "dead-3", Cwd: cwd, Scratch: true, ScratchName: "logs"},
			{ID: "g2", Width: 40, Height: 30, Workspace: ws + 1, PTYID: "dead-4", Cwd: cwd, Scratch: true, ScratchName: "logs"},
			{ID: "fzf", Title: "fzf", Width: 60, Height: 20, Workspace: 1, PTYID: "dead-5", Cwd: cwd,
				Popup: true, IsFloating: true},
		},
		FocusedWindowID: "g1",
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
	st := sess.GetState()
	byID := map[string]WindowState{}
	for _, w := range st.Windows {
		byID[w.ID] = w
	}
	if _, ok := byID["fzf"]; ok {
		t.Error("a plain popup came back from the dead")
	}
	for _, id := range []string{"legacy", "g1", "g2"} {
		w, ok := byID[id]
		if !ok {
			t.Fatalf("scratch pane %s did not come back: %v", id, byID)
		}
		if !w.Scratch || w.Popup || w.Minimized || !IsScratchWorkspace(w.Workspace) || w.PTYID == "" || strings.HasPrefix(w.PTYID, "dead") {
			t.Fatalf("restored %s = %+v, want a tiled scratch pane with a new shell on a scratch workspace", id, w)
		}
	}
	if byID["g1"].Workspace != ws+1 || byID["g2"].ScratchName != "logs" {
		t.Fatalf("the logs group moved: %+v", byID["g1"])
	}
	if byID["legacy"].Workspace == ws+1 {
		t.Fatal("the legacy scratch joined the logs group")
	}
	if st.FocusedWindowID == "g1" || st.CurrentWorkspace != 1 {
		t.Fatalf("focus=%s current=%d, want the groups hidden", st.FocusedWindowID, st.CurrentWorkspace)
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

// The daemon's focus cycle stays on the session's workspace, so it never
// reaches a scratch group. A focus by id on a scratch pane takes the focus
// without making the group's workspace the session's: a client shows the
// group over the workspace it is on.
func TestDaemonFocusAndHiddenScratch(t *testing.T) {
	sess := newTestSession(t)
	state := &SessionState{CurrentWorkspace: 2, Windows: []WindowState{
		{ID: "a", Workspace: 2},
		{ID: "hidden", Workspace: ScratchWorkspaceBase, Scratch: true},
		{ID: "b", Workspace: 2},
	}, FocusedWindowID: "a"}
	_ = sess.mutateState(func(s *SessionState) error { *s = *state; return nil })

	for range 4 {
		if err := sess.CycleDaemonFocus(1); err != nil {
			t.Fatal(err)
		}
		if got := sess.GetState().FocusedWindowID; got == "hidden" {
			t.Fatal("the focus cycle landed on a scratch pane")
		}
	}
	if err := sess.FocusDaemonWindow("hidden"); err != nil {
		t.Fatal(err)
	}
	st := sess.GetState()
	if st.FocusedWindowID != "hidden" || st.CurrentWorkspace != 2 {
		t.Fatalf("focus=%s current=%d, want the scratch pane focused over workspace 2", st.FocusedWindowID, st.CurrentWorkspace)
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

// A split inside a group joins the group, a window cannot be put on a
// scratch workspace no group is on, and a new pane focused there does not
// move the session's workspace.
func TestNewWindowOnAScratchWorkspace(t *testing.T) {
	sess := newTestSession(t)
	first, err := sess.AddDaemonWindowWith(NewWindowOptions{Scratch: true, ScratchName: "logs", Command: []string{"sleep", "30"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !IsScratchWorkspace(first.Workspace) || first.Popup {
		t.Fatalf("group pane = %+v, want a tiled pane on a scratch workspace", first)
	}
	split, err := sess.AddDaemonWindowWith(NewWindowOptions{Workspace: first.Workspace, Focus: true, Command: []string{"sleep", "30"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !split.Scratch || split.ScratchName != "logs" || split.Workspace != first.Workspace {
		t.Fatalf("split = %+v, want it in the logs group", split)
	}
	if st := sess.GetState(); IsScratchWorkspace(st.CurrentWorkspace) {
		t.Fatalf("the session's workspace became %d", st.CurrentWorkspace)
	}
	if _, err := sess.AddDaemonWindowWith(NewWindowOptions{Workspace: first.Workspace + 7, Command: []string{"sleep", "30"}}, nil); err == nil {
		t.Fatal("a window went on a scratch workspace with no group")
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
