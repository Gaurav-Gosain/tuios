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

// framePeek reads back off the rendered rail which session the terminals
// section is showing: the peeked session's name is right-aligned in the
// section's header, and "" is a rested frame showing the attached session.
// Every claim below goes through this rather than through the state, because
// the state was never what the complaint was about.
func framePeek(t *testing.T, m *OS, tree sessiontree.Tree) string {
	t.Helper()
	lines := railPlain(t, m, tree)
	h := lineOf(lines, " terminals")
	if h < 0 {
		t.Fatalf("no terminals header:\n%s", strings.Join(lines, "\n"))
	}
	for _, name := range []string{"api", "docs"} {
		if strings.Contains(lines[h], name) {
			return name
		}
	}
	return ""
}

// TestPeekFollowsTheHoveredRow is the table the pair rule failed. Session rows
// are one cell tall and terminals report motion once per cell entered, so a
// pointer crossing a row vertically lands exactly one event on it however
// slowly it moves: the pair the old rule waited for formed only on sideways
// wobble. The sequences marked below are the ones a user reported as "works for
// some sessions, not others"; the last two are the same gesture reaching the
// same row by two paths, which must agree.
func TestPeekFollowsTheHoveredRow(t *testing.T) {
	m, tree := sectionsTestOS(t, 120, 30)
	m.sidebarPanelLinesForTree(tree)
	main, api, docs := sessionRowY(t, m, "main"), sessionRowY(t, m, "api"), sessionRowY(t, m, "docs")
	pane := -1 // a y standing for "off the band entirely"

	for _, tc := range []struct {
		name string
		ys   []int
		want string
	}{
		{"one event on a row is a peek", []int{api}, "api"},
		{"a second event on the same row holds it", []int{api, api}, "api"},
		{"entering sideways from the panes peeks at once", []int{pane, api}, "api"},

		// Failed before: the first row committed and then kept showing while
		// the pointer moved on, so the header named a session the hover band
		// was no longer on.
		{"stepping to the next row moves the preview with it", []int{pane, api, docs}, "docs"},
		{"a fast sweep ends on the row under the pointer", []int{pane, api, docs, api}, "api"},

		// Failed before: leaving the attached row armed it, so the neighbour
		// needed a wobble to commit and the section stayed on the attached
		// session's panes.
		{"stepping off the attached row peeks the neighbour", []int{main, api}, "api"},
		{"a sweep from the attached row through every session", []int{main, api, docs}, "docs"},

		{"the attached row is never a peek", []int{api, main}, ""},
		{"leaving the sessions section snaps back", []int{api, main}, ""},
		{"leaving the band snaps back", []int{api, pane}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m.sidebarClearPeek()
			for _, y := range tc.ys {
				if y == pane {
					m.SidebarMotion(m.GetRenderWidth()-2, main)
					continue
				}
				m.SidebarMotion(1, y)
			}
			if got := framePeek(t, m, tree); got != tc.want {
				t.Errorf("the terminals section shows %q, want %q", got, tc.want)
			}
		})
	}
}

// TestPeekNeverNamesARowThePointerLeft is the complaint in one assertion: after
// every event of a sweep the section must show the row the pointer is on, never
// an earlier one. A preview that lags the hover band is worse than no preview,
// because both marks are on screen at once saying different things.
func TestPeekNeverNamesARowThePointerLeft(t *testing.T) {
	m, tree := sectionsTestOS(t, 120, 30)
	m.sidebarPanelLinesForTree(tree)

	for _, name := range []string{"main", "api", "docs", "api", "main", "docs"} {
		m.SidebarMotion(1, sessionRowY(t, m, name))
		want := name
		if name == "main" { // attached: the section already shows the truth
			want = ""
		}
		if got := framePeek(t, m, tree); got != want {
			t.Errorf("hovering %q the section shows %q, want %q", name, got, want)
		}
	}
}

// TestPeekEntryPathsAgree: arriving on a row sideways from the pane area and
// arriving along the rail from the row above are the same hover and must draw
// the same frame. Under the pair rule they did not, which is what made the
// preview look like it worked on some rows and not on others.
func TestPeekEntryPathsAgree(t *testing.T) {
	m, tree := sectionsTestOS(t, 120, 30)
	m.sidebarPanelLinesForTree(tree)
	main, api, docs := sessionRowY(t, m, "main"), sessionRowY(t, m, "api"), sessionRowY(t, m, "docs")

	m.sidebarClearPeek()
	m.SidebarMotion(m.GetRenderWidth()-2, main)
	m.SidebarMotion(1, docs)
	sideways := railPlain(t, m, tree)

	m.sidebarClearPeek()
	m.SidebarMotion(1, main)
	m.SidebarMotion(1, api)
	m.SidebarMotion(1, docs)
	alongTheRail := railPlain(t, m, tree)

	if strings.Join(sideways, "\n") != strings.Join(alongTheRail, "\n") {
		t.Errorf("the same hover draws two frames:\nsideways:\n%s\n\nalong the rail:\n%s",
			strings.Join(sideways, "\n"), strings.Join(alongTheRail, "\n"))
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
	if framePeek(t, m, tree) != "api" {
		t.Fatalf("the fixture never peeked: %q", m.SidebarPeek)
	}
	m.SidebarMotion(m.GetRenderWidth()-2, api)
	if got := framePeek(t, m, tree); got != "" {
		t.Errorf("band exit left the section showing %q", got)
	}
}

// TestPeekClearsOnAttach: attaching makes the preview the truth, so there is
// nothing left to preview.
func TestPeekClearsOnAttach(t *testing.T) {
	m, tree := sectionsTestOS(t, 120, 30)
	m.sidebarPanelLinesForTree(tree)

	m.SidebarPeek = "api"
	// Standalone, so the switch itself fails; the clear runs ahead of it and is
	// unconditional, which is the half this test is about.
	m.DaemonClient = nil
	m.sidebarSwitchSession("api")
	if m.SidebarPeek != "" {
		t.Errorf("attaching left the peek at %q", m.SidebarPeek)
	}

	m.SidebarPeek = "api"
	m.SidebarFocused = true
	m.ExitSidebarFocus()
	if m.SidebarPeek != "" {
		t.Errorf("leaving the rail scope left the peek at %q", m.SidebarPeek)
	}
}

// TestPeekKeyboardBrowseParity: the rail cursor previews exactly as the pointer
// does: one keypress is one deliberate move, exactly as one motion event is.
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
// rows, so they must never share a cache entry.
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
	if m.sidebarSignature() != resting {
		t.Error("clearing the peek does not restore the resting signature")
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
	m.SidebarPeek = "api"
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
