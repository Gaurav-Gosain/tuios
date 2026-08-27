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

// These read the rail's composed output rather than its state. Model state and
// pixels disagreeing is the failure this codebase keeps hitting, so the files
// section's claims about where its rows land are checked by looking at what is
// drawn.

// railLines renders the rail and returns its rows with the styling stripped.
func railLines(t *testing.T, m *OS) []string {
	t.Helper()
	lines, w := m.sidebarPanelLines()
	if w <= 0 || lines == nil {
		t.Fatalf("the rail reserved no columns (w=%d)", w)
	}
	out := make([]string, len(lines))
	for i, ln := range lines {
		out[i] = ansi.Strip(ln)
	}
	return out
}

// openFilesOn puts the files section on the rail and waits for its first
// listing, so a render test has names to look at. It drives the real command,
// which is the only way the entries ever arrive in the app.
func openFilesOn(t *testing.T, m *OS, dir string) {
	t.Helper()
	if !m.OpenFileView(dir) {
		t.Fatal("OpenFileView refused a rail that is on and expanded")
	}
	cmd := m.TakeSidebarCmd()
	if cmd == nil {
		t.Fatal("opening the section scheduled no read")
	}
	msg, ok := cmd().(fileListMsg)
	if !ok {
		t.Fatalf("the read answered with %T, not a listing", msg)
	}
	m.HandleFileList(msg)
}

// TestFilesSectionSitsBesideTheOtherSections is the design decision this branch
// changed, checked where it matters: the listing is a section on the rail, not
// a mode that hides the rail's other sections.
//
// It replaces TestFileViewReplacesTheSectionsOnScreen, which pinned exactly the
// opposite and had to go: it asserted that opening the listing took "sessions"
// and "terminals" off the screen. That was the old design and the maintainer
// asked for the other one, so the test is inverted rather than deleted, and the
// half of it that is still true (the header, the folders, the files, the path,
// and the way back to a rail without the section) is kept.
//
// Negative control, confirmed red: skip the files section in the layout draw
// loop, and the rail keeps its three sections but never draws a listing.
func TestFilesSectionSitsBesideTheOtherSections(t *testing.T) {
	m := sidebarTestOS(t, 120, 40, "left")

	before := strings.Join(railLines(t, m), "\n")
	if !strings.Contains(before, "sessions") || !strings.Contains(before, "terminals") {
		t.Fatalf("the rail is not drawing its sections to begin with:\n%s", before)
	}

	dir := fileViewTree(t)
	openFilesOn(t, m, dir)
	after := strings.Join(railLines(t, m), "\n")

	for _, want := range []string{"sessions", "terminals", "files", "apple/", "README.md"} {
		if !strings.Contains(after, want) {
			t.Errorf("the rail with a files section does not show %q:\n%s", want, after)
		}
	}
	// The header names the directory, cut from the front so the last component
	// always survives.
	if !strings.Contains(after, filepath.Base(dir)) {
		t.Errorf("the files header does not name the directory %q:\n%s", dir, after)
	}

	// And back again: switching the section off restores exactly what was there
	// before, with no listing left on the rail.
	m.CloseFileView()
	restored := strings.Join(railLines(t, m), "\n")
	if !strings.Contains(restored, "sessions") || !strings.Contains(restored, "terminals") {
		t.Errorf("switching the files section off took the other sections with it:\n%s", restored)
	}
	if strings.Contains(restored, "apple/") {
		t.Errorf("a rail with the files section off is still drawing a listing:\n%s", restored)
	}
}

// TestFileViewRowsAreClickableWhereTheyAreDrawn. Every rail rectangle is
// recorded by the renderer as it draws, and this is the check that the two
// agree: the row the pointer lands on is the entry that is printed there.
//
// The expected name comes from the drawn line, not from the entry list.
//
// Negative control: with recordHit called after the line is appended rather than
// before, every rectangle is one row low and this fails on the first entry.
func TestFileViewRowsAreClickableWhereTheyAreDrawn(t *testing.T) {
	m := sidebarTestOS(t, 120, 40, "left")
	openFilesOn(t, m, fileViewTree(t))
	lines := railLines(t, m)
	top := m.GetTopMargin()

	found := 0
	for _, h := range m.SidebarHits {
		if h.Kind != sidebarRowFileEntry {
			continue
		}
		row := h.Y0 - top
		if row < 0 || row >= len(lines) {
			t.Fatalf("an entry rectangle is at row %d, outside the %d the rail drew", row, len(lines))
		}
		name := m.filesView.Entries[h.WindowIndex].Name
		if !strings.Contains(lines[row], name) {
			t.Errorf("the rectangle for %q is on row %d, which reads %q", name, row, lines[row])
		}
		found++
	}
	if found != len(wantFileOrder) {
		t.Errorf("recorded %d entry rectangles for %d entries", found, len(wantFileOrder))
	}
}

// TestFileViewClickWalksIntoAFolder drives the mouse the way a user does:
// through SidebarClick, against the rectangles the last render published.
func TestFileViewClickWalksIntoAFolder(t *testing.T) {
	root := fileViewTree(t)
	m := sidebarTestOS(t, 120, 40, "left")
	openFilesOn(t, m, root)
	railLines(t, m)

	var target sidebarRowHit
	for _, h := range m.SidebarHits {
		if h.Kind == sidebarRowFileEntry && m.filesView.Entries[h.WindowIndex].Name == "apple" {
			target = h
			break
		}
	}
	if target.Y1 == 0 {
		t.Fatal("no rectangle was recorded for the apple folder")
	}
	if !m.SidebarClick(target.X0+1, target.Y0, false) {
		t.Fatal("the rail did not consume a click on one of its own rows")
	}
	// The click schedules the read; the loop runs it and hands the reply back.
	cmd := m.TakeSidebarCmd()
	if cmd == nil {
		t.Fatal("the click on a folder scheduled no read")
	}
	m.HandleFileList(cmd().(fileListMsg))
	if got, want := m.FileViewDir(), filepath.Join(root, "apple"); got != want {
		t.Errorf("the click left the view at %q, want %q", got, want)
	}
}

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
			m := &OS{Windows: []*terminal.Window{win}}
			m.filesView.Show = 1
			m.filesView.Origin = win.ID
			m.loadFileViewNow(t, root)

			prev := config.SidebarFolderClick
			config.SidebarFolderClick = tc.mode
			t.Cleanup(func() { config.SidebarFolderClick = prev })

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

// TestFileViewShowsAnUnreadableDirectory: the rail says what went wrong where
// the names would have been, rather than drawing an empty folder, which is a
// different and misleading fact.
func TestFileViewShowsAnUnreadableDirectory(t *testing.T) {
	m := sidebarTestOS(t, 120, 40, "left")
	openFilesOn(t, m, filepath.Join(t.TempDir(), "not-there"))
	out := strings.Join(railLines(t, m), "\n")
	if !strings.Contains(out, "gone") {
		t.Errorf("the rail did not say the folder is gone:\n%s", out)
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

// TestTruncPathLeftKeepsTheTail. Every other truncation on the rail keeps the
// head; a path is the other way round, because the last component is the
// directory you are in.
//
// The expected strings are written out by hand from the input.
func TestTruncPathLeftKeepsTheTail(t *testing.T) {
	const path = "/home/u/dev/tuios/internal"

	if got := truncPathLeft(path, 40); got != path {
		t.Errorf("a path that fits was cut: %q", got)
	}
	if got := truncPathLeft(path, len(path)); got != path {
		t.Errorf("a path that exactly fits was cut: %q", got)
	}
	got := truncPathLeft(path, 12)
	if !strings.HasSuffix(got, "internal") {
		t.Errorf("truncPathLeft(%q, 12) = %q, which lost the tail", path, got)
	}
	if ansi.StringWidth(got) > 12 {
		t.Errorf("truncPathLeft(%q, 12) = %q, %d cells wide", path, got, ansi.StringWidth(got))
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

// TestAFoldedRailDrawsNoListing.
//
// Three columns cannot draw a path or a name, so the strip does not draw the
// files section. This replaces TestTheFileViewAndTheFoldedRailAreExclusive,
// whose second half pinned the old rule that folding the rail switched the
// listing off: that rule existed because the listing was a mode, and a folded
// rail left in a mode had nothing on screen mentioning it and no way out. A
// section has no such trap, so folding now hides it and unfolding brings it
// back, exactly as it does the other three.
//
// Negative controls, both confirmed red: drop the width test from OpenFileView
// and the first half fails; put the CloseFileView call back in
// SidebarSetCollapsed, so folding switches the section off rather than hiding
// it, and unfolding never brings the listing back.
//
// The "a folded rail draws no listing" line in the middle is a guard rather
// than a measurement, and it is written down as one: the collapsed strip has
// its own renderer and returns before the section machinery runs at all, so no
// change inside the files section can make it draw one. Removing the width test
// from filesSectionEnabled leaves it green, which was run and confirmed.
func TestAFoldedRailDrawsNoListing(t *testing.T) {
	dir := fileViewTree(t)

	// A rail folded to its glyph strip refuses to open the section outright,
	// because the caller needs to know it has to do something else instead: a
	// folder link falls back to the clipboard.
	folded := sidebarTestOS(t, 120, 40, "left")
	folded.SidebarCollapsed = true
	if folded.OpenFileView(dir) {
		t.Error("the files section opened on a folded rail")
	}
	if cmd := folded.TakeSidebarCmd(); cmd != nil {
		t.Error("a refused OpenFileView still scheduled a directory read")
	}
	if out := strings.Join(railLines(t, folded), "\n"); strings.Contains(out, "apple/") {
		t.Errorf("a folded rail drew a listing it refused to open:\n%s", out)
	}

	// And folding an expanded rail that has the section hides the listing
	// rather than leaving half of it on a three-column strip.
	m := sidebarTestOS(t, 120, 40, "left")
	openFilesOn(t, m, dir)
	if out := strings.Join(railLines(t, m), "\n"); !strings.Contains(out, "apple/") {
		t.Fatalf("the expanded rail is not drawing the listing to begin with:\n%s", out)
	}
	m.SidebarSetCollapsed(true)
	if out := strings.Join(railLines(t, m), "\n"); strings.Contains(out, "apple/") {
		t.Errorf("a folded rail is still drawing a listing:\n%s", out)
	}
	// Unfolding brings it back, which is what makes hiding it safe.
	m.SidebarSetCollapsed(false)
	if cmd := m.TakeSidebarCmd(); cmd != nil {
		if msg, ok := cmd().(fileListMsg); ok {
			m.HandleFileList(msg)
		}
	}
	if out := strings.Join(railLines(t, m), "\n"); !strings.Contains(out, "apple/") {
		t.Errorf("unfolding the rail did not bring the listing back:\n%s", out)
	}
}

// TestAnEmptyFolderSaysSo. A folder with nothing in it draws a ".." row and then
// a gap, and a gap is also what a listing that failed to draw looks like. The
// word is the difference.
//
// Negative control, confirmed red: count the drawn rows rather than the names,
// and the ".." keeps the count above zero so the word never appears.
func TestAnEmptyFolderSaysSo(t *testing.T) {
	empty := filepath.Join(t.TempDir(), "nothing-here")
	if err := os.Mkdir(empty, 0o755); err != nil {
		t.Fatal(err)
	}

	m := sidebarTestOS(t, 120, 40, "left")
	openFilesOn(t, m, empty)
	out := strings.Join(railLines(t, m), "\n")
	if !strings.Contains(out, "empty") {
		t.Errorf("an empty folder drew no word for it:\n%s", out)
	}
	// And the way out is still there, so it is not a dead end.
	if !strings.Contains(out, "..") {
		t.Errorf("an empty folder drew no way back out:\n%s", out)
	}

	// A folder with names in it says nothing of the sort.
	openFilesOn(t, m, fileViewTree(t))
	if out := strings.Join(railLines(t, m), "\n"); strings.Contains(out, "empty") {
		t.Errorf("a five-name folder was called empty:\n%s", out)
	}
}
