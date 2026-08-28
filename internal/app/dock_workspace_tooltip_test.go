package app

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/session"
)

// pillTooltipName is longer than a pill can carry, so the strip has to cut it
// and the hover is the only way to the rest of it.
const pillTooltipName = "release/hotfix-payments"

// pillTooltipOS is a dock with three occupied workspaces, the middle one named
// past what its pill can print.
func pillTooltipOS(t *testing.T) *OS {
	t.Helper()
	m := newNarrowOS(t, 140, 30)
	m.NumWorkspaces = 9
	m.CurrentWorkspace = 1
	for _, ws := range []int{1, 2, 3} {
		win := newTestWindow(t, fmt.Sprintf("pill-ws%d", ws), 40, 10)
		win.Workspace = ws
		m.Windows = append(m.Windows, win)
	}
	m.adoptSessionLabels(&session.SessionState{
		WorkspaceNames: map[int]string{2: pillTooltipName, 3: "docs"},
	})

	prevTabs, prevTip := m.Settings.DockWorkspaceTabs, m.Settings.DockWorkspaceTooltip
	m.Settings.DockWorkspaceTabs, m.Settings.DockWorkspaceTooltip = true, true
	t.Cleanup(func() { m.Settings.DockWorkspaceTabs, m.Settings.DockWorkspaceTooltip = prevTabs, prevTip })
	return m
}

// pillFrame is the whole screen as the app would draw it, colours dropped. The
// label has to be found in the frame rather than in the layer it came from: a
// layer that composes to nothing readable is the failure worth catching.
func pillFrame(m *OS) []string {
	return strings.Split(stripANSIForTrace(lipgloss.Sprint(m.GetCanvas(true).Render())), "\n")
}

// pillRect is where workspace ws was drawn on the last frame.
func pillRect(t *testing.T, m *OS, ws int) dockWorkspaceHit {
	t.Helper()
	for _, h := range m.dockWorkspaceHits {
		if h.Workspace == ws {
			return h
		}
	}
	t.Fatalf("the dock drew no pill for workspace %d", ws)
	return dockWorkspaceHit{}
}

// pillHits copies the recorded rects, which the renderer reuses in place.
func pillHits(m *OS) []dockWorkspaceHit {
	return append([]dockWorkspaceHit(nil), m.dockWorkspaceHits...)
}

// hoverPill draws a frame, rests the pointer on workspace ws's pill, and draws
// the frame that follows. The delay is faked rather than waited out: the clock
// is arriving motion, and what is under test is the frame past it.
func hoverPill(t *testing.T, m *OS, ws int) (before, after []string, rect dockWorkspaceHit) {
	t.Helper()
	before = pillFrame(m)
	rect = pillRect(t, m, ws)
	if !m.DockWorkspaceHoverAt(rect.X0, rect.Y) {
		t.Fatalf("the pointer on column %d was not on workspace %d's pill", rect.X0, ws)
	}
	m.Tooltip.At = time.Now().Add(-2 * tooltipDelay)
	return before, pillFrame(m), rect
}

// TestWorkspacePillTooltipRevealsTheWholeName is the whole point of the change.
// The pill cuts a long name at twelve cells, which is enough to steer by and not
// enough to tell two release branches apart; resting on it finishes the word.
func TestWorkspacePillTooltipRevealsTheWholeName(t *testing.T) {
	m := pillTooltipOS(t)
	before, after, rect := hoverPill(t, m, 2)

	if strings.Contains(strings.Join(before, "\n"), pillTooltipName) {
		t.Fatalf("the name was already on screen before the hover; nothing was truncated:\n%s", strings.Join(before, "\n"))
	}
	if got := strings.Join(after, "\n"); !strings.Contains(got, pillTooltipName) {
		t.Errorf("hovering the pill never said %q:\n%s", pillTooltipName, got)
	}
	// On the row beside the bar, not over it: a label drawn on the pill would
	// cover the thing the pointer is asking about.
	row := rect.Y - 1
	if config.Global.DockbarPosition == "top" {
		row = rect.Y + 1
	}
	if row < 0 || row >= len(after) || !strings.Contains(after[row], pillTooltipName) {
		t.Errorf("row %d does not carry the label: %q", row, safeRow(after, row))
	}
}

// TestWorkspacePillTooltipLeavesTheStripWhereItWas: the pills' rectangles are
// recorded as they are drawn, so a strip that reflows while the label is up
// walks the click target out from under the pointer that summoned it.
func TestWorkspacePillTooltipLeavesTheStripWhereItWas(t *testing.T) {
	m := pillTooltipOS(t)
	_ = pillFrame(m)
	was := pillHits(m)
	rect := pillRect(t, m, 2)
	before := pillFrame(m)

	m.DockWorkspaceHoverAt(rect.X0, rect.Y)
	m.Tooltip.At = time.Now().Add(-2 * tooltipDelay)
	after := pillFrame(m)

	if got := pillHits(m); len(got) != len(was) {
		t.Fatalf("the strip drew %d pills with the label up, %d without", len(got), len(was))
	} else {
		for i := range got {
			if got[i] != was[i] {
				t.Errorf("pill %d moved under the label: %+v then %+v", i, was[i], got[i])
			}
		}
	}
	if before[rect.Y] != after[rect.Y] {
		t.Errorf("the bar redrew itself under the label:\n%q\n%q", before[rect.Y], after[rect.Y])
	}
	// Everything but the one row the label occupies is untouched, which is what
	// makes this a label over the frame rather than a change to it.
	labelRow := rect.Y - 1
	if config.Global.DockbarPosition == "top" {
		labelRow = rect.Y + 1
	}
	for i := range before {
		if i == labelRow || i >= len(after) {
			continue
		}
		if before[i] != after[i] {
			t.Errorf("row %d changed with the label up:\n%q\n%q", i, before[i], after[i])
		}
	}
}

// TestWorkspacePillTooltipKeepsEveryHitOnItsDrawnCells walks the edges with the
// label up. Both edge columns of a pill still resolve to the workspace drawn
// there, and the bare column between two pills still belongs to neither.
func TestWorkspacePillTooltipKeepsEveryHitOnItsDrawnCells(t *testing.T) {
	m := pillTooltipOS(t)
	_, _, _ = hoverPill(t, m, 2)

	hits := pillHits(m)
	if len(hits) < 3 {
		t.Fatalf("the strip recorded %d rects, want at least three", len(hits))
	}
	for i, h := range hits {
		if i > 0 {
			if prev := hits[i-1]; h.X0 != prev.X1+dockWorkspacePillGap {
				t.Errorf("pill %d starts at %d but its neighbour ended at %d", i, h.X0, prev.X1)
			}
		}
		if h.Workspace == 0 {
			continue // the "+" tab, which stands for a workspace that does not exist yet
		}
		for _, x := range []int{h.X0, h.X1 - 1} {
			if got := m.DockWorkspaceAt(x, h.Y); got != h.Workspace {
				t.Errorf("pill %d: column %d in [%d,%d) resolves to workspace %d, want %d",
					i, x, h.X0, h.X1, got, h.Workspace)
			}
		}
	}
	first := hits[0]
	if got := m.DockWorkspaceAt(first.X0-1, first.Y); got != 0 {
		t.Errorf("the cell before the strip resolves to workspace %d", got)
	}
}

// TestWorkspacePillTooltipStaysOffWhenTheNameFits: a pill already saying all of
// its name has nothing to reveal, and a label repeating the screen would hold
// the maintenance tick open across the delay for nothing.
func TestWorkspacePillTooltipStaysOffWhenTheNameFits(t *testing.T) {
	m := pillTooltipOS(t)
	_ = pillFrame(m)
	rect := pillRect(t, m, 3) // "docs"

	if !m.DockWorkspaceHoverAt(rect.X0, rect.Y) {
		t.Fatal("the pointer was not on workspace 3's pill")
	}
	if m.Tooltip.Source != tooltipNone {
		t.Errorf("a pill whose name fits armed a %v label", m.Tooltip.Source)
	}
	if m.TooltipPending() {
		t.Error("a pill whose name fits is holding the tick open")
	}
}

// TestWorkspacePillTooltipClearsWhenThePointerLeaves: the label is
// gesture-scoped, so leaving the strip drops it and the tick it was holding.
func TestWorkspacePillTooltipClearsWhenThePointerLeaves(t *testing.T) {
	m := pillTooltipOS(t)
	_, after, rect := hoverPill(t, m, 2)
	if !strings.Contains(strings.Join(after, "\n"), pillTooltipName) {
		t.Fatal("the hover drew no label to leave")
	}

	if m.DockWorkspaceHoverAt(0, rect.Y) {
		t.Fatal("column 0 of the bar reported a pill under the pointer")
	}
	if m.Tooltip.Source != tooltipNone {
		t.Errorf("moving off the strip left a %v label armed", m.Tooltip.Source)
	}
	if m.renderTooltip() != nil {
		t.Error("the pointer is off the strip and a label drew anyway")
	}
	if got := strings.Join(pillFrame(m), "\n"); strings.Contains(got, pillTooltipName) {
		t.Errorf("the label survived the pointer leaving:\n%s", got)
	}
}

// TestWorkspacePillTooltipCostsNoIdleTick: pending is the only state that holds
// the maintenance tick, and it closes on the frame that draws the label. There
// is no standing tick behind any of this, so a label left up must not become
// one.
func TestWorkspacePillTooltipCostsNoIdleTick(t *testing.T) {
	m := pillTooltipOS(t)
	_ = pillFrame(m)
	if m.TooltipPending() {
		t.Fatal("a fresh dock is already pending a label")
	}

	rect := pillRect(t, m, 2)
	m.DockWorkspaceHoverAt(rect.X0, rect.Y)
	if !m.TooltipPending() {
		t.Fatal("hovering a clipped pill armed nothing")
	}
	m.Tooltip.At = time.Now().Add(-2 * tooltipDelay)
	_ = pillFrame(m)
	if m.TooltipPending() {
		t.Error("the label has been drawn and is still holding the tick open")
	}
}

// TestWorkspacePillTooltipCanBeTurnedOff: off, the pill truncates exactly as it
// did before the label existed, and the hover arms nothing at all.
func TestWorkspacePillTooltipCanBeTurnedOff(t *testing.T) {
	m := pillTooltipOS(t)
	m.Settings.DockWorkspaceTooltip = false

	before := pillFrame(m)
	was := pillHits(m)
	rect := pillRect(t, m, 2)

	m.DockWorkspaceHoverAt(rect.X0, rect.Y)
	if m.Tooltip.Source != tooltipNone {
		t.Errorf("the key is off and a %v label armed anyway", m.Tooltip.Source)
	}
	if m.TooltipPending() {
		t.Error("the key is off and a label is pending anyway")
	}
	m.Tooltip.At = time.Now().Add(-2 * tooltipDelay)
	if m.renderTooltip() != nil {
		t.Error("the key is off and a label drew anyway")
	}

	after := pillFrame(m)
	if strings.Join(before, "\n") != strings.Join(after, "\n") {
		t.Error("hovering changed the frame with the key off")
	}
	if got := strings.Join(after, "\n"); strings.Contains(got, pillTooltipName) {
		t.Errorf("the key is off and the whole name is on screen anyway:\n%s", got)
	}
	// What is left is today's behaviour: the truncated label, nothing more.
	if want := m.workspacePillLabel(2); !strings.Contains(strings.Join(after, "\n"), want) {
		t.Errorf("the pill stopped drawing its truncated label %q", want)
	}
	for i := range was {
		if was[i] != pillHits(m)[i] {
			t.Errorf("pill %d moved with the key off: %+v then %+v", i, was[i], pillHits(m)[i])
		}
	}
}

// TestWorkspacePillTooltipInASCIIAndMonochrome: the label is words on a row, so
// it must survive a terminal with no drawing glyphs and one with no palette.
// Both frames are checked with the colours already stripped, which is the
// monochrome case, and the ASCII run additionally holds the label to plain
// bytes.
func TestWorkspacePillTooltipInASCIIAndMonochrome(t *testing.T) {
	t.Run("monochrome", func(t *testing.T) {
		m := pillTooltipOS(t)
		_, after, rect := hoverPill(t, m, 2)
		row := rect.Y - 1
		if config.Global.DockbarPosition == "top" {
			row = rect.Y + 1
		}
		if !strings.Contains(safeRow(after, row), pillTooltipName) {
			t.Errorf("with every colour dropped the label reads %q", safeRow(after, row))
		}
	})

	t.Run("ascii", func(t *testing.T) {
		prev := config.Global.UseASCIIOnly
		config.Global.UseASCIIOnly = true
		overlay.SetASCII(true)
		t.Cleanup(func() {
			config.Global.UseASCIIOnly = prev
			overlay.SetASCII(prev)
		})

		m := pillTooltipOS(t)
		_, after, rect := hoverPill(t, m, 2)
		row := rect.Y - 1
		if config.Global.DockbarPosition == "top" {
			row = rect.Y + 1
		}
		line := safeRow(after, row)
		if !strings.Contains(line, pillTooltipName) {
			t.Errorf("the ASCII label reads %q", line)
		}
		for _, r := range line {
			if r > 127 {
				t.Errorf("the ASCII label row carries %q: %q", r, line)
				break
			}
		}
	})
}

// safeRow is row i of a frame, or "" when the frame is shorter than that.
func safeRow(frame []string, i int) string {
	if i < 0 || i >= len(frame) {
		return ""
	}
	return frame[i]
}
