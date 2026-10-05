package app

import (
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
// The panes are taken in the order select_window_1 to select_window_9 count
// them, so with the default digit keys the first nine labels are the numbers
// those keys already give the same panes. A zoom shows one pane and hides the
// rest, so the zoomed pane's label also lists the hidden panes with theirs:
// focusing one of them moves the zoom to it (see ZoomFollowsFocus).
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
}

// PaneLabelsOpen reports whether the pane labels are up. Labels made for a
// workspace that is no longer on screen are closed first.
func (m *OS) PaneLabelsOpen() bool {
	if m.paneLabels != nil && m.paneLabels.workspace != m.CurrentWorkspace {
		m.ClosePaneLabels()
	}
	return m.paneLabels != nil
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
	if len(windows) == 0 {
		m.ShowNotification("This workspace has no panes to label.", "info", m.Settings.NotificationDuration)
		return
	}
	labels := hints.KeyLabels(len(windows), m.paneLabelKeys())
	state := &paneLabelsState{workspace: m.CurrentWorkspace}
	region := m.hintsRegion()
	for i, w := range windows {
		state.panes = append(state.panes, paneLabel{
			windowID: w.ID,
			label:    labels[i],
			name:     m.getWindowDisplayName(w),
			shown:    m.hintsDrawn(w) && paneContentRect(w).Overlaps(region),
		})
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

// paneLabelWindows is every pane the labels are for: the panes of the
// workspace on screen that are not minimised, in the order select_window_N
// counts them. A pane behind a zoom is in the list: a label can move the
// zoom to it.
func (m *OS) paneLabelWindows() []*terminal.Window {
	var out []*terminal.Window
	for _, w := range m.Windows {
		if w == nil || w.Workspace != m.CurrentWorkspace || w.Minimized {
			continue
		}
		out = append(out, w)
	}
	return out
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
