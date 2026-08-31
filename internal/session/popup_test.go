package session

import "testing"

// TestPopupSizeGivesUpSpaceBeforeTheRegion is the layout rule this project
// already settled, applied to a size a person typed: a popup wider than the
// region it goes in is cut down to the region rather than drawn outside it, and
// the floor is given up on the same terms.
//
// Negative control, confirmed red: drop the `size = min(size, extent)` line in
// ResolvePopupSize. The 200-cell and 500-cell cases then report 200 and 500 in a
// region of 100, which is a pane drawn off the screen.
func TestPopupSizeGivesUpSpaceBeforeTheRegion(t *testing.T) {
	for _, tc := range []struct {
		name   string
		spec   string
		extent int
		floor  int
		want   int
	}{
		{"cells inside the region", "60", 100, PopupMinWidth, 60},
		{"a share of the region", "60%", 100, PopupMinWidth, 60},
		{"the whole region", "100%", 100, PopupMinWidth, 100},
		{"more cells than the region has", "200", 100, PopupMinWidth, 100},
		{"far more cells than the region has", "500", 100, PopupMinWidth, 100},
		{"a share of a tiny region", "50%", 12, PopupMinWidth, 10},
		{"a region smaller than the floor", "50%", 6, PopupMinWidth, 6},
		{"a region of one cell", "80%", 1, PopupMinWidth, 1},
		{"no region at all", "80%", 0, PopupMinWidth, 0},
		{"an unreadable spec falls back", "wide", 100, PopupMinWidth, 80},
		{"an empty spec falls back", "", 100, PopupMinWidth, 80},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := ResolvePopupSize(tc.spec, PopupDefaultWidth, tc.extent, tc.floor)
			if got != tc.want {
				t.Errorf("ResolvePopupSize(%q, extent %d) = %d, want %d",
					tc.spec, tc.extent, got, tc.want)
			}
			if got > tc.extent {
				t.Errorf("ResolvePopupSize(%q, extent %d) = %d, which is outside the region",
					tc.spec, tc.extent, got)
			}
		})
	}
}

// TestPopupSizeRefusesWhatItCannotMean holds the validation the verb reports
// back, so a mistyped flag is a message and not a popup of a size nobody asked
// for.
func TestPopupSizeRefusesWhatItCannotMean(t *testing.T) {
	for _, spec := range []string{"wide", "0", "-4", "120%", "60 cells", "%"} {
		if err := ValidatePopupSize(spec); err == nil {
			t.Errorf("ValidatePopupSize(%q) accepted it", spec)
		}
	}
	for _, spec := range []string{"", "1", "60", "60%", "100%", " 60 % "} {
		if err := ValidatePopupSize(spec); err != nil {
			t.Errorf("ValidatePopupSize(%q) refused it: %v", spec, err)
		}
	}
}

// TestClientSyncCannotUnmakeAPopup is the retention rule: the popup mark is
// stamped once, by the daemon, and a client snapshot that says nothing about it
// must not take it away.
//
// It matters because a client that did not know about popups - an older build,
// or one mid-upgrade - would push every window plain, and the mark going missing
// turns the popup into an ordinary pane that the next retile tiles into the box.
//
// Negative control, confirmed red: delete the popups map from
// retainDaemonExclusive. The incoming window keeps Popup false and IsFloating
// false, and the test names both.
func TestClientSyncCannotUnmakeAPopup(t *testing.T) {
	canonical := &SessionState{Windows: []WindowState{
		{ID: "w1", Popup: true, IsFloating: true, PopupWidth: "60%", PopupHeight: "40%"},
		{ID: "w2"},
	}}
	// What a client that knows nothing about popups sends back.
	incoming := &SessionState{Windows: []WindowState{
		{ID: "w1"},
		{ID: "w2"},
	}}

	retainDaemonExclusive(incoming, canonical)

	got := incoming.Windows[0]
	if !got.Popup {
		t.Error("a client sync unmade the popup")
	}
	if !got.IsFloating {
		t.Error("a client sync tiled the popup back into the box")
	}
	if got.PopupWidth != "60%" || got.PopupHeight != "40%" {
		t.Errorf("the popup lost the size it was asked for: %q x %q", got.PopupWidth, got.PopupHeight)
	}
	if incoming.Windows[1].Popup {
		t.Error("an ordinary window was marked a popup")
	}
}

// TestAPopupDoesNotSurviveTheDaemon is the other half of the lifetime rule. A
// popup holds one command, and a restore respawns a shell rather than the
// command it was given, so bringing a popup back would hand the user a floating
// box holding a shell nobody asked for and that will never exit.
//
// Negative control, confirmed red: remove the `if w.Popup { continue }` guard in
// restoreSession. The restored session comes back with two windows and the test
// names the popup that should not be there.
func TestAPopupDoesNotSurviveTheDaemon(t *testing.T) {
	tmpDir := t.TempDir()
	defer useResurrectionDir(tmpDir)()

	cwd := t.TempDir()
	saved := &SessionState{
		Name:             "transient",
		CurrentWorkspace: 1,
		Width:            120,
		Height:           40,
		Windows: []WindowState{
			{ID: "keep-me", Title: "shell", Width: 60, Height: 40, Workspace: 1, PTYID: "dead-1", Cwd: cwd},
			{ID: "the-popup", Title: "fzf", Width: 60, Height: 20, Workspace: 1, PTYID: "dead-2", Cwd: cwd,
				Popup: true, IsFloating: true, PopupWidth: "60%", PopupHeight: "40%"},
		},
	}
	if err := SaveSessionForResurrection(saved); err != nil {
		t.Fatalf("failed to save state: %v", err)
	}

	d := NewDaemon(&DaemonConfig{})
	d.restoreAllSessions()
	defer d.manager.Shutdown()

	sess := d.manager.GetSession("transient")
	if sess == nil {
		t.Fatal("session 'transient' was not restored")
	}
	state := sess.GetState()
	for _, w := range state.Windows {
		if w.Popup || w.ID == "the-popup" {
			t.Errorf("the popup came back from the dead as window %s", w.ID)
		}
	}
	if len(state.Windows) != 1 {
		t.Fatalf("restored %d windows, want only the one that was not a popup: %+v",
			len(state.Windows), state.Windows)
	}
	if state.Windows[0].ID != "keep-me" {
		t.Errorf("the restored window is %s, want keep-me", state.Windows[0].ID)
	}
}
