package app

import (
	"fmt"
	"image"
	"strings"
	"unicode"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/hints"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

// Pane labels, after tmux's display-panes.
//
// The display_panes action puts a large label on every pane of the workspace
// on screen. Typing a label focuses that pane. With more panes than label
// keys the labels take two keys, as hints mode's do. Esc closes.
//
// The labels are handed out in the order select_window_1 to select_window_9
// count the panes, minimised panes included when tiling is off, so with the
// default digit keys label 3 is the pane select_window_3 gives. A minimised
// pane gets no label: it is not on the screen to point at.
//
// A pane whose label would not be seen gets its label in a list instead: a
// pane behind a zoom, a pane mostly off the screen (a scrolling column at the
// edge, or a view of a larger session), and a pane too small for its label.
// The list is drawn under the label of the zoomed pane, else the focused
// pane, else the first pane shown. Focusing a pane behind a zoom moves the
// zoom to it (see ZoomFollowsFocus).
//
// The labels name the layout they were drawn for. When it changes under them
// (a pane opens, closes, moves, resizes or zooms, the session changes) they
// close, rather than point at panes that are no longer where they were.
//
// Nothing is copied. The labels are a pass over the canvas after each pane is
// drawn (pane_labels_render.go), so closing them is the next frame not
// running it.

// paneLabel is one labelled pane.
type paneLabel struct {
	windowID string
	label    string
	// name is what the pane is called on the rail: its own name, or the
	// title its program set.
	name string
	// shown says the frame draws the pane. A pane behind a zoom is not
	// drawn, and its label is listed on the zoomed pane's instead.
	shown bool
}

// paneLabelsState is the labels while they are up.
type paneLabelsState struct {
	// workspace is the workspace the labels were made for. A switch to
	// another one closes them.
	workspace int
	panes     []paneLabel
	// listOn is the pane whose label also lists the panes the frame does
	// not draw: the zoomed pane, else the focused pane, else the first one
	// drawn. Empty when every pane is drawn.
	listOn string
	// typed is the start of a label typed so far.
	typed string
	// layout is paneLabelsLayout when the labels were made.
	layout string
}

// PaneLabelsOpen reports whether the pane labels are up. Labels made for a
// layout that is no longer on screen are closed first: another workspace or
// session, or a pane opened, closed, moved, resized or zoomed since.
func (m *OS) PaneLabelsOpen() bool {
	if m.paneLabels != nil && m.paneLabels.layout != m.paneLabelsLayout() {
		m.ClosePaneLabels()
	}
	return m.paneLabels != nil
}

// paneLabelsLayout describes what the labels depend on: the session, the
// workspace, the region panes are drawn in, and each pane of the workspace
// with its box and its minimised and zoomed state.
func (m *OS) paneLabelsLayout() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s|%d|%v|%v", m.SessionName, m.CurrentWorkspace, m.hintsRegion(), m.sessionView.on)
	if v := m.sessionView; v.on {
		fmt.Fprintf(&b, "|%d,%d", v.dx, v.dy)
	}
	for _, w := range m.Windows {
		if w == nil || w.Workspace != m.CurrentWorkspace {
			continue
		}
		fmt.Fprintf(&b, "|%s,%d,%d,%d,%d,%v,%v,%v", w.ID, w.X, w.Y, w.Width, w.Height, w.Minimized, w.Zoomed, w.IsPopup)
	}
	return b.String()
}

// PaneLabels maps each labelled pane's id to its label, for tests.
func (m *OS) PaneLabels() map[string]string {
	if m.paneLabels == nil {
		return nil
	}
	out := make(map[string]string, len(m.paneLabels.panes))
	for _, p := range m.paneLabels.panes {
		out[p.windowID] = p.label
	}
	return out
}

// PaneLabelsTyped is the start of a label typed so far.
func (m *OS) PaneLabelsTyped() string {
	if m.paneLabels == nil {
		return ""
	}
	return m.paneLabels.typed
}

// paneLabelKeys is the label keys in force.
func (m *OS) paneLabelKeys() string {
	if m.UserConfig == nil {
		return config.PanesDefaultLabelKeys
	}
	return m.UserConfig.Panes.LabelKeysInUse()
}

// OpenPaneLabels is the display_panes action. It labels every pane of the
// workspace on screen, or says there is none.
func (m *OS) OpenPaneLabels() {
	m.ClosePaneLabels()
	m.CloseHints()
	windows := m.paneLabelWindows()
	labels := hints.KeyLabels(len(windows), m.paneLabelKeys())
	state := &paneLabelsState{workspace: m.CurrentWorkspace, layout: m.paneLabelsLayout()}
	for i, w := range windows {
		if w.Minimized {
			continue
		}
		state.panes = append(state.panes, paneLabel{
			windowID: w.ID,
			label:    labels[i],
			name:     m.getWindowDisplayName(w),
			shown:    m.hintsDrawn(w) && paneLabelFits(m.paneLabelVisible(w), labels[i]),
		})
	}
	if len(state.panes) == 0 {
		m.ShowNotification("This workspace has no panes to label.", "info", m.Settings.NotificationDuration)
		return
	}
	state.listOn = m.paneLabelListOn(state)
	m.paneLabels = state
	m.CancelCopyFlash()
	m.MarkAllDirty()
}

// paneLabelListOn picks the pane that lists the hidden panes. See
// paneLabelsState.listOn.
func (m *OS) paneLabelListOn(s *paneLabelsState) string {
	if len(s.hiddenPanes()) == 0 {
		return ""
	}
	shown := func(id string) bool {
		p := s.paneLabelFor(id)
		return p != nil && p.shown
	}
	if z := m.zoomedWindow(); z != nil && shown(z.ID) {
		return z.ID
	}
	if f := m.GetFocusedWindow(); f != nil && shown(f.ID) {
		return f.ID
	}
	for _, p := range s.panes {
		if p.shown {
			return p.windowID
		}
	}
	return ""
}

// paneLabelWindows is every pane select_window_N counts, in its order: the
// panes of the workspace on screen, without the minimised ones while tiling
// is on. The caller labels the list and then leaves the minimised panes out,
// so the labels keep select_window_N's numbers. A pane behind a zoom is in
// the list: a label can move the zoom to it.
func (m *OS) paneLabelWindows() []*terminal.Window {
	var out []*terminal.Window
	for _, w := range m.Windows {
		if w == nil || w.Workspace != m.CurrentWorkspace {
			continue
		}
		if m.AutoTiling && w.Minimized {
			continue
		}
		out = append(out, w)
	}
	return out
}

// paneLabelVisible is the part of a pane's label area that is on the
// screen, in layout coordinates.
func (m *OS) paneLabelVisible(w *terminal.Window) image.Rectangle {
	return paneLabelRect(w).Intersect(m.hintsRegion())
}

// paneLabelFits reports whether the smallest form of a label, its keys with
// a cell either side, fits in the visible part of a pane.
func paneLabelFits(r image.Rectangle, label string) bool {
	return r.Dx() >= len(label)+2 && r.Dy() >= 1
}

// ClosePaneLabels takes the labels down.
func (m *OS) ClosePaneLabels() {
	if m.paneLabels == nil {
		return
	}
	m.paneLabels = nil
	m.MarkAllDirty()
}

// PaneLabelsBackspace takes back the last key typed.
func (m *OS) PaneLabelsBackspace() {
	if m.paneLabels == nil || m.paneLabels.typed == "" {
		return
	}
	m.paneLabels.typed = m.paneLabels.typed[:len(m.paneLabels.typed)-1]
	m.MarkAllDirty()
}

// PaneLabelsUsesKey reports whether r is one of the keys labels are made
// of. A key outside them is free for the labels' own keys, such as q.
func (m *OS) PaneLabelsUsesKey(r rune) bool {
	if m.paneLabels == nil {
		return false
	}
	return strings.ContainsRune(m.paneLabelKeys(), unicode.ToLower(r))
}

// PaneLabelsPress takes one key of a label. A key that starts no label is
// ignored, so a slip does not close the labels. When the keys typed so far
// are a whole label, that pane takes the focus and the labels close. It
// returns whether a pane was focused.
func (m *OS) PaneLabelsPress(r rune) bool {
	s := m.paneLabels
	if s == nil {
		return false
	}
	typed := s.typed + string(unicode.ToLower(r))
	prefix := false
	for _, p := range s.panes {
		switch {
		case p.label == typed:
			m.ClosePaneLabels()
			for i, w := range m.Windows {
				if w != nil && w.ID == p.windowID {
					m.FocusWindow(i)
					return true
				}
			}
			return false
		case strings.HasPrefix(p.label, typed):
			prefix = true
		}
	}
	if prefix {
		s.typed = typed
		m.MarkAllDirty()
	}
	return false
}

// paneLabelFor is the label of the pane whose layer id is id, or nil.
func (s *paneLabelsState) paneLabelFor(id string) *paneLabel {
	for i := range s.panes {
		if s.panes[i].windowID == id {
			return &s.panes[i]
		}
	}
	return nil
}

// hiddenPanes is every labelled pane the frame does not draw.
func (s *paneLabelsState) hiddenPanes() []paneLabel {
	var out []paneLabel
	for _, p := range s.panes {
		if !p.shown {
			out = append(out, p)
		}
	}
	return out
}

// paneLabelRect is where the labels of the pane w go: its content box, or
// its whole box when it has no content area.
func paneLabelRect(w *terminal.Window) image.Rectangle {
	r := paneContentRect(w)
	if r.Empty() {
		return image.Rect(w.X, w.Y, w.X+w.Width, w.Y+w.Height)
	}
	return r
}
