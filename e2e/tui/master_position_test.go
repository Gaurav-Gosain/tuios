package tuie2e

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// The master-stack layout with the master on any side, more than one master,
// and the keys and commands that change them (issue #321).
//
// Every test here runs against a daemon and reads the pane rectangles back
// with list-windows, in the order the session holds the windows. The first
// window is the one the session opened, so it is the master until a swap moves
// another pane into its place. Each test saves the frames it checks under
// artifactDir.

// masterSession makes a detached session in master-stack mode with appearance
// written into the config, attaches a client and opens windows until there are
// n. It returns the client.
func masterSession(t *testing.T, base, name, appearance string, n int) *tuitest.Terminal {
	t.Helper()
	writeConfig(t, base, "[startup]\nopen_default_window = true\ntiled = true\nlayout = \"master-stack\"\n"+
		"[appearance]\n"+appearance)
	if out, err := tuiosCLI(t, base, "new", "-d", name); err != nil {
		t.Fatalf("create the detached session: %v\n%s", err, out)
	}
	term := attachIn(t, base, name, startOpts{cols: 120, rows: 40})
	waitForSettledGeometryIn(t, base, name, 1)
	for i := 2; i <= n; i++ {
		if out, err := tuiosCLI(t, base, "run-command", "-s", name, "NewWindow"); err != nil {
			t.Fatalf("open window %d: %v\n%s", i, err, out)
		}
		waitForSettledGeometryIn(t, base, name, i)
	}
	return term
}

// waitForShape polls the session's rectangles until check accepts them, and
// fails with the last answer when it never does.
func waitForShape(t *testing.T, base, session string, n int, what string, check func([]winRect) error) []winRect {
	t.Helper()
	deadline := time.Now().Add(shellTimeout)
	var rects []winRect
	var last error
	for time.Now().Before(deadline) {
		rects = waitForSettledGeometryIn(t, base, session, n)
		if last = check(rects); last == nil {
			return rects
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("%s: %v\n%s", what, last, describeRects(rects))
	return nil
}

func describeRects(rects []winRect) string {
	var b strings.Builder
	for i, r := range rects {
		fmt.Fprintf(&b, "  %d %s (%d,%d) %dx%d\n", i, r.ID, r.X, r.Y, r.Width, r.Height)
	}
	return b.String()
}

// The relations between two rectangles. A shared border can make neighbours
// overlap by one cell, so "left of" allows that much.
func leftOf(a, b winRect) bool  { return a.X+a.Width <= b.X+1 }
func above(a, b winRect) bool   { return a.Y+a.Height <= b.Y+1 }
func sameCol(a, b winRect) bool { return a.X == b.X && a.Width == b.Width }
func sameRow(a, b winRect) bool { return a.Y == b.Y && a.Height == b.Height }

// fullHeight and fullWidth say whether a pane spans the box all the panes
// share, measured from the panes themselves.
func fullHeight(r winRect, rects []winRect) bool {
	top, bottom := r.Y, r.Y+r.Height
	for _, o := range rects {
		top, bottom = min(top, o.Y), max(bottom, o.Y+o.Height)
	}
	return r.Y == top && r.Y+r.Height == bottom
}

func fullWidth(r winRect, rects []winRect) bool {
	left, right := r.X, r.X+r.Width
	for _, o := range rects {
		left, right = min(left, o.X), max(right, o.X+o.Width)
	}
	return r.X == left && r.X+r.Width == right
}

// masterShape checks that rects are master-stack panes with masters masters
// at position, the stack dealt the way the layout deals it.
func masterShape(position string, masters int) func([]winRect) error {
	return func(rects []winRect) error {
		m, stack := rects[:masters], rects[masters:]
		for i := 1; i < len(m); i++ {
			col := sameCol(m[0], m[i])
			if position == "top" || position == "bottom" {
				col = sameRow(m[0], m[i])
			}
			if !col {
				return fmt.Errorf("master %d is not beside master 0 in one %s", i, map[bool]string{true: "row", false: "column"}[position == "top" || position == "bottom"])
			}
		}
		for i, s := range stack {
			for j, mr := range m {
				var ok bool
				switch position {
				case "left":
					ok = leftOf(mr, s)
				case "right":
					ok = leftOf(s, mr)
				case "top":
					ok = above(mr, s)
				case "bottom":
					ok = above(s, mr)
				case "center":
					if i%2 == 0 {
						ok = leftOf(mr, s)
					} else {
						ok = leftOf(s, mr)
					}
				}
				if !ok {
					return fmt.Errorf("master %d is not %s of stack pane %d", j, position, i)
				}
			}
		}
		if masters == 1 {
			switch position {
			case "left", "right", "center":
				if !fullHeight(m[0], rects) {
					return fmt.Errorf("the master does not span the height")
				}
			case "top", "bottom":
				if !fullWidth(m[0], rects) {
					return fmt.Errorf("the master does not span the width")
				}
			}
		}
		if position == "center" {
			// The stack on each side is one column, filled top to bottom.
			for i := 2; i < len(stack); i++ {
				if !sameCol(stack[i], stack[i-2]) || !above(stack[i-2], stack[i]) {
					return fmt.Errorf("stack pane %d is not under stack pane %d", i, i-2)
				}
			}
		}
		return nil
	}
}

// TestMasterPositionFromConfig lays three and five panes out with
// appearance.master_position set to each side. Center with three panes is
// the layout the issue asks for: one column each side of the master.
//
// Five panes on the left is the default grid, which the default keeps so no
// layout changes on an upgrade. It is checked here as the positive half of
// the other sides: they are not a grid at five panes.
func TestMasterPositionFromConfig(t *testing.T) {
	for _, pos := range []string{"left", "right", "top", "bottom", "center"} {
		for _, n := range []int{3, 5} {
			t.Run(fmt.Sprintf("%s/%d", pos, n), func(t *testing.T) {
				base := t.TempDir()
				term := masterSession(t, base, "mp", fmt.Sprintf("master_position = %q\n", pos), n)
				what := fmt.Sprintf("%d panes with the master %s", n, pos)
				var rects []winRect
				if pos == "left" && n == 5 {
					rects = waitForShape(t, base, "mp", n, what, func(rects []winRect) error {
						for _, r := range rects {
							if fullHeight(r, rects) {
								return fmt.Errorf("pane %s spans the height, so this is not the grid", r.ID)
							}
						}
						return nil
					})
				} else {
					rects = waitForShape(t, base, "mp", n, what, masterShape(pos, 1))
				}
				saveArtifact(t, term, artifactDir(t), fmt.Sprintf("master-%s-%d", pos, n))
				t.Logf("%s:\n%s%s", what, describeRects(rects), term.Snapshot())
			})
		}
	}
}

// TestMasterLayoutIsTheSessions changes the master position and count at run
// time with set-layout and attaches a second client. The second client has
// the default config, so a client that laid the workspace out from its own
// config would put the master back on the left and push that geometry over
// the session's. A change made with both clients attached reaches the one
// that did not make it, which a retile on each client shows.
func TestMasterLayoutIsTheSessions(t *testing.T) {
	base := t.TempDir()
	first := masterSession(t, base, "ms", "", 5)
	waitForShape(t, base, "ms", 5, "the default before any change", func(rects []winRect) error {
		for _, r := range rects {
			if fullHeight(r, rects) {
				return fmt.Errorf("pane %s spans the height, so this is not the default grid", r.ID)
			}
		}
		return nil
	})

	if out, err := tuiosCLI(t, base, "set-layout", "-s", "ms", "--master-position", "center"); err != nil {
		t.Fatalf("set-layout --master-position center: %v\n%s", err, out)
	}
	waitForShape(t, base, "ms", 5, "after set-layout --master-position center", masterShape("center", 1))

	second := attachIn(t, base, "ms", startOpts{cols: 120, rows: 40})
	// Long enough for the second client to lay the panes out and push them.
	time.Sleep(2 * time.Second)
	rects := waitForShape(t, base, "ms", 5, "with a second client attached", masterShape("center", 1))
	dir := artifactDir(t)
	saveArtifact(t, first, dir, "first-center")
	saveArtifact(t, second, dir, "second-center")
	t.Logf("center with two clients:\n%s%s", describeRects(rects), second.Snapshot())

	if out, err := tuiosCLI(t, base, "set-layout", "-s", "ms", "--masters", "2"); err != nil {
		t.Fatalf("set-layout --masters 2: %v\n%s", err, out)
	}
	rects = waitForShape(t, base, "ms", 5, "after set-layout --masters 2", masterShape("center", 2))
	time.Sleep(time.Second)
	rects = waitForShape(t, base, "ms", 5, "two masters, a moment later", masterShape("center", 2))
	// Each client lays the panes out itself on a retile, with the shape it
	// holds. The geometry above can be one client's alone, so a retile from
	// each client, by a swap with the master, shows that both hold the
	// session's shape. Only one of them sent the change.
	for i, term := range []*tuitest.Terminal{first, second} {
		sendKeys(t, term, tuitest.Ctrl('b'), "L", tuitest.Enter)
		time.Sleep(time.Second)
		rects = waitForShape(t, base, "ms", 5, fmt.Sprintf("after a swap on client %d", i+1), masterShape("center", 2))
	}
	saveArtifact(t, first, dir, "first-center-2-masters")
	saveArtifact(t, second, dir, "second-center-2-masters")
	t.Logf("two masters in the center:\n%s%s", describeRects(rects), first.Snapshot())

	out, err := tuiosCLI(t, base, "set-layout", "-s", "ms", "--masters", "2", "--json")
	if err != nil {
		t.Fatalf("set-layout --json: %v\n%s", err, out)
	}
	var res struct {
		MasterPosition string `json:"master_position"`
		MasterCount    int    `json:"master_count"`
	}
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("set-layout --json: %v\n%s", err, out)
	}
	if res.MasterPosition != "center" || res.MasterCount != 2 {
		t.Errorf("set-layout reports %+v, not the session's center with two masters: %s", res, out)
	}
}

// TestMasterKeys drives the layout prefix keys: o moves the master to the
// next side, Enter swaps the focused pane with the master, i adds a master.
func TestMasterKeys(t *testing.T) {
	base := t.TempDir()
	term := masterSession(t, base, "mk", "", 3)
	rects := waitForShape(t, base, "mk", 3, "the default", masterShape("left", 1))
	master := rects[0].ID

	// The newest window has the focus. Enter makes it the master.
	sendKeys(t, term, tuitest.Ctrl('b'), "L", tuitest.Enter)
	rects = waitForShape(t, base, "mk", 3, "after Ctrl+B L Enter", func(rects []winRect) error {
		if rects[0].ID == master {
			return fmt.Errorf("the master is still %s", master)
		}
		return masterShape("left", 1)(rects)
	})
	t.Logf("after the swap:\n%s", describeRects(rects))

	sendKeys(t, term, tuitest.Ctrl('b'), "L", "o")
	waitForShape(t, base, "mk", 3, "after Ctrl+B L o", masterShape("right", 1))
	waitScreen(t, term, "the dock message", "Master position: right")

	sendKeys(t, term, tuitest.Ctrl('b'), "L", "i")
	rects = waitForShape(t, base, "mk", 3, "after Ctrl+B L i", masterShape("right", 2))
	saveArtifact(t, term, artifactDir(t), "right-2-masters")
	t.Logf("two masters on the right:\n%s%s", describeRects(rects), term.Snapshot())
}

// TestMasterResizeKeepsItsRatio grows the master with the keyboard while it is
// on the right, then opens a pane. The new pane retiles the workspace, and the
// master keeps the width the resize gave it rather than going back to half.
func TestMasterResizeKeepsItsRatio(t *testing.T) {
	base := t.TempDir()
	term := masterSession(t, base, "mr", "master_position = \"right\"\n", 3)
	before := waitForShape(t, base, "mr", 3, "the master on the right", masterShape("right", 1))

	sendKeys(t, term, tuitest.Ctrl('b'), "L", "m")
	for range 5 {
		sendKeys(t, term, ">")
	}
	grown := waitForShape(t, base, "mr", 3, "after growing the master", func(rects []winRect) error {
		if rects[0].Width <= before[0].Width {
			return fmt.Errorf("the master is %d wide, it was %d", rects[0].Width, before[0].Width)
		}
		return masterShape("right", 1)(rects)
	})

	if out, err := tuiosCLI(t, base, "run-command", "-s", "mr", "NewWindow"); err != nil {
		t.Fatalf("open a fourth window: %v\n%s", err, out)
	}
	after := waitForShape(t, base, "mr", 4, "after a fourth pane", masterShape("right", 1))
	if after[0].Width != grown[0].Width {
		t.Errorf("the retile moved the master from %d to %d wide: the resize was not kept\n%s",
			grown[0].Width, after[0].Width, describeRects(after))
	}
	saveArtifact(t, term, artifactDir(t), "right-resized")
	t.Logf("before %d, grown %d, after a retile %d\n%s", before[0].Width, grown[0].Width, after[0].Width, term.Snapshot())
}

// TestDirectionalFocusMasterPositions walks every direction key from every
// pane, as TestDirectionalFocusMasterStack does for the default, with the
// master on each of the other sides and with two masters in the center.
func TestDirectionalFocusMasterPositions(t *testing.T) {
	for _, c := range []struct {
		appearance string
		n          int
	}{
		{"master_position = \"center\"\n", 5},
		{"master_position = \"right\"\n", 4},
		{"master_position = \"top\"\n", 4},
		{"master_position = \"bottom\"\n", 4},
		{"master_position = \"center\"\nmaster_count = 2\n", 5},
	} {
		t.Run(strings.NewReplacer("\n", " ", "\"", "").Replace(c.appearance), func(t *testing.T) {
			base := t.TempDir()
			term := masterSession(t, base, "mf", c.appearance, c.n)
			walkEveryDirection(t, term, base, "mf", windowModeFocusKeys, c.n)
		})
	}
}

// TestMasterDividerDragInTheCenter drags the master's right border with the
// mouse. The master takes the width the drag gave it, and on release the two
// sides share what is left again, so the master stays in the middle.
func TestMasterDividerDragInTheCenter(t *testing.T) {
	base := t.TempDir()
	term := masterSession(t, base, "md", "master_position = \"center\"\n", 3)
	before := waitForShape(t, base, "md", 3, "the master in the center", masterShape("center", 1))
	master, right := before[0], before[1]

	// The right border of the master, a row into the panes, dragged ten
	// columns into the right-hand pane.
	edge := master.X + master.Width - 1
	row := master.Y + master.Height/2
	mouseDrag(t, term, edge, row, edge+10, row, tuitest.MouseLeft, 0)

	after := waitForShape(t, base, "md", 3, "after dragging the master's border", func(rects []winRect) error {
		m, r, l := rects[0], rects[1], rects[2]
		if m.Width <= master.Width {
			return fmt.Errorf("the master is %d wide, it was %d", m.Width, master.Width)
		}
		if d := r.Width - l.Width; d < 0 || d > 1 {
			return fmt.Errorf("the sides are %d and %d wide: the master is not back in the middle", l.Width, r.Width)
		}
		return masterShape("center", 1)(rects)
	})
	saveArtifact(t, term, artifactDir(t), "center-dragged")
	t.Logf("master %d -> %d wide, right side %d -> %d\n%s%s",
		master.Width, after[0].Width, right.Width, after[1].Width, describeRects(after), term.Snapshot())
}
