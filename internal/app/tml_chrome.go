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

// The chrome tuios draws around a TML surface.
//
// A surface in a pane keeps the pane's frame. Its title bar shows the
// document's title in the pill and the owner after it in a quiet colour,
// "Test run  from shell". The title gives way first, so the owner is never
// cut: the person can always tell which pane opened what they are about to
// press.
//
// A surface in a popup is a dialog, not a pane: a hairline frame with the
// owner and then the title set into the top border, and the keys
// tuios answers for in the bottom border. The surface sits inside with room
// around it. The dialog has no fill of its own: it is on the pane's ground,
// as every surface is, and the frame and the scrim on the screen behind it
// (see scrim.go) are what set it apart.
//
// The owner comes from the window's name, which the daemon writes as
// "from OWNER · TITLE" (session.tmlWindowName). Neither part can hold the
// separator, so the split is exact, and no document can write the owner.

// tmlNameParts splits a TML window's name into the owner and the title.
// ok is false for a name the daemon did not write in that form.
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

// tmlWindowParts is tmlNameParts for a window, false for any window that
// is not a TML window.
func tmlWindowParts(w *terminal.Window) (owner, title string, ok bool) {
	if w == nil || w.Kind != session.WindowKindTML {
		return "", "", false
	}
	return tmlNameParts(w.CustomName)
}

// tmlRailLabel is the rail's label for a TML window: the document's title,
// then the owner. A rail row is cut at its end, so the title is held to
// tmlRailTitleCells, and the owner stays in sight on a rail of the usual
// width.
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

// tmlRailTitleCells caps the title part of a TML window's rail label.
const tmlRailTitleCells = 24

// isTMLPopup reports whether w is a popup that draws a TML surface as a
// dialog.
func (m *OS) isTMLPopup(w *terminal.Window) bool {
	return w != nil && w.IsPopup && !w.Tiled && m.DrawsTML(w)
}

// tmlInset is where the surface sits in a TML window's content box: the
// margin of tmldraw.Inset, for a window this client draws as TML.
func (m *OS) tmlInset(w *terminal.Window) (dx, dy, iw, ih int) {
	cw, ch := max(w.ContentWidth(), 1), max(w.ContentHeight(), 1)
	if !m.DrawsTML(w) {
		return 0, 0, cw, ch
	}
	return tmldraw.Inset(cw, ch)
}

// renderTMLDialogBox frames a TML popup's content as a dialog. content is
// the content box, ContentWidth by ContentHeight.
func (m *OS) renderTMLDialogBox(window *terminal.Window, content string) string {
	pal := theme.UI()
	// No fill: the pane's ground, which is the terminal's background or
	// appearance.pane_background, painted by the compositor.
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

	// Top border: the owner first, then the title, as the window name has
	// them. The owner is what the person must read first, so a title that
	// tries to pass for another owner can only come after the real one. The
	// title is the cleaned one from the window name, which cannot hold the
	// separator, and it gives way to the owner.
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
	// The control that docks the surface as a pane, at the right of the top
	// border, where a pane's title bar has its controls. It is recorded as a
	// window control, so the click path is the one every control takes.
	dock := " dock "
	dockW := lipgloss.Width(dock)
	var controls []WindowButtonRect
	if fill := w - used - 2 - dockW; fill >= 1 {
		top += gap + rule(fill) + overlay.Style(bg).Foreground(overlay.Readable(pal.AccentBright, bg)).Bold(true).Render(dock) + rule(1) + frame.Render(tr)
		controls = []WindowButtonRect{{Action: WindowButtonFloat, X: window.X + 1 + w - 1 - dockW, W: dockW, Y: window.Y}}
	} else {
		top += gap + rule(w-used-1) + frame.Render(tr)
	}

	// Bottom border: the key tuios answers for in every popup.
	hints := []overlay.Hint{{Key: "esc", Label: "close", Priority: overlay.HintEssential}}
	if key := contextMenuHint(m.KeybindRegistry, "window_prefix_float", &m.Settings); key != "" {
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

// tmlOwnerTag is what a TML pane's title bar shows after the title: the
// owner, a step quieter than the title. Beside a pill, and beside a plain
// title on the focused pane, that is the dim text colour. Beside a plain
// title on another pane, which is already dim, it is the muted one. Empty
// for any other window.
//
// room is the bar's room for the title and the tag. A tag wider than that is
// cut to it, and the title then gets no room: on a narrow pane the owner
// stays and the title goes.
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

// popupScrimTarget is the layer id and percent of the popup the screen is
// dimmed behind this frame. Only the focused popup dims the screen: it takes
// the keys, as a modal does. TML keeps the modal dim unless overridden.
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

// scrimmedChrome reports whether a layer is chrome the scrim of a popup
// dims after it is drawn: the dock and the rails sit above the panes, so
// they are drawn after the popup's layer and need a pass of their own.
func scrimmedChrome(id string) bool {
	return id == "dock" || id == sidebarLayerID || id == "sidebar-secondary"
}
