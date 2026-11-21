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

type paneLabel struct {
	windowID string
	label    string
	name     string
	shown    bool
}

type paneLabelsState struct {
	workspace int
	panes     []paneLabel
	listOn    string
	typed     string
	layout    string
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

func (m *OS) paneLabelVisible(w *terminal.Window) image.Rectangle {
	return paneLabelRect(w).Intersect(m.hintsRegion())
}

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

func (s *paneLabelsState) paneLabelFor(id string) *paneLabel {
	for i := range s.panes {
		if s.panes[i].windowID == id {
			return &s.panes[i]
		}
	}
	return nil
}

func (s *paneLabelsState) hiddenPanes() []paneLabel {
	var out []paneLabel
	for _, p := range s.panes {
		if !p.shown {
			out = append(out, p)
		}
	}
	return out
}

func paneLabelRect(w *terminal.Window) image.Rectangle {
	r := paneContentRect(w)
	if r.Empty() {
		return image.Rect(w.X, w.Y, w.X+w.Width, w.Y+w.Height)
	}
	return r
}
