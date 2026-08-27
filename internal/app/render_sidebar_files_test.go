package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

// These read the rail's composed output rather than its state. Model state and
// pixels disagreeing is the failure this codebase keeps hitting, so the mode's
// claim that it replaces the sections is checked by looking at what is drawn.

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

// TestFileViewReplacesTheSectionsOnScreen is the whole design decision, checked
// where it matters: the mode costs the three sections nothing when it is off,
// and takes the rail when it is on.
//
// Negative control, confirmed red: with the filesView.Open branch removed from
// sidebarPanelLinesForTree, the rail keeps drawing "sessions" and the listing
// never appears.
//
// The other control this comment used to name (move the branch above the
// collapsed-strip return, so a folded rail draws a path into three columns) was
// run and turns nothing red, because the mode can never be open on a folded
// rail: OpenFileView refuses to enter it there and collapsing leaves it. Those
// two guards are what the invariant actually rests on, and
// TestTheFileViewAndTheFoldedRailAreExclusive pins them.
func TestFileViewReplacesTheSectionsOnScreen(t *testing.T) {
	m := sidebarTestOS(t, 120, 40, "left")

	before := strings.Join(railLines(t, m), "\n")
	if !strings.Contains(before, "sessions") || !strings.Contains(before, "terminals") {
		t.Fatalf("the rail is not drawing its sections to begin with:\n%s", before)
	}

	dir := fileViewTree(t)
	if !m.OpenFileView(dir) {
		t.Fatal("OpenFileView refused a rail that is on and expanded")
	}
	after := strings.Join(railLines(t, m), "\n")

	if strings.Contains(after, "sessions") || strings.Contains(after, "terminals") {
		t.Errorf("the file view left the sections on screen:\n%s", after)
	}
	if !strings.Contains(after, "files") {
		t.Errorf("the file view drew no header:\n%s", after)
	}
	// Folders wear a trailing slash and come first, per fileViewTree's order.
	if !strings.Contains(after, "apple/") {
		t.Errorf("the listing does not show its folders:\n%s", after)
	}
	if !strings.Contains(after, "README.md") {
		t.Errorf("the listing does not show its files:\n%s", after)
	}
	// The path row names the directory, cut from the front so the last
	// component always survives.
	if !strings.Contains(after, filepath.Base(dir)) {
		t.Errorf("the path row does not name the directory %q:\n%s", dir, after)
	}

	// And back again: closing restores exactly what was there before.
	m.CloseFileView()
	restored := strings.Join(railLines(t, m), "\n")
	if !strings.Contains(restored, "sessions") || !strings.Contains(restored, "terminals") {
		t.Errorf("closing the file view did not bring the sections back:\n%s", restored)
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
	if !m.OpenFileView(fileViewTree(t)) {
		t.Fatal("OpenFileView refused")
	}
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
	if !m.OpenFileView(root) {
		t.Fatal("OpenFileView refused")
	}
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
	if got, want := m.FileViewDir(), filepath.Join(root, "apple"); got != want {
		t.Errorf("the click left the view at %q, want %q", got, want)
	}
}

// TestFileViewShowsAnUnreadableDirectory: the rail says what went wrong where
// the names would have been, rather than drawing an empty folder, which is a
// different and misleading fact.
func TestFileViewShowsAnUnreadableDirectory(t *testing.T) {
	m := sidebarTestOS(t, 120, 40, "left")
	if !m.OpenFileView(filepath.Join(t.TempDir(), "not-there")) {
		t.Fatal("OpenFileView refused")
	}
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
			continue // too narrow for the mode, which is its own correct answer
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

// TestFileViewReachableFromTheKeyboard walks the whole mode with enter alone.
//
// The rail has three switches on row kind, not one: the click handler's, the
// completed-gesture path's, and SidebarActivateCursor's. Only the last is the
// keyboard's, and it was the one that shipped without the file view's rows, so
// enter on a listing did nothing at all while a click on the same row worked.
// Nothing else in this package steers the mode from the keyboard, so a
// regression there is silent.
//
// Every row here is found through SidebarNav, which the renderer publishes as it
// draws, so this also checks that the nav rows and the rectangles agree.
//
// Negative control, confirmed red: with the file rows dropped from
// SidebarActivateCursor's switch, the first assertion fails and the mode is
// never entered.
func TestFileViewReachableFromTheKeyboard(t *testing.T) {
	root := fileViewTree(t)
	m := sidebarTestOS(t, 120, 40, "left")
	m.Windows[0].Cwd = root
	m.FocusedWindow = 0

	// Enter on the footer's "files" control opens the view on the focused
	// pane's directory.
	railLines(t, m)
	m.SidebarCursor = m.sidebarFirstRowOfKind(sidebarRowFiles)
	if m.SidebarCursor < 0 {
		t.Fatal("the rail published no files control for the keyboard to land on")
	}
	if exit := m.SidebarActivateCursor(); exit {
		t.Fatal("opening the file view asked to leave the rail")
	}
	if !m.FileViewOpen() {
		t.Fatal("enter on the files control did not open the view")
	}
	if got := m.FileViewDir(); got != root {
		t.Fatalf("the view opened at %q, want the pane's own directory %q", got, root)
	}

	// Enter on a folder row walks into it. "apple" is the first entry.
	railLines(t, m)
	m.SidebarCursor = m.sidebarFirstRowOfKind(sidebarRowFileEntry)
	if m.SidebarCursor < 0 {
		t.Fatal("the listing published no entry row")
	}
	if name := m.filesView.Entries[m.SidebarNav[m.SidebarCursor].WindowIndex].Name; name != "apple" {
		t.Fatalf("the first entry row is %q, want apple", name)
	}
	m.SidebarActivateCursor()
	if got, want := m.FileViewDir(), filepath.Join(root, "apple"); got != want {
		t.Fatalf("enter on a folder left the view at %q, want %q", got, want)
	}

	// Enter on ".." walks back out. It is the first row of a listing that has a
	// parent, which this one does.
	railLines(t, m)
	m.SidebarCursor = m.sidebarFirstRowOfKind(sidebarRowFileUp)
	if m.SidebarCursor < 0 {
		t.Fatal("a listing below the root published no .. row")
	}
	m.SidebarActivateCursor()
	if got := m.FileViewDir(); got != root {
		t.Fatalf("enter on .. left the view at %q, want %q", got, root)
	}

	// And enter on "back" leaves the mode, which is the way out a user who
	// arrived here by keyboard has to be able to find.
	railLines(t, m)
	m.SidebarCursor = m.sidebarFirstRowOfKind(sidebarRowFileBack)
	if m.SidebarCursor < 0 {
		t.Fatal("the file view published no back control")
	}
	m.SidebarActivateCursor()
	if m.FileViewOpen() {
		t.Error("enter on back left the rail in its file view")
	}
	if out := strings.Join(railLines(t, m), "\n"); !strings.Contains(out, "sessions") {
		t.Errorf("leaving the file view did not bring the sections back:\n%s", out)
	}
}

// TestTheFileViewAndTheFoldedRailAreExclusive.
//
// Three columns cannot draw a path, so a folded rail has no way to show the
// listing and no way out of it: a mode nothing on screen mentions is a mode the
// user cannot leave. Two guards keep the pair apart, and the mode's position in
// sidebarPanelLinesForTree relies on both of them holding.
//
// Negative control, confirmed red: drop the width test from OpenFileView and the
// first half fails; drop the CloseFileView call from SidebarSetCollapsed and the
// second half fails.
func TestTheFileViewAndTheFoldedRailAreExclusive(t *testing.T) {
	dir := fileViewTree(t)

	// A rail folded to its glyph strip refuses the mode outright.
	folded := sidebarTestOS(t, 120, 40, "left")
	folded.SidebarCollapsed = true
	if folded.OpenFileView(dir) {
		t.Error("the file view opened on a folded rail")
	}
	if folded.FileViewOpen() {
		t.Error("a refused OpenFileView still left the rail in its file view")
	}

	// And folding an expanded rail that is in the mode leaves the mode rather
	// than hiding it.
	m := sidebarTestOS(t, 120, 40, "left")
	if !m.OpenFileView(dir) {
		t.Fatal("OpenFileView refused an expanded rail")
	}
	m.SidebarSetCollapsed(true)
	if m.FileViewOpen() {
		t.Error("folding the rail hid the file view instead of leaving it")
	}
	if out := strings.Join(railLines(t, m), "\n"); strings.Contains(out, "apple/") {
		t.Errorf("a folded rail is still drawing a listing:\n%s", out)
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
	if !m.OpenFileView(empty) {
		t.Fatal("OpenFileView refused")
	}
	out := strings.Join(railLines(t, m), "\n")
	if !strings.Contains(out, "empty") {
		t.Errorf("an empty folder drew no word for it:\n%s", out)
	}
	// And the way out is still there, so it is not a dead end.
	if !strings.Contains(out, "..") {
		t.Errorf("an empty folder drew no way back out:\n%s", out)
	}

	// A folder with names in it says nothing of the sort.
	if !m.OpenFileView(fileViewTree(t)) {
		t.Fatal("OpenFileView refused the populated tree")
	}
	if out := strings.Join(railLines(t, m), "\n"); strings.Contains(out, "empty") {
		t.Errorf("a five-name folder was called empty:\n%s", out)
	}
}
