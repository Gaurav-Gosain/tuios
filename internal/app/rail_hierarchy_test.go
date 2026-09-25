package app

import (
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/sessiontree"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
)

// The rail used to answer "why is this row lit" with one boolean, and used to
// rank a machine's name above the sessions under it. These pin the two
// distinctions that replaced those: the ground says which kind of attention a
// row has, and a heading is told from an item by a rule rather than by weight.

// TestTheCursorAndTheMouseDrawDifferentGrounds is the whole point of splitting
// the boolean. Sweeping the pointer down the rail used to paint the same
// full-strength band the keyboard cursor paints, so the rail claimed the cursor
// was wherever the mouse happened to be.
func TestTheCursorAndTheMouseDrawDifferentGrounds(t *testing.T) {
	pal := theme.UI()
	cursor := sidebarRowBg(sidebarRowState{Cursor: true, Focused: true}, pal)
	hover := sidebarRowBg(sidebarRowState{Hover: true, Focused: true}, pal)

	if cursor == nil {
		t.Fatal("the keyboard cursor paints no ground")
	}
	if hover == nil {
		t.Fatal("the mouse wash paints no ground")
	}
	if cursor == hover {
		t.Errorf("the cursor and the mouse draw the same ground %v, so the rail cannot say which one you are looking at", cursor)
	}
	// The negative half: a row with neither stays on the rail's own ground, or
	// every row would be lit and none of the above would mean anything.
	if resting := sidebarRowBg(sidebarRowState{Focused: true}, pal); resting != nil {
		t.Errorf("a resting row paints %v, want the rail's own ground", resting)
	}
}

// TestTheCursorWinsWhenARowIsBoth: the two grounds are never composited. A row
// that is both draws the cursor, because a third ground would read as a third
// state and there is no third state.
func TestTheCursorWinsWhenARowIsBoth(t *testing.T) {
	pal := theme.UI()
	both := sidebarRowBg(sidebarRowState{Cursor: true, Hover: true, Focused: true}, pal)
	cursorOnly := sidebarRowBg(sidebarRowState{Cursor: true, Focused: true}, pal)
	if both != cursorOnly {
		t.Errorf("a row that is both the cursor and hovered drew %v, want the cursor's own %v", both, cursorOnly)
	}
}

// TestAnUnfocusedRailSoftensItsCursorBand: a rail that does not own the
// keyboard still says where the cursor will land, but stops asserting that the
// next key goes there.
func TestAnUnfocusedRailSoftensItsCursorBand(t *testing.T) {
	pal := theme.UI()
	focused := sidebarRowBg(sidebarRowState{Cursor: true, Focused: true}, pal)
	blurred := sidebarRowBg(sidebarRowState{Cursor: true}, pal)

	if blurred == nil {
		t.Fatal("an unfocused rail drew no cursor at all, so the cursor is lost on the way back")
	}
	if focused == blurred {
		t.Error("a focused and an unfocused rail draw the same cursor band, so the rail never says whether it has the keyboard")
	}
}

// TestAMachineHeadingCarriesARule pins what replaced the weight. The heading is
// told from the rows under it by a rule running out to the right spine, which
// is a different kind of mark rather than a louder one, so it survives having
// colour switched off.
func TestAMachineHeadingCarriesARule(t *testing.T) {
	m := hostRailOS(t)
	rule := m.Settings.GetRailRuleGlyph()
	head := hostHeadingNode(t, m)

	var lines []string
	m.drawHostRow(head, 40, sidebarVariantFull, theme.UI(), sidebarRowState{}, true, true,
		func(sidebarRowKind, string, string) bool { return false },
		func(sidebarRowKind, string, string, int, int) {},
		func(sidebarTokenSpan, string) {},
		-1, func(s string) string { return s }, &lines)
	if len(lines) != 1 {
		t.Fatalf("drawHostRow drew %d lines, want 1", len(lines))
	}
	if !strings.Contains(stripANSIForTrace(lines[0]), rule) {
		t.Errorf("a machine heading carries no rule, so nothing tells it from a session row:\n%q", stripANSIForTrace(lines[0]))
	}

	// The negative half: an ordinary session row must not carry one, or the
	// rule says nothing.
	plain := &OS{Settings: config.Global}
	row := plain.sidebarSessionRow(
		sessiontree.BuildSession(sessiontree.SessionInput{Name: "work", WindowCount: 2}),
		sidebarVariantFull, 40, overlay.Palette{}, sidebarRowState{}, false, true)
	if strings.Contains(stripANSIForTrace(row), rule) {
		t.Errorf("a session row carries a heading's rule: %q", stripANSIForTrace(row))
	}
}

// TestACountColumnOfOnesIsNotDrawn: a one-window session is the common case, so
// a column printing "1" against every row is a column of identical digits
// carrying nothing. The section decides once, so the column is either there for
// every row or gone.
func TestACountColumnOfOnesIsNotDrawn(t *testing.T) {
	m := &OS{Settings: config.Global}
	m.Settings.SidebarShowCounts = true
	node := sessiontree.BuildSession(sessiontree.SessionInput{Name: "work", WindowCount: 1})

	off := stripANSIForTrace(m.sidebarSessionRow(node, sidebarVariantFull, 28, overlay.Palette{}, sidebarRowState{}, false, false))
	if strings.ContainsAny(off, "0123456789") {
		t.Errorf("the rail drew a count in a section where no session has more than one window: %q", off)
	}
	// The positive half: the same row prints its figure once some row in the
	// section has more than one, so the assertion above tests the decision and
	// not the row.
	on := stripANSIForTrace(m.sidebarSessionRow(node, sidebarVariantFull, 28, overlay.Palette{}, sidebarRowState{}, false, true))
	if !strings.Contains(on, "1") {
		t.Errorf("the rail dropped a count in a section that has one to show: %q", on)
	}
}

// sgrHasBold reports whether any SGR sequence in s turns bold on.
//
// It parses the parameter list rather than searching for "\x1b[1m", because
// lipgloss folds attributes and colour into one sequence: bold arrives as the
// "1" in "\x1b[1;38;2;r;g;bm", and a search for the standalone form silently
// matches nothing and passes. It skips the arguments of 38, 48 and 58 so that
// "\x1b[38;5;1m", which is colour index 1, is not read as bold.
func sgrHasBold(s string) bool {
	for i := 0; i+1 < len(s); i++ {
		if s[i] != 0x1b || s[i+1] != '[' {
			continue
		}
		j := i + 2
		for j < len(s) && (s[j] == ';' || (s[j] >= '0' && s[j] <= '9')) {
			j++
		}
		if j >= len(s) || s[j] != 'm' {
			continue
		}
		parts := strings.Split(s[i+2:j], ";")
		for k := 0; k < len(parts); k++ {
			switch parts[k] {
			case "38", "48", "58":
				if k+1 < len(parts) && parts[k+1] == "5" {
					k += 2
				} else if k+1 < len(parts) && parts[k+1] == "2" {
					k += 4
				}
			case "1":
				return true
			}
		}
		i = j
	}
	return false
}

// hostHeadingNode is the machine heading in the host rail fixture.
func hostHeadingNode(t *testing.T, m *OS) sessiontree.Node {
	t.Helper()
	for _, n := range m.hostGroupNodes() {
		if n.Kind == sessiontree.KindHost {
			return n
		}
	}
	t.Fatalf("the fixture has no machine heading: %+v", m.hostGroupNodes())
	return sessiontree.Node{}
}
