package input

import (
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

func autoScrollTickCmd() tea.Cmd {
	return tea.Tick(50*time.Millisecond, func(time.Time) tea.Msg { return app.AutoScrollTickMsg{} })
}

func cockpitMousePress(msg tea.MouseClickMsg, o *app.OS) (bool, tea.Cmd) {
	x, y := msg.Mouse().X, msg.Mouse().Y
	o.CockpitLinkCancel()
	g := o.CockpitMouse()
	g.Armed = false
	if msg.Button != tea.MouseLeft || !o.CockpitThreadAt(x, y) {
		return false, nil
	}
	o.CancelPendingCopy()
	clicks := g.Register(x, y, o.DoubleClickWindow())
	g.X, g.Y, g.Dragged, g.Dir = x, y, false, 0
	shift := msg.Mod&tea.ModShift != 0
	on := o.CockpitCopyOn()
	if on && o.CockpitCopyImplicit() && clicks == 1 && !shift {
		o.CockpitCopyEnd()
		on = false
	}
	if !on && clicks == 1 {
		g.Armed = !shift
		return false, nil
	}
	cm, grid, ok := o.CockpitMouseCopy()
	if !ok {
		return false, nil
	}
	cx, cy, rows := o.CockpitCopyCell(x, y)
	if shift {
		gridDrag(cm, grid, cx, cy, rows)
		g.Dragged = true
	} else {
		gridPress(cm, grid, cx, cy, rows, clicks, &o.Settings)
	}
	g.Armed = true
	return true, nil
}

func cockpitMouseMotion(msg tea.MouseMotionMsg, o *app.OS) (bool, tea.Cmd) {
	g := o.CockpitMouse()
	if !g.Armed || msg.Button != tea.MouseLeft {
		return false, nil
	}
	x, y := msg.Mouse().X, msg.Mouse().Y
	if !g.Dragged && x == g.X && y == g.Y {
		return true, nil
	}
	if !o.CockpitCopyOn() {
		cm, grid, ok := o.CockpitMouseCopy()
		if !ok {
			g.Armed = false
			return false, nil
		}
		cx, cy, rows := o.CockpitCopyCell(g.X, g.Y)
		gridPress(cm, grid, cx, cy, rows, 1, &o.Settings)
	}
	cm, grid := o.CockpitCopy()
	if cm == nil {
		g.Armed = false
		return false, nil
	}
	g.Dragged = true
	cx, cy, rows := o.CockpitCopyCell(x, y)
	dir := gridDrag(cm, grid, cx, cy, rows)
	start := dir != 0 && g.Dir == 0
	g.Dir = dir
	if start {
		return true, autoScrollTickCmd()
	}
	return true, nil
}

func cockpitMouseRelease(o *app.OS) (bool, tea.Cmd) {
	g := o.CockpitMouse()
	if !g.Armed {
		return false, nil
	}
	g.Armed, g.Dir = false, 0
	if !o.CockpitCopyOn() || !g.Dragged && g.Clicks < 2 {
		return g.Dragged, nil
	}
	return true, o.CockpitCopyFinish(g.Clicks)
}

func quickLookCopyPress(msg tea.MouseClickMsg, o *app.OS) bool {
	x, y := msg.Mouse().X, msg.Mouse().Y
	g := o.QuickLookMouse()
	if g == nil {
		return false
	}
	g.Armed = false
	if msg.Button != tea.MouseLeft || !o.QuickLookCopyOnText(x, y) {
		return false
	}
	o.CancelPendingCopy()
	clicks := g.Register(x, y, o.DoubleClickWindow())
	g.X, g.Y, g.Dragged = x, y, false
	shift := msg.Mod&tea.ModShift != 0
	cm, grid := o.QuickLookCopy()
	if cm != nil && cm.Implicit && clicks == 1 && !shift {
		o.QuickLookCopyEnd()
		cm = nil
	}
	if cm == nil && clicks == 1 {
		g.Armed = !shift
		return true
	}
	if cm == nil {
		var ok bool
		if cm, grid, ok = o.QuickLookMouseCopy(); !ok {
			return true
		}
	}
	cx, cy, rows, _ := o.QuickLookCopyCell(x, y)
	g.Armed = true
	if shift {
		gridDrag(cm, grid, cx, cy, rows)
		g.Dragged = true
		return true
	}
	gridPress(cm, grid, cx, cy, rows, clicks, &o.Settings)
	return true
}

func quickLookCopyMotion(msg tea.MouseMotionMsg, o *app.OS) bool {
	g := o.QuickLookMouse()
	if g == nil || !g.Armed || msg.Button != tea.MouseLeft {
		return false
	}
	x, y := msg.Mouse().X, msg.Mouse().Y
	if !g.Dragged && x == g.X && y == g.Y {
		return true
	}
	cm, grid := o.QuickLookCopy()
	if cm == nil {
		var ok bool
		if cm, grid, ok = o.QuickLookMouseCopy(); !ok {
			g.Armed = false
			return false
		}
		px, py, rows, _ := o.QuickLookCopyCell(g.X, g.Y)
		gridPress(cm, grid, px, py, rows, 1, &o.Settings)
	}
	cx, cy, rows, ok := o.QuickLookCopyCell(x, y)
	if !ok {
		g.Armed = false
		return false
	}
	g.Dragged = true
	gridDrag(cm, grid, cx, cy, rows)
	return true
}

func quickLookCopyRelease(o *app.OS) (bool, tea.Cmd) {
	g := o.QuickLookMouse()
	if g == nil || !g.Armed {
		return false, nil
	}
	g.Armed = false
	cm, grid := o.QuickLookCopy()
	if cm == nil || !cm.State.InVisual() || !g.Dragged && g.Clicks < 2 {
		if cm != nil && cm.Implicit {
			o.QuickLookCopyEnd()
		}
		return true, nil
	}
	text, ok := o.QuickLookCopyText()
	if !ok {
		text = extractVisualText(cm, grid)
	}
	return true, o.QuickLookCopyFinish(text)
}

func handleSelectionCopy(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	text, _, _, ok := o.CockpitThreadSelection()
	if !ok {
		return o, nil
	}
	return o, tea.Batch(o.CopyToClipboard(text), o.SaveToPasteBuffers(text))
}

func handleSelectionCopyMarkdown(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	_, y0, y1, ok := o.CockpitThreadSelection()
	if !ok {
		return o, nil
	}
	text := o.CockpitCopyMarkdown(y0, y1)
	if text == "" {
		return o, nil
	}
	o.ShowNotification(fmt.Sprintf("Copied as Markdown, %d chars", len(text)), "success", o.Settings.NotificationDuration)
	return o, o.CopyToClipboard(text)
}

func handleSelectionSearch(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	text, _, _, ok := o.CockpitThreadSelection()
	cm, grid := o.CockpitCopy()
	if !ok || cm == nil {
		return o, nil
	}
	query, _, _ := strings.Cut(text, "\n")
	o.CockpitCopyKeep()
	cm.State = terminal.CopyModeNormal
	cm.BeginSearch(false, grid.ScrollbackLen())
	cm.Search.Set(query)
	executeSearch(cm, grid, o.Settings.SearchMaxMatches)
	cm.State = terminal.CopyModeNormal
	o.ShowNotification(fmt.Sprintf("/%s (%s matches)", query, terminal.SearchMatchCount(len(cm.SearchMatches), o.Settings.SearchMaxMatches)), "info", o.Settings.NotificationDuration)
	return o, nil
}
