package app

import (
	"image/color"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
	"github.com/Gaurav-Gosain/tuios/internal/tmldraw"
)

func tmlNameParts(name string) (owner, title string, ok bool) {
	rest, found := strings.CutPrefix(name, "from ")
	if !found {
		return "", "", false
	}
	owner, title, ok = strings.Cut(rest, " · ")
	if !ok || owner == "" {
		return "", "", false
	}
	return owner, title, true
}

func tmlWindowParts(w *terminal.Window) (owner, title string, ok bool) {
	if w == nil || w.Kind != session.WindowKindTML {
		return "", "", false
	}
	return tmlNameParts(w.CustomName)
}

func tmlRailLabel(w *terminal.Window) (string, bool) {
	owner, title, ok := tmlWindowParts(w)
	if !ok {
		return "", false
	}
	if title == "" {
		return "from " + owner, true
	}
	return overlay.Truncate(title, tmlRailTitleCells) + " · " + owner, true
}

const tmlRailTitleCells = 24

func (m *OS) isTMLPopup(w *terminal.Window) bool {
	return w != nil && w.IsPopup && !w.Tiled && m.DrawsTML(w)
}

func (m *OS) tmlInset(w *terminal.Window) (dx, dy, iw, ih int) {
	cw, ch := max(w.ContentWidth(), 1), max(w.ContentHeight(), 1)
	if !m.DrawsTML(w) {
		return 0, 0, cw, ch
	}
	return tmldraw.Inset(cw, ch)
}

func (m *OS) renderTMLDialogBox(window *terminal.Window, content string) string {
	pal := theme.UI()
	var bg color.Color = lipgloss.NoColor{}
	w := window.ContentWidth()
	tl, tr, bl, br, h, v, vr := "╭", "╮", "╰", "╯", "─", "│", "│"
	bottomRule := "─"
	if overlay.UseASCII() {
		tl, tr, bl, br, h, v, vr, bottomRule = "+", "+", "+", "+", "-", "|", "|", "-"
	}
	frame := overlay.Style(bg).Foreground(pal.FgMute)
	gap := overlay.Style(bg).Render(" ")
	ruleOf := func(glyph string) func(int) string {
		return func(n int) string {
			if n <= 0 {
				return ""
			}
			return frame.Render(strings.Repeat(glyph, n))
		}
	}
	rule := ruleOf(h)

	owner, title, _ := tmlWindowParts(window)
	ownerText := ""
	if owner != "" {
		ownerText = overlay.Truncate("from "+owner, max(w-4, 1))
	}
	ownerW := lipgloss.Width(ownerText)
	const sep = " · "
	titleRoom := w - 3 - ownerW - len([]rune(sep))
	titleText := ""
	if titleRoom >= 4 && title != "" {
		titleText = overlay.Truncate(title, titleRoom)
	}
	top := frame.Render(tl) + rule(1) + gap
	used := 2
	if ownerW > 0 {
		top += overlay.Style(bg).Foreground(pal.FgDim).Render(ownerText)
		used += ownerW
	}
	if titleText != "" {
		if ownerW > 0 {
			top += overlay.Style(bg).Foreground(pal.FgMute).Render(sep)
			used += len([]rune(sep))
		}
		top += overlay.Style(bg).Foreground(overlay.Readable(pal.Accent, bg)).Bold(true).Render(titleText)
		used += lipgloss.Width(titleText)
	}
	dock := " dock "
	dockW := lipgloss.Width(dock)
	var controls []WindowButtonRect
	if fill := w - used - 2 - dockW; fill >= 1 {
		top += gap + rule(fill) + overlay.Style(bg).Foreground(overlay.Readable(pal.AccentBright, bg)).Bold(true).Render(dock) + rule(1) + frame.Render(tr)
		controls = []WindowButtonRect{{Action: WindowButtonFloat, X: window.X + 1 + w - 1 - dockW, W: dockW, Y: window.Y}}
	} else {
		top += gap + rule(w-used-1) + frame.Render(tr)
	}

	hints := []overlay.Hint{{Key: "esc", Label: "close", Priority: overlay.HintEssential}}
	if key := contextMenuHint(m.KeybindRegistry, "window_prefix_float", &m.Settings, m.Mode); key != "" {
		hints = append(hints, overlay.Hint{Key: key, Label: "dock as a pane"})
	}
	strip, stripW := overlay.FitHintStrip(hints, max(w-3, 0), bg, pal)
	bottomLine := ruleOf(bottomRule)
	bottom := frame.Render(bl)
	if stripW > 0 && w-stripW-3 >= 1 {
		bottom += bottomLine(w-stripW-3) + gap + strip + gap + bottomLine(1)
	} else {
		bottom += bottomLine(w)
	}
	bottom += frame.Render(br)

	left, right := frame.Render(v), frame.Render(vr)
	lines := strings.Split(content, "\n")
	out := make([]string, 0, window.Height)
	out = append(out, top)
	for i := range window.ContentHeight() {
		l := ""
		if i < len(lines) {
			l = lines[i]
		}
		out = append(out, left+overlay.Fill(l, w, bg)+right)
	}
	out = append(out, bottom)
	m.recordWindowButtons(window.ID, controls)
	return strings.Join(out, "\n")
}

func (m *OS) tmlOwnerTag(window *terminal.Window, s *config.Settings, room int) (tag string, width int) {
	owner, _, ok := tmlWindowParts(window)
	if !ok || room < 4 {
		return "", 0
	}
	text := " from " + owner + " "
	if ansi.StringWidth(text) > room {
		text = " " + overlay.Truncate("from "+owner, room-2) + " "
	}
	pal := theme.UI()
	fg := pal.FgDim
	if s.WindowTitleStyle != "pill" && s.WindowTitleStyle != "" && m.GetFocusedWindow() != window {
		fg = pal.FgMute
	}
	return lipgloss.NewStyle().Foreground(fg).Render(text), ansi.StringWidth(text)
}

func (m *OS) popupScrimTarget() (string, int) {
	w := m.GetFocusedWindow()
	if w == nil || w.Minimized || w.Workspace != m.CurrentWorkspace || !w.IsPopup || w.Tiled {
		return "", 0
	}
	dim := m.Settings.PopupDim
	if w.PopupDim != nil {
		dim = w.PopupDim.Percent
	} else if m.isTMLPopup(w) {
		dim = m.Settings.ModalDim
	}
	dim = min(max(dim, 0), config.PopupDimMax)
	if dim == 0 {
		return "", 0
	}
	return w.ID, dim
}

func scrimmedChrome(id string) bool {
	return id == "dock" || id == sidebarLayerID || id == "sidebar-secondary"
}
