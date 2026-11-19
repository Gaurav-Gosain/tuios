package tuie2e

import (
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuitest"
	"tuios.dev/shot"
)

// shortMenuConfig opens the shipped short menu from leader t s, and draws a
// dock with no mode pill, as the Simple profile does.
const shortMenuConfig = `
[keybindings.window_prefix]
"open_menu:start" = ["s"]

[dock]
left = ["workspaces", "trail"]
`

// shortMenuShot saves the frame as text and as a PNG drawn by tuios.dev/shot.
func shortMenuShot(t *testing.T, term *tuitest.Terminal, name string) {
	t.Helper()
	saveFrame(t, term, name)
	savePNG(t, term.Screen(), shot.XTermPalette(), artifactDir(t), name)
}

// TestShortMenuKeepsItsOrder opens the short menu tuios ships. The panel
// title is "Menu" as the menu gives it, the rows are in the order the menu
// gives them, and a row runs its action.
//
// Negative control: sort the rows by key again in MenuKeybindingGroups. "All
// commands" (a) then comes before "New terminal" (n).
func TestShortMenuKeepsItsOrder(t *testing.T) {
	term, base := panesSession(t, "short-menu", shortMenuConfig)
	waitForSettledGeometryIn(t, base, "short-menu", 1)
	if err := term.SendKeys(tuitest.Ctrl('b'), "t", "s"); err != nil {
		t.Fatalf("send leader t s: %v", err)
	}
	order := []string{"New terminal", "Split side by side", "Split top and bottom", "Make this pane full screen",
		"Close this pane", "Find a file", "Select and copy text", "All commands", "Settings", "Help"}
	if err := term.WaitFor(func(s tuitest.Screen) bool { return screenHas(s, order...) }, uiTimeout); err != nil {
		t.Fatalf("the short menu never showed its rows: %v\n%s", err, term.Snapshot())
	}
	s := term.Screen()
	text := s.Text()
	if !strings.Contains(text, "Menu") || strings.Contains(text, " menu ") {
		t.Fatalf("the panel title is not \"Menu\"\n%s", term.Snapshot())
	}
	// The panel reads down each column, then across: a row is below the one
	// before it in the same column, or in a column to its right.
	_, rows := s.Size()
	where := func(label string) (x, y int) {
		for y := range rows {
			if i := strings.Index(s.Line(y), label); i >= 0 {
				return len([]rune(s.Line(y)[:i])), y
			}
		}
		return -1, -1
	}
	px, py := where(order[0])
	for _, row := range order[1:] {
		x, y := where(row)
		if !(x > px || (x == px && y > py)) {
			t.Fatalf("%q is out of the menu's order\n%s", row, term.Snapshot())
		}
		px, py = x, y
	}
	shortMenuShot(t, term, "short-menu")
	if err := term.SendKeys("n"); err != nil {
		t.Fatalf("send n: %v", err)
	}
	waitForSettledGeometryIn(t, base, "short-menu", 2)
}

// TestLeaderBadgeLeavesThePaneTitle: with no mode pill on the dock, the armed
// leader shows on the dock's rule row. It used to sit on the panes' top row,
// over the focused pane's title.
func TestLeaderBadgeLeavesThePaneTitle(t *testing.T) {
	term, base := panesSession(t, "leader-badge", shortMenuConfig)
	waitForSettledGeometryIn(t, base, "leader-badge", 1)
	if err := term.SendKeys(tuitest.Ctrl('b')); err != nil {
		t.Fatalf("send the leader: %v", err)
	}
	if err := term.WaitForText("PREFIX", uiTimeout); err != nil {
		t.Fatalf("the leader badge never showed: %v\n%s", err, term.Snapshot())
	}
	s := term.Screen()
	_, rows := s.Size()
	if !strings.Contains(s.Line(rows-2), "PREFIX") {
		t.Fatalf("the leader badge is not on the dock's rule row\n%s", term.Snapshot())
	}
	shortMenuShot(t, term, "leader-badge")
}
