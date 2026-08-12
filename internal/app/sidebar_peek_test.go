package app

import (
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/sessiontree"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
)

// sessionRowY is the screen row a session's rail row was drawn on.
func sessionRowY(t *testing.T, m *OS, id string) int {
	t.Helper()
	for _, h := range m.SidebarHits {
		if h.Kind == sidebarRowSession && h.SessionID == id {
			return h.Y0
		}
	}
	t.Fatalf("no session row for %q", id)
	return 0
}

// TestPeekPairRule pins the debounce the design gets for free from the motion
// events already arriving, instead of from a clock. The table is a sequence of
// pointer positions, because a single position never decides anything on its
// own: what commits a peek is what the previous event resolved to.
func TestPeekPairRule(t *testing.T) {
	m, tree := sectionsTestOS(t, 120, 30)
	m.sidebarPanelLinesForTree(tree)
	main, api, docs := sessionRowY(t, m, "main"), sessionRowY(t, m, "api"), sessionRowY(t, m, "docs")
	pane := m.GetRenderWidth() - 2 // outside the band

	for _, tc := range []struct {
		name string
		ys   []int
		want string
	}{
		{"a pair on one row commits", []int{api, api}, "api"},
		{"entering sideways from the panes commits at once", []int{pane, api}, "api"},
		{"a one-event sweep across three rows commits nothing", []int{main, api, docs}, ""},
		{"a slow crossing commits row by row", []int{main, api, api, docs, docs}, "docs"},
		{"the attached row is never a peek", []int{main, main}, ""},
		{"leaving the sessions section snaps back", []int{api, api, main}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m.sidebarClearPeek()
			for _, y := range tc.ys {
				x := 1
				if y == pane {
					x, y = pane, main
				}
				m.SidebarMotion(x, y)
			}
			if m.SidebarPeek != tc.want {
				t.Errorf("peek = %q, want %q", m.SidebarPeek, tc.want)
			}
		})
	}
}

// TestPeekSnapsBackOnBandExit: leaving the band entirely is covered by the one
// out-of-band motion event the whitelist keeps flowing, the same event that
// clears the stale hover highlight. Without this the preview would survive the
// pointer that made it.
func TestPeekSnapsBackOnBandExit(t *testing.T) {
	m, tree := sectionsTestOS(t, 120, 30)
	m.sidebarPanelLinesForTree(tree)
	api := sessionRowY(t, m, "api")

	m.SidebarMotion(1, api)
	m.SidebarMotion(1, api)
	if m.SidebarPeek != "api" {
		t.Fatalf("the fixture never peeked: %q", m.SidebarPeek)
	}
	m.SidebarMotion(m.GetRenderWidth()-2, api)
	if m.SidebarPeek != "" || m.SidebarPeekArm != "" {
		t.Errorf("band exit left peek=%q arm=%q", m.SidebarPeek, m.SidebarPeekArm)
	}
}

// TestPeekClearsOnAttach: attaching makes the preview the truth, so there is
// nothing left to preview.
func TestPeekClearsOnAttach(t *testing.T) {
	m, tree := sectionsTestOS(t, 120, 30)
	m.sidebarPanelLinesForTree(tree)

	m.SidebarPeek, m.SidebarPeekArm = "api", "api"
	// Standalone, so the switch itself fails; the clear runs ahead of it and is
	// unconditional, which is the half this test is about.
	m.DaemonClient = nil
	m.sidebarSwitchSession("api")
	if m.SidebarPeek != "" {
		t.Errorf("attaching left the peek at %q", m.SidebarPeek)
	}

	m.SidebarPeek, m.SidebarPeekArm = "api", "api"
	m.SidebarFocused = true
	m.ExitSidebarFocus()
	if m.SidebarPeek != "" {
		t.Errorf("leaving the rail scope left the peek at %q", m.SidebarPeek)
	}
}

// TestPeekKeyboardBrowseParity: the rail cursor previews exactly as the pointer
// does, with no pair rule, because a keypress is already one deliberate move.
func TestPeekKeyboardBrowseParity(t *testing.T) {
	m, tree := sectionsTestOS(t, 120, 30)
	m.SidebarFocused = true
	m.sidebarPanelLinesForTree(tree)

	m.sidebarSetCursor(m.sidebarCurrentSessionNavIndex())
	if m.SidebarPeek != "" {
		t.Fatalf("the cursor on the attached session peeked at %q", m.SidebarPeek)
	}
	m.SidebarCursorMove(1)
	if m.SidebarPeek != "api" {
		t.Errorf("j onto a foreign session peeked at %q, want api", m.SidebarPeek)
	}
	m.SidebarCursorMove(1)
	if m.SidebarPeek != "docs" {
		t.Errorf("j again peeked at %q, want docs", m.SidebarPeek)
	}
	m.SidebarCursorMove(-2)
	if m.SidebarPeek != "" {
		t.Errorf("back on the attached session the peek survived as %q", m.SidebarPeek)
	}
}

// TestPeekShowsTheOtherSessionsPanes checks all three marks the design gives a
// preview: the peeked session's name in the terminals header, its panes in
// place of the attached session's, and every row dim with no focus gutter.
func TestPeekShowsTheOtherSessionsPanes(t *testing.T) {
	m, tree := sectionsTestOS(t, 120, 30)
	m.SidebarPeek = "api"
	lines := railPlain(t, m, tree)

	header := lineOf(lines, " terminals")
	if header < 0 {
		t.Fatalf("no terminals header:\n%s", strings.Join(lines, "\n"))
	}
	if !strings.Contains(lines[header], "api") {
		t.Errorf("the terminals header does not say whose panes these are: %q", lines[header])
	}
	if lineOf(lines, "server") < 0 || lineOf(lines, "worker") < 0 {
		t.Errorf("the peeked session's panes are missing:\n%s", strings.Join(lines, "\n"))
	}
	for _, h := range m.SidebarHits {
		if h.Kind == sidebarRowWindow && h.WindowID == "aaaaaaaa1111" {
			t.Error("the attached session's panes are still listed under a peek")
		}
	}

	// Nothing in a preview is the user's to act on, so no row wears the focus
	// mark. Severity gutters survive: they are why the peek happened.
	styled, _ := m.sidebarPanelLinesForTree(tree)
	pal := theme.UI()
	for _, h := range m.SidebarHits {
		if h.Kind != sidebarRowWindow {
			continue
		}
		row := styled[h.Y0-m.GetTopMargin()]
		if strings.Contains(row, fgSeq(pal.Accent)) {
			t.Errorf("a peeked terminal row wears the focus accent: %q", stripANSIForTrace(row))
		}
	}
	if row := lineOf(lines, "server"); !strings.Contains(styled[row], fgSeq(pal.Warning)) {
		t.Errorf("a peeked row lost its severity mark: %q", lines[row])
	}
}

// TestEmptyPeekSaysSo: without the hint row the section would read as "the
// attached session has no panes", which is a lie about the wrong session.
func TestEmptyPeekSaysSo(t *testing.T) {
	m, tree := sectionsTestOS(t, 120, 30)
	m.SidebarPeek = "docs" // the fixture's session with no panes on the wire
	lines := railPlain(t, m, tree)

	header := lineOf(lines, " terminals")
	if header < 0 || !strings.Contains(lines[header], "docs") {
		t.Fatalf("the header lost its name mark on an empty peek: %q", lines[max(header, 0)])
	}
	if lineOf(lines, "no terminals") < 0 {
		t.Errorf("an empty peek drew no hint row:\n%s", strings.Join(lines, "\n"))
	}
	for _, h := range m.SidebarHits {
		if h.Kind == sidebarRowWindow {
			t.Errorf("the empty-peek hint is interactive: %+v", h)
		}
	}
}

// TestHoveringTheAttachedSessionIsNotAPeek: the section already shows the
// truth, so no marks appear and no rebuild is provoked.
func TestHoveringTheAttachedSessionIsNotAPeek(t *testing.T) {
	m, tree := sectionsTestOS(t, 120, 30)
	rested := railPlain(t, m, tree)
	main := sessionRowY(t, m, "main")

	m.SidebarMotion(1, main)
	m.SidebarMotion(1, main)
	if m.SidebarPeek != "" {
		t.Fatalf("hovering the attached session peeked at %q", m.SidebarPeek)
	}
	hovered := railPlain(t, m, tree)
	if lineOf(hovered, "no terminals") >= 0 {
		t.Error("hovering the attached session produced an empty-peek hint")
	}
	if got, want := lineOf(hovered, " terminals"), lineOf(rested, " terminals"); got != want {
		t.Errorf("the terminals header moved from line %d to %d on a non-peek", want, got)
	}
}

// TestPeekIsInTheSignature: a peeked frame and a resting one draw different
// rows, so they must never share a cache entry. The pair rule's arm draws
// nothing, so folding it in would rebuild the rail for no visible reason.
func TestPeekIsInTheSignature(t *testing.T) {
	m, tree := sectionsTestOS(t, 120, 30)
	m.sidebarPanelLinesForTree(tree)
	resting := m.sidebarSignature()

	m.SidebarPeek = "api"
	peeked := m.sidebarSignature()
	if peeked == resting {
		t.Error("a peek does not move the rail signature")
	}

	m.SidebarPeek = ""
	m.SidebarPeekArm = "api"
	if m.sidebarSignature() != resting {
		t.Error("the pair rule's arm moves the signature, so an armed pointer rebuilds the rail for nothing")
	}
}

// TestPeekNeedsNoTick: the whole preview rides arriving motion events, so a
// live peek must leave the idle gate exactly where it found it.
func TestPeekNeedsNoTick(t *testing.T) {
	m, tree := sectionsTestOS(t, 120, 30)
	m.Windows = nil
	m.sidebarPanelLinesForTree(tree)
	if m.tickNeedsWork() {
		t.Skip("the fixture is not idle to begin with")
	}
	m.SidebarPeek, m.SidebarPeekArm = "api", "api"
	if m.tickNeedsWork() {
		t.Error("a live peek woke the maintenance tick")
	}
}

// TestPeekIgnoresASessionThatIsGone: peek is runtime state and the session list
// is not, so the render must not trust a stale name.
func TestPeekIgnoresASessionThatIsGone(t *testing.T) {
	m, tree := sectionsTestOS(t, 120, 30)
	m.SidebarPeek = "vanished"
	lines := railPlain(t, m, tree)

	if lineOf(lines, "no terminals") >= 0 {
		t.Errorf("a stale peek emptied the terminals section:\n%s", strings.Join(lines, "\n"))
	}
	if lineOf(lines, "nvim") < 0 {
		t.Errorf("a stale peek hid the attached session's panes:\n%s", strings.Join(lines, "\n"))
	}
	if shown, peeking := m.sidebarShownSession(tree.Sessions); peeking || shown != "main" {
		t.Errorf("shown = %q peeking = %v, want main and false", shown, peeking)
	}
}

// TestPeekedPanesCarryTheirWorkspaceTags is what the protocol enrichment buys:
// before it, a peeked session's panes were tagless because this client had no
// way to know where they lived. The names of another session's workspaces are
// still not on the wire, so its tags take the numbered form.
func TestPeekedPanesCarryTheirWorkspaceTags(t *testing.T) {
	m, tree := sectionsTestOS(t, 120, 30)
	m.SidebarPeek = "api"
	lines := railPlain(t, m, tree)

	worker := lineOf(lines, "worker")
	if worker < 0 {
		t.Fatalf("the peek did not show the other session's panes:\n%s", strings.Join(lines, "\n"))
	}
	if !strings.Contains(lines[worker], "w3") {
		t.Errorf("a peeked pane on workspace 3 says nothing about it: %q", lines[worker])
	}
	// A pane on the peeked session's own current workspace stays quiet, because
	// "here" is not information whichever session is being looked at.
	server := lineOf(lines, "server")
	if server < 0 {
		t.Fatalf("the peek lost a pane:\n%s", strings.Join(lines, "\n"))
	}
	if strings.Contains(lines[server], "w1") {
		t.Errorf("a peeked pane on the session's own workspace was tagged anyway: %q", lines[server])
	}
}

var _ = sessiontree.Tree{}
