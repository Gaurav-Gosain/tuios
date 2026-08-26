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
// Negative control: with the filesView.Open branch removed from
// sidebarPanelLinesForTree, the rail keeps drawing "sessions" and the listing
// never appears; with the branch placed before the collapsed-strip return, a
// folded rail draws a path into three columns. NOT YET CONFIRMED RED.
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
