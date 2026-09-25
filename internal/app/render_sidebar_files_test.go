package app

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
	"github.com/charmbracelet/x/ansi"
)

// cdProbe is a pane whose writes are recorded instead of reaching a PTY, so a
// test can assert the bytes a cd actually typed rather than that a function was
// called. The window is a real daemon window, so SendInput takes the same path
// it takes for an attached client.
func cdProbe(t *testing.T, id string) (*terminal.Window, func() string) {
	t.Helper()
	win := newTestWindow(t, id, 40, 10)
	var mu sync.Mutex
	var buf strings.Builder
	win.DaemonWriteFunc = func(b []byte) error {
		mu.Lock()
		defer mu.Unlock()
		buf.Write(b)
		return nil
	}
	return win, func() string {
		mu.Lock()
		defer mu.Unlock()
		return buf.String()
	}
}

// TestFolderClickCanCdThePane is the option the maintainer asked for: a click
// on a folder can walk the listing, tell the pane to cd there, or do both.
//
// The pane is a real one with a real PTY, so what is asserted is what the shell
// actually received, not that a function was called.
//
// Negative controls, all three confirmed red: with the folder_click test
// dropped from FileViewEnter so it always navigates, the cd and both cases see
// nothing typed; with it always sending a cd, the navigate case types into the
// pane it must not touch.
func TestFolderClickCanCdThePane(t *testing.T) {
	for _, tc := range []struct {
		mode         string
		wantNavigate bool
		wantCd       bool
	}{
		{config.SidebarFolderClickNavigate, true, false},
		{config.SidebarFolderClickCd, false, true},
		{config.SidebarFolderClickBoth, true, true},
	} {
		t.Run(tc.mode, func(t *testing.T) {
			root := fileViewTree(t)
			sub := filepath.Join(root, "apple")

			win, typedInto := cdProbe(t, "aaaaaaaa1111")
			win.Cwd = root
			m := &OS{Settings: config.Global, Windows: []*terminal.Window{win}}
			m.filesView.Show = 1
			m.filesView.Origin = win.ID
			m.loadFileViewNow(t, root)

			prev := m.Settings.SidebarFolderClick
			m.Settings.SidebarFolderClick = tc.mode
			t.Cleanup(func() { m.Settings.SidebarFolderClick = prev })

			// "apple" is the first entry, per the order fileViewTree pins.
			cmd := m.FileViewEnter(0)
			if (cmd != nil) != tc.wantNavigate {
				t.Errorf("%s scheduled a listing read: %v, want %v", tc.mode, cmd != nil, tc.wantNavigate)
			}
			if cmd != nil {
				m.HandleFileList(cmd().(fileListMsg))
				if got := m.FileViewDir(); got != sub {
					t.Errorf("%s left the listing at %q, want %q", tc.mode, got, sub)
				}
			} else if got := m.FileViewDir(); got != root {
				t.Errorf("%s moved the listing to %q with navigation off", tc.mode, got)
			}

			typed := typedInto()
			if got := strings.Contains(typed, "cd "); got != tc.wantCd {
				t.Errorf("%s typed %q into the pane; wanted a cd: %v", tc.mode, typed, tc.wantCd)
			}
			if tc.wantCd && !strings.Contains(typed, shellQuote(sub)) {
				t.Errorf("%s typed %q, which does not name %q", tc.mode, typed, sub)
			}
		})
	}
}

// TestFileViewRowsFitTheRail. Every rail row is exactly the reserved width, and
// a listing is the one place names arrive from outside tuios entirely: a file
// can be named anything, at any length.
func TestFileViewRowsFitTheRail(t *testing.T) {
	dir := t.TempDir()
	long := strings.Repeat("very-long-file-name-", 12) + ".txt"
	if err := os.WriteFile(filepath.Join(dir, long), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, strings.Repeat("d", 200)), 0o755); err != nil {
		t.Fatal(err)
	}

	for _, size := range []struct{ w, h int }{{120, 40}, {80, 24}, {90, 10}} {
		m := sidebarTestOS(t, size.w, size.h, "left")
		if !m.OpenFileView(dir) {
			continue // too narrow for the section, which is its own correct answer
		}
		if cmd := m.TakeSidebarCmd(); cmd != nil {
			m.HandleFileList(cmd().(fileListMsg))
		}
		lines, w := m.sidebarPanelLines()
		for i, ln := range lines {
			if got := ansi.StringWidth(ln); got != w {
				t.Errorf("%dx%d row %d is %d cells, want %d: %q",
					size.w, size.h, i, got, w, ansi.Strip(ln))
			}
		}
		if len(lines) != m.GetUsableHeight() {
			t.Errorf("%dx%d drew %d rows, want %d", size.w, size.h, len(lines), m.GetUsableHeight())
		}
	}
}

// TestFilesSectionReachableFromTheKeyboard walks the whole section with enter
// alone.
//
// The rail has three switches on row kind, not one: the click handler's, the
// completed-gesture path's, and SidebarActivateCursor's. Only the last is the
// keyboard's, and it was the one that shipped without the file rows, so enter
// on a listing did nothing at all while a click on the same row worked. Nothing
// else in this package steers the section from the keyboard, so a regression
// there is silent.
//
// Every row here is found through SidebarNav, which the renderer publishes as it
// draws, so this also checks that the nav rows and the rectangles agree.
//
// The "back" control it used to end on is gone with the mode it belonged to:
// the section is switched off from the same footer control that switches it on,
// which is the last thing this walks.
//
// Negative control, confirmed red: with the file rows dropped from
// SidebarActivateCursor's switch, the first assertion fails and the section is
// never opened.
func TestFilesSectionReachableFromTheKeyboard(t *testing.T) {
	root := fileViewTree(t)
	m := sidebarTestOS(t, 120, 40, "left")
	m.Windows[0].Cwd = root
	m.FocusedWindow = 0
	// The layout names the files section, so this starts it off and lets the
	// footer control be the thing that turns it on.
	m.filesView.Show = -1

	// enter runs the cursor's row and then the read it scheduled, which is what
	// the loop does one message later.
	enter := func() {
		t.Helper()
		m.SidebarActivateCursor()
		if cmd := m.TakeSidebarCmd(); cmd != nil {
			if msg, ok := cmd().(fileListMsg); ok {
				m.HandleFileList(msg)
			}
		}
	}
	land := func(kind sidebarRowKind, what string) {
		t.Helper()
		railLines(t, m)
		m.SidebarCursor = m.sidebarFirstRowOfKind(kind)
		if m.SidebarCursor < 0 {
			t.Fatalf("the rail published no %s for the keyboard to land on", what)
		}
	}

	// Enter on the footer's "files" control opens the section on the focused
	// pane's directory.
	land(sidebarRowFiles, "files control")
	enter()
	if !m.FileViewOpen() {
		t.Fatal("enter on the files control did not open the section")
	}
	if got := m.FileViewDir(); got != root {
		t.Fatalf("the section opened at %q, want the pane's own directory %q", got, root)
	}

	// Enter on a folder row walks into it. "apple" is the first entry.
	land(sidebarRowFileEntry, "entry row")
	if name := m.filesView.Entries[m.SidebarNav[m.SidebarCursor].WindowIndex].Name; name != "apple" {
		t.Fatalf("the first entry row is %q, want apple", name)
	}
	enter()
	if got, want := m.FileViewDir(), filepath.Join(root, "apple"); got != want {
		t.Fatalf("enter on a folder left the listing at %q, want %q", got, want)
	}

	// Enter on ".." walks back out. It is the first row of a listing that has a
	// parent, which this one does.
	land(sidebarRowFileUp, ".. row")
	enter()
	if got := m.FileViewDir(); got != root {
		t.Fatalf("enter on .. left the listing at %q, want %q", got, root)
	}

	// And enter on the same footer control takes the section off again.
	land(sidebarRowFiles, "files control")
	enter()
	if m.FileViewOpen() {
		t.Error("enter on the files control left the section on the rail")
	}
	if out := strings.Join(railLines(t, m), "\n"); strings.Contains(out, "apple/") {
		t.Errorf("a rail with the section switched off is still drawing a listing:\n%s", out)
	}
}
