package app

import (
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/sessiontree"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
	"github.com/charmbracelet/x/ansi"
)

// These tests pin the rich agent row: what the agent is doing now while it
// works, the turn's first line when it finished and nobody looked, nothing at
// rest, the context warning at 80 percent, and the queued figure at the right
// edge.

// richRow draws one agent pane on the shipped rail and returns its two lines
// without styling, and the styled row.
func richRow(t *testing.T, w sessiontree.WindowInput) (string, string) {
	t.Helper()
	m := needOS(t)
	if w.ID == "" {
		w.ID = "dddddddd4444"
	}
	if w.Title == "" {
		w.Title = "api"
	}
	lines, _ := m.sidebarPanelLinesForTree(needTree(w))
	styled := railAgentRow(m, lines, w.ID)
	return stripANSIForTrace(styled), styled
}

// TestRowShowsNowOnlyWhileWorking: a working row's second line is the tool it
// runs, not the message and not the last prompt; a pane that stopped keeps no
// "now" even if the value is still there.
func TestRowShowsNowOnlyWhileWorking(t *testing.T) {
	meta := []sessiontree.MetaToken{{Key: "prompt", Value: "make the backoff configurable"}, {Key: "now", Value: "Bash: go test ./..."}}
	row, _ := richRow(t, sessiontree.WindowInput{AgentState: "working", Harness: "claude-code", Message: "editing files", Meta: meta})
	if !strings.Contains(row, "Bash: go test") {
		t.Fatalf("working row = %q, want what it is doing now", row)
	}
	if strings.Contains(row, "editing files") || strings.Contains(row, "backoff") {
		t.Errorf("working row = %q, want neither the message nor the prompt beside now", row)
	}

	// With nothing running now, the message is the line again.
	row, _ = richRow(t, sessiontree.WindowInput{AgentState: "working", Harness: "claude-code", Message: "editing files"})
	if !strings.Contains(row, "editing files") {
		t.Errorf("working row without now = %q, want its message", row)
	}

	// A needs-you row says what it asks, not the tool.
	row, _ = richRow(t, sessiontree.WindowInput{AgentState: "needs_input", AgentKind: "approval", Harness: "claude-code",
		Message: "approve Bash: rm -rf build", Meta: meta, StateAt: time.Now().Add(-7 * time.Minute).UnixNano()})
	if !strings.Contains(row, "approval") || strings.Contains(row, "go test") {
		t.Errorf("needs-you row = %q, want the need and no now", row)
	}
}

// TestRowAtRestSaysNothing: a finished turn nobody looked at shows its first
// line; once seen, or idle, the row has no second line to fill.
func TestRowAtRestSaysNothing(t *testing.T) {
	row, _ := richRow(t, sessiontree.WindowInput{AgentState: "done", Harness: "claude-code", Message: "Added retry with backoff"})
	if !strings.Contains(row, "Added retry") {
		t.Fatalf("finished unread row = %q, want the turn's first line", row)
	}

	row, _ = richRow(t, sessiontree.WindowInput{AgentState: "done", DoneSeen: true, Harness: "claude-code", Message: "Added retry with backoff"})
	if strings.Contains(row, "Added retry") {
		t.Errorf("seen finished row = %q, want no message at rest", row)
	}
	row, _ = richRow(t, sessiontree.WindowInput{AgentState: "idle", Harness: "claude-code", Message: "waiting for input"})
	if strings.Contains(row, "waiting for input") {
		t.Errorf("idle row = %q, want no message at rest", row)
	}
}

// TestRowContextWarnsAtEighty: context draws only at 80 percent and over, as
// "ctx N%", in the warning ink.
func TestRowContextWarnsAtEighty(t *testing.T) {
	for _, c := range []struct {
		value string
		want  string
	}{
		{"42%", ""},
		{"79.9%", ""},
		{"80%", "ctx 80%"},
		{"84.6%", "ctx 84%"},
		{"not a percent", ""},
	} {
		row, styled := richRow(t, sessiontree.WindowInput{AgentState: "working", Harness: "claude-code",
			Meta: []sessiontree.MetaToken{{Key: "context", Value: c.value}, {Key: "now", Value: "Edit: a.go"}}})
		if c.want == "" {
			if strings.Contains(row, "ctx") || strings.Contains(row, c.value) {
				t.Errorf("context %s: row = %q, want no context", c.value, row)
			}
			continue
		}
		if !strings.Contains(row, c.want) {
			t.Errorf("context %s: row = %q, want %q", c.value, row, c.want)
		}
		if !strings.Contains(styled, fgParams(theme.UI().Warning)) {
			t.Errorf("context %s: the warning is not in the warning ink: %q", c.value, styled)
		}
		// The context comes before now, so a long command loses its tail
		// first.
		if strings.Index(row, "ctx") > strings.Index(row, "Ed") {
			t.Errorf("context %s: row = %q, want the context ahead of now", c.value, row)
		}
	}
}

// TestRowQueuedFigure: "N queued" takes the right edge of the first line, and
// the name gives way before it on the 24-column rail. A row that needs you
// keeps its wait there.
func TestRowQueuedFigure(t *testing.T) {
	row, _ := richRow(t, sessiontree.WindowInput{Title: "a-rather-long-agent-name", AgentState: "working", Harness: "claude-code", Queued: 2})
	first := strings.SplitN(row, "\n", 2)[0]
	if !strings.HasSuffix(strings.TrimRight(first, " │"), "2 queued") {
		t.Fatalf("first line = %q, want 2 queued at its right edge", first)
	}
	plain, _ := richRow(t, sessiontree.WindowInput{Title: "a-rather-long-agent-name", AgentState: "working", Harness: "claude-code"})
	if w, want := ansi.StringWidth(first), ansi.StringWidth(strings.SplitN(plain, "\n", 2)[0]); w != want {
		t.Errorf("first line is %d cells, the row without a queue %d", w, want)
	}
	row, _ = richRow(t, sessiontree.WindowInput{AgentState: "needs_input", AgentKind: "approval", Harness: "claude-code",
		Message: "approve Bash: make", Queued: 1, StateAt: time.Now().Add(-12 * time.Minute).UnixNano()})
	if strings.Contains(row, "queued") || !strings.Contains(row, "12m") {
		t.Errorf("needs-you row = %q, want its wait rather than the queue", row)
	}
	row, _ = richRow(t, sessiontree.WindowInput{AgentState: "working", Harness: "claude-code"})
	if strings.Contains(row, "queued") {
		t.Errorf("row with nothing queued = %q", row)
	}
}

// TestContextPercentReads: the forms a context value comes in.
func TestContextPercentReads(t *testing.T) {
	for v, want := range map[string]float64{"42%": 42, " 81.5% ": 81.5, "90": 90, "84% ctx": 84} {
		if got, ok := sidebarContextPercent(v); !ok || got != want {
			t.Errorf("sidebarContextPercent(%q) = %v, %v; want %v", v, got, ok, want)
		}
	}
	for _, v := range []string{"", "12000 tokens", "140%", "-3%"} {
		if _, ok := sidebarContextPercent(v); ok {
			t.Errorf("sidebarContextPercent(%q) read as a percent", v)
		}
	}
}
