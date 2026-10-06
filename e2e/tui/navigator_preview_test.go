package tuie2e

import (
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// The checks behind the navigator's look. The failure modes they are written
// against are listed at the top of navigator_look_test.go.

// navFind is where marker starts on row y at or after column from, by cell,
// or -1. The markers are ASCII, one cell a character.
func navFind(s tuitest.Screen, y int, marker string, from int) int {
	cols, _ := s.Size()
	for x := from; x+len(marker) <= cols; x++ {
		ok := true
		for i := range len(marker) {
			if s.Cell(x+i, y).Content != marker[i:i+1] {
				ok = false
				break
			}
		}
		if ok {
			return x
		}
	}
	return -1
}

// navColumn is the cell column a byte offset of row y's text falls on.
func navColumn(s tuitest.Screen, y, offset int) int {
	return len([]rune(s.Line(y)[:offset]))
}

// navPreviewBox is the preview box's left border column and its top row,
// or -1 when the navigator is not up.
func navPreviewBox(s tuitest.Screen) (x, y int) {
	_, rows := s.Size()
	for y := range rows {
		if i := strings.Index(s.Line(y), "╭─ Preview"); i >= 0 {
			return navColumn(s, y, i), y
		}
	}
	return -1, -1
}

// navPreviewInk is the foreground of the first cell of marker inside the
// preview box, and whether the preview shows marker at all.
func navPreviewInk(s tuitest.Screen, marker string) (tuitest.Color, bool) {
	px, top := navPreviewBox(s)
	if px < 0 {
		return tuitest.Color{}, false
	}
	_, rows := s.Size()
	for y := top + 1; y < rows; y++ {
		if x := navFind(s, y, marker, px+1); x >= 0 {
			return s.Cell(x, y).Fg, true
		}
	}
	return tuitest.Color{}, false
}

// navIsSlot reports whether c is one of the palette slots in slots.
func navIsSlot(c tuitest.Color, slots ...uint8) bool {
	if c.Kind != tuitest.ColorIndexed {
		return false
	}
	for _, s := range slots {
		if c.Index == s {
			return true
		}
	}
	return false
}

// TestNavigatorPreviewKeepsColour: the preview shows a pane's text in the
// colour the pane printed it in. A pane in another session is read with a
// styled capture and prints red, SGR 31. A pane in the attached session is
// read from the client's own cells and prints green, SGR 32. No theme is on,
// so both reach the host as palette slots, and the chrome uses neither.
func TestNavigatorPreviewKeepsColour(t *testing.T) {
	base, _ := navigatorSessions(t)
	if o, err := tuiosCLI(t, base, "new-window", "paint", "-s", "work", "--no-focus"); err != nil {
		t.Fatalf("new-window: %v\n%s", err, o)
	}
	runShown(t, base, "work", "paint", `clear; printf '\033[31m%s\033[0m\n' RED-4417`, "RED-4417")
	runShown(t, base, "home", focusedIn(t, base, "home"), `clear; printf '\033[32m%s\033[0m\n' GRN-5523`, "GRN-5523")
	term := navClient(t, base, []string{"TERM=xterm-256color", "COLORTERM=truecolor"})
	dir := artifactDir(t)

	for _, c := range []struct {
		marker, where string
		slots         []uint8
	}{
		{"RED-4417", "another session", []uint8{1, 9}},
		{"GRN-5523", "the attached session", []uint8{2, 10}},
	} {
		openNavigator(t, term)
		searchNavigator(t, term, c.marker, c.marker)
		var ink tuitest.Color
		if err := term.WaitFor(func(s tuitest.Screen) bool {
			var ok bool
			ink, ok = navPreviewInk(s, c.marker)
			return ok && navIsSlot(ink, c.slots...)
		}, uiTimeout); err != nil {
			t.Fatalf("the preview of a pane in %s does not show %s in slot %v: the ink is %+v\n%s", c.where, c.marker, c.slots, ink, term.Snapshot())
		}
		saveArtifact(t, term, dir, c.marker)
		savePNG(t, term.Screen(), hostPalette(t, ""), dir, c.marker)
		sendKeys(t, term, tuitest.Esc, tuitest.Esc, tuitest.Esc)
		if err := term.WaitFor(func(s tuitest.Screen) bool { return !strings.Contains(s.Text(), "Press / to search") }, uiTimeout); err != nil {
			t.Fatalf("esc did not close the navigator: %v\n%s", err, term.Snapshot())
		}
	}
}

// TestNavigatorPreviewIsInert: a pane in another session prints a link (OSC
// 8), a clipboard write (OSC 52) and a title (OSC 2) around its text. The
// styled capture carries the link to the client. The preview shows the text,
// and none of the three reaches the host terminal.
func TestNavigatorPreviewIsInert(t *testing.T) {
	base, _ := navigatorSessions(t)
	if o, err := tuiosCLI(t, base, "new-window", "inject", "-s", "work", "--no-focus"); err != nil {
		t.Fatalf("new-window: %v\n%s", err, o)
	}
	runShown(t, base, "work", "inject",
		`clear; printf '\033]8;;http://evil.example/6620\033\\%s\033]8;;\033\\\033]52;c;SU5KRUNURUQ=\007\033]2;PWNTITLE\007\n' LINK-6620`,
		"LINK-6620")
	host := &paneHostCopy{}
	term := attachIn(t, base, "home", startOpts{cols: 140, rows: 40, out: host})
	if err := term.WaitFor(func(s tuitest.Screen) bool { return countWindows(s) == 1 }, bootTimeout); err != nil {
		t.Fatalf("client never attached: %v\n%s", err, term.Snapshot())
	}
	openNavigator(t, term, "work")
	searchNavigator(t, term, "LINK-6620", "inject")
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		_, ok := navPreviewInk(s, "LINK-6620")
		return ok
	}, uiTimeout); err != nil {
		t.Fatalf("the preview never showed the pane's text: %v\n%s", err, term.Snapshot())
	}
	saveFrame(t, term, "navigator-inert")
	// A beat for any host write that trails the frame.
	time.Sleep(500 * time.Millisecond)
	out := host.String()
	for _, bad := range []string{"evil.example", "\x1b]52;", "PWNTITLE", "SU5KRUNURUQ"} {
		if strings.Contains(out, bad) {
			t.Fatalf("the navigator's preview wrote %q to the host terminal", bad)
		}
	}
}

// TestNavigatorDrawsTheTree: the tree joins each workspace to its session
// and each pane to its workspace with guides, the last of each with the
// closing guide, and the row under the cursor, and only that row, carries
// the bar and a ground of its own.
func TestNavigatorDrawsTheTree(t *testing.T) {
	base, _ := navigatorSessions(t)
	term := navClient(t, base, []string{"TERM=xterm-256color", "COLORTERM=truecolor"})
	openNavigator(t, term, "home", "work")
	sendKeys(t, term, "G", "l")
	waitScreen(t, term, "work did not open", "logs")
	if err := term.WaitStable(uiTimeout); err != nil {
		t.Fatalf("the screen never settled: %v", err)
	}
	saveFrame(t, term, "navigator-tree-guides")
	s := term.Screen()
	px, top := navPreviewBox(s)
	if px < 0 {
		t.Fatalf("no preview box:\n%s", term.Snapshot())
	}
	// The list box's left border is the first │ on the row under its top.
	i := strings.Index(s.Line(top+1), "│")
	if i < 0 {
		t.Fatalf("no list box border:\n%s", term.Snapshot())
	}
	left := navColumn(s, top+1, i)
	_, rows := s.Size()

	branch, last, logsRow, barRows, barRow := 0, 0, -1, 0, -1
	for y := top + 1; y < rows; y++ {
		var b strings.Builder
		for x := left + 1; x < px-1; x++ {
			b.WriteString(s.Cell(x, y).Content)
		}
		row := b.String()
		branch += strings.Count(row, "├─")
		last += strings.Count(row, "└─")
		if strings.Contains(row, "logs") {
			logsRow = y
			if !strings.Contains(row, "├─") && !strings.Contains(row, "└─") {
				t.Fatalf("the logs pane has no guide: %q\n%s", row, term.Snapshot())
			}
			// logs sits at its prompt, so its command is its session's
			// shell, which the daemon names in list-windows.
			if !strings.Contains(row, "logs │ sh") {
				t.Fatalf("the logs pane does not say it runs sh: %q\n%s", row, term.Snapshot())
			}
		}
		// A pane with only the name tuios made up is called by its folder.
		if strings.Contains(row, "Terminal ") {
			t.Fatalf("a row shows a made-up pane name: %q\n%s", row, term.Snapshot())
		}
		if s.Cell(left+1, y).Content == "▎" {
			barRows++
			barRow = y
		}
	}
	if branch == 0 || last < 2 || logsRow < 0 {
		t.Fatalf("the tree has %d branch guides and %d closing guides, want some of each, and logs:\n%s", branch, last, term.Snapshot())
	}
	if barRows != 1 || !strings.Contains(s.Line(barRow), "work") {
		t.Fatalf("%d rows carry the bar, want one, on work:\n%s", barRows, term.Snapshot())
	}
	if sel, rest := s.Cell(px-3, barRow).Bg, s.Cell(px-3, logsRow).Bg; sel == rest {
		t.Fatalf("the cursor row's ground %+v is the ground of the other rows", sel)
	}
}
