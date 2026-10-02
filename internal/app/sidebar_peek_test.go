package app

import (
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/sessiontree"
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

// TestPeekCostsNoExtraRebuild is why committing on the first event is
// affordable: the pointer's own cell is already in the rail signature, so a
// motion event that crosses a session row rebuilds the rail whether or not the
// preview moves with it. Previewing per event buys correctness for nothing, and
// the debounce the pair rule existed to provide was never paying for a rebuild.
func TestPeekCostsNoExtraRebuild(t *testing.T) {
	m, tree := sectionsTestOS(t, 120, 30)
	m.sidebarPanelLinesForTree(tree)
	api, docs := sessionRowY(t, m, "api"), sessionRowY(t, m, "docs")

	m.SidebarMotion(1, api)
	onAPI := m.sidebarSignature()
	m.SidebarMotion(1, docs)
	if m.sidebarSignature() == onAPI {
		t.Fatal("crossing a session row leaves the rail signature alone, so the cost claim needs rechecking")
	}

	// The same crossing with the preview held still: the signature moves anyway.
	m.sidebarClearPeek()
	m.SidebarHoverX, m.SidebarHoverY = 1, api
	held := m.sidebarSignature()
	m.SidebarHoverY = docs
	if m.sidebarSignature() == held {
		t.Error("the hovered cell is not in the rail signature; a hover move would not repaint the band")
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

var _ = sessiontree.Tree{}

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
