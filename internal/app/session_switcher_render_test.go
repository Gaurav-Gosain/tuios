package app

import (
	"strconv"
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/Gaurav-Gosain/tuios/internal/sessiontree"
)

// switcherOS builds a session switcher over a fixed item list, with a zero-value
// daemon client because the switcher's daemon-mode guard only checks for one.
func switcherOS(items []sessiontree.Node) *OS {
	m := &OS{Settings: config.Global, IsDaemonSession: true, DaemonClient: &session.TUIClient{}}
	m.ShowSessionSwitcher = true
	m.SessionSwitcherItems = items
	return m
}

func switcherItems() []sessiontree.Node {
	return []sessiontree.Node{
		sessiontree.BuildSession(sessiontree.SessionInput{
			Name: "work", DisplayName: "Payments API", IsCurrent: true,
			Windows: []sessiontree.WindowInput{
				{ID: "w1", Title: "vim"},
				{ID: "w2", Title: "claude", AgentState: "needs_input"},
				{ID: "w3", Title: "logs", AgentState: "working"},
			},
		}),
		sessiontree.BuildSession(sessiontree.SessionInput{
			Name:    "notes",
			Windows: []sessiontree.WindowInput{{ID: "n1", Title: "shell"}},
		}),
		sessiontree.BuildSession(sessiontree.SessionInput{
			Name: "infra", Windows: []sessiontree.WindowInput{
				{ID: "i1", Title: "tf", AgentState: "errored"},
				{ID: "i2", Title: "sh"},
			},
		}),
	}
}

// TestSessionSwitcherRowShowsLabelIdentityCountAndState reads the four things a
// row promises straight off the rendered frame.
func TestSessionSwitcherRowShowsLabelIdentityCountAndState(t *testing.T) {
	m := switcherOS(switcherItems())
	out, _, _ := m.renderSessionSwitcher()
	t.Logf("\n%s", out)

	for _, want := range []string{"Payments API", "(work)", "3 panes", "1 pane", "current", "notes", "infra"} {
		if !strings.Contains(out, want) {
			t.Errorf("frame is missing %q", want)
		}
	}
	// The worst state in the session, not the first one found: needs_input
	// outranks working, and errored outranks everything.
	if glyph := agentStateIndicator("needs_input"); !strings.Contains(out, glyph) {
		t.Errorf("frame is missing the needs_input glyph %q", glyph)
	}
	if glyph := agentStateIndicator("errored"); !strings.Contains(out, glyph) {
		t.Errorf("frame is missing the errored glyph %q", glyph)
	}
}

// TestUnnamedSessionRowShowsNoIdentitySuffix pins the no-op case: a session
// nobody has renamed must not grow a parenthesised identity, because its label
// and its identity are the same string.
func TestUnnamedSessionRowShowsNoIdentitySuffix(t *testing.T) {
	m := switcherOS([]sessiontree.Node{
		sessiontree.BuildSession(sessiontree.SessionInput{Name: "notes", WindowCount: 1}),
	})
	out, _, _ := m.renderSessionSwitcher()

	if strings.Contains(out, "(notes)") {
		t.Errorf("an unrenamed session showed its identity twice:\n%s", out)
	}
	if !strings.Contains(out, "notes") {
		t.Errorf("frame is missing the session name:\n%s", out)
	}
}

// TestSessionSwitcherHitRectsMatchDrawnRows checks the recorded rects against
// the frame they were recorded from, at three widths. A rect that names row i
// must land on the line the label for item i was actually drawn on.
func TestSessionSwitcherHitRectsMatchDrawnRows(t *testing.T) {
	for _, w := range []int{62, 100, 180} {
		t.Run(strconv.Itoa(w), func(t *testing.T) {
			m := switcherOS(switcherItems())
			m.Width, m.Height = w, 30

			out, _, rows := m.renderSessionSwitcher()
			if len(rows) != 3 {
				t.Fatalf("rows = %d, want 3", len(rows))
			}
			lines := strings.Split(out, "\n")

			want := []string{"Payments API", "notes", "infra"}
			for _, row := range rows {
				if row.Rect.Y0 < 0 || row.Rect.Y0 >= len(lines) {
					t.Fatalf("row %d rect Y0=%d is outside the %d-line frame", row.Idx, row.Rect.Y0, len(lines))
				}
				line := lines[row.Rect.Y0]
				if !strings.Contains(line, want[row.Idx]) {
					t.Errorf("width %d: rect for row %d points at line %d %q, want the row holding %q",
						w, row.Idx, row.Rect.Y0, line, want[row.Idx])
				}
			}
		})
	}
}

// TestSessionSwitcherFilterNarrowsAndTargetsTheFilteredRow is the classic
// overlay off-by-one: with a query typed, selection 0 must mean the first row
// on screen, not the first item in the unfiltered list.
func TestSessionSwitcherFilterNarrowsAndTargetsTheFilteredRow(t *testing.T) {
	m := switcherOS(switcherItems())

	if got := len(FilterSessionItems(m.SessionSwitcherItems, "")); got != 3 {
		t.Fatalf("unfiltered count = %d, want 3", got)
	}

	m.SessionSwitcherQuery = "inf"
	m.SessionSwitcherSelected = 0

	filtered := FilterSessionItems(m.SessionSwitcherItems, m.SessionSwitcherQuery)
	if len(filtered) != 1 {
		t.Fatalf("filtered count = %d, want 1", len(filtered))
	}
	target, ok := m.SessionSwitcherTarget(0)
	if !ok {
		t.Fatal("selection 0 resolved to nothing while a row was on screen")
	}
	if target.ID != "infra" {
		t.Errorf("target = %q, want infra: the selection was resolved against the unfiltered list", target.ID)
	}

	// A rename must not cost the user the name they know the session by.
	m.SessionSwitcherQuery = "work"
	byIdentity, ok := m.SessionSwitcherTarget(0)
	if !ok || byIdentity.ID != "work" {
		t.Errorf("searching the identity of a renamed session found %+v, want work", byIdentity)
	}

	m.SessionSwitcherQuery = "payments"
	byLabel, ok := m.SessionSwitcherTarget(0)
	if !ok || byLabel.ID != "work" {
		t.Errorf("searching the display name found %+v, want the work session", byLabel)
	}

	m.SessionSwitcherQuery = "nothing-matches-this"
	if _, ok := m.SessionSwitcherTarget(0); ok {
		t.Error("an empty filtered list still resolved a target")
	}
}
