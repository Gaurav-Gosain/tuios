package input

import (
	"fmt"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// Multi copy mode's edges, each one a way the first build got it wrong.

// A count typed before y is dropped in every pane; the yank still takes every
// selection. It used to reach only the focused pane, as a count.
//
// Negative control: putting back the PendingCount == 0 condition on the
// whole-set keys fails this.
func TestMultiCopyCountBeforeYankTakesEverySelection(t *testing.T) {
	o, w := multiCopyOS(t)
	o.EnterCopyModeFocused()
	typeSearch(o, "lldp")
	mcPress(o, "V", "1")
	got := clipboardOf(t, mcPress(o, "y"))
	if want := "LLDP neighbor: swp1 rack-sw-01\nLLDP neighbor: swp2 rack-sw-01\n"; got != want {
		t.Errorf("1y yanked %q, want both selections %q", got, want)
	}
	for _, p := range w[:3] {
		if p.CopyMode.PendingCount != 0 {
			t.Errorf("pane %s kept the count %d", p.ID, p.CopyMode.PendingCount)
		}
	}
	// The same for tab and Y.
	mcPress(o, "V", "2", "tab")
	if o.MultiCopy.Format != config.MultiCopyFormatMarkdown {
		t.Errorf("2 then tab did not change the format")
	}
	mcPress(o, "3", "Y")
	if o.MultiCopy.Save == nil || len(o.MultiCopy.Save.Panes) != 2 {
		t.Fatalf("3Y did not open the save prompt with both selections")
	}
}

// A paste while the save prompt is open is the path, cleaned. It never
// reaches the pane: a path ending in a newline would run in the shell.
func TestMultiCopySavePromptTakesThePaste(t *testing.T) {
	o, w := multiCopyOS(t)
	o.EnterCopyModeFocused()
	typeSearch(o, "lldp")
	mcPress(o, "V", "Y")
	o.Mode = app.TerminalMode
	o.MultiCopy.Save.Path = ""
	HandleInput(tea.PasteMsg{Content: "~/rack\x1b[31m‮.md\r\n"}, o)
	if o.MultiCopy == nil || o.MultiCopy.Save == nil {
		t.Fatal("the paste closed the save prompt")
	}
	if got := o.MultiCopy.Save.Path; got != "~/rack[31m.md" {
		t.Errorf("the pasted path is %q, want it without control characters", got)
	}
	if !w[0].InCopyMode() {
		t.Error("the paste left copy mode")
	}
}

// A pane minimised or moved to another workspace leaves the mode: out of copy
// mode, out of the yank, out of the count.
//
// Negative control: dropping the on-screen check from SettleMultiCopy fails
// this.
func TestMultiCopyDropsPanesThatLeaveTheScreen(t *testing.T) {
	o, w := multiCopyOS(t)
	o.EnterCopyModeFocused()
	typeSearch(o, "lldp")
	mcPress(o, "V")
	w[1].Minimized = true
	// Any input settles it, a mouse event as well as a key.
	HandleInput(tea.MouseMotionMsg{X: 1, Y: 1}, o)
	if w[1].InCopyMode() || o.MultiCopy.Has(w[1].ID) {
		t.Fatal("a minimised pane is still in multi copy mode")
	}
	got := clipboardOf(t, mcPress(o, "y"))
	if want := "LLDP neighbor: swp1 rack-sw-01\n"; got != want {
		t.Errorf("yank after minimising = %q, want %q", got, want)
	}
	w[2].Workspace = 2
	o.SettleMultiCopy()
	if w[2].InCopyMode() || o.MultiCopy.Has(w[2].ID) {
		t.Error("a pane moved to another workspace is still in multi copy mode")
	}
	if _, total := o.MultiCopyMatched(); total != 1 {
		t.Errorf("the mode counts %d panes, want 1", total)
	}
}

// A search that matches no pane parks them all, the focused one included:
// nothing moves and nothing is selected.
//
// Negative control: exempting the lead from the parked keys fails this.
func TestMultiCopySearchWithNoMatchMovesNothing(t *testing.T) {
	o, w := multiCopyOS(t)
	o.EnterCopyModeFocused()
	before := *w[0].CopyMode
	typeSearch(o, "zzz-nowhere")
	if matched, _ := o.MultiCopyMatched(); matched != 0 {
		t.Fatalf("%d panes matched a search for text that is nowhere", matched)
	}
	mcPress(o, "j", "j", "V")
	for _, p := range w[:3] {
		if p.HasSelection() {
			t.Errorf("pane %s selected after a search that matched nothing", p.ID)
		}
	}
	if w[0].CopyMode.CursorY != before.CursorY {
		t.Errorf("the focused pane moved from row %d to %d", before.CursorY, w[0].CopyMode.CursorY)
	}
	// A new search is still allowed, and one that matches unparks.
	typeSearch(o, "lldp")
	if matched, _ := o.MultiCopyMatched(); matched != 2 {
		t.Errorf("after a matching search %d panes matched, want 2", matched)
	}
}

// Shift+Up and Shift+Down scroll every pane of the set, and leave them in
// copy mode.
func TestMultiCopyShiftArrowsScrollEveryPane(t *testing.T) {
	o, w := multiCopyOS(t)
	for _, p := range w[:3] {
		for i := range 40 {
			p.WriteOutput(fmt.Appendf(nil, "filler %d\r\n", i))
		}
	}
	o.KeybindRegistry = config.NewKeybindRegistry(config.DefaultConfig())
	o.EnterCopyModeFocused()
	o.Mode = app.TerminalMode
	start := make([]int, 3)
	for i, p := range w[:3] {
		start[i] = p.CopyMode.ScrollOffset*1000 - p.CopyMode.CursorY
	}
	for range 20 {
		HandleKeyPress(tea.KeyPressMsg{Code: tea.KeyUp, Mod: tea.ModShift}, o)
	}
	for i, p := range w[:3] {
		if p.CopyMode.ScrollOffset*1000-p.CopyMode.CursorY == start[i] {
			t.Errorf("pane %s did not scroll", p.ID)
		}
	}
	for range 40 {
		HandleKeyPress(tea.KeyPressMsg{Code: tea.KeyDown, Mod: tea.ModShift}, o)
	}
	if o.MultiCopy == nil {
		t.Fatal("scrolling ended multi copy mode")
	}
	for _, p := range w[:3] {
		if !p.InCopyMode() {
			t.Errorf("pane %s left copy mode at the bottom", p.ID)
		}
	}
}
