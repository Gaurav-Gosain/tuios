package app

import "github.com/Gaurav-Gosain/tuios/internal/sessiontree"

// The rail is a map of what is open, and a map that scrolls the place you just
// went to off its own edge is a list you lose your place in. Focusing a pane
// far down the terminals section, or switching to a session below the fold,
// used to leave the row that now matters most out of sight: the agents section
// anchors its viewport to a row, and the cursor auto-scrolls while the rail has
// the keyboard, but nothing moved either of the other two sections on a focus
// change that came from a click, the palette, a hook or a key on the panes.
//
// The reveal is that missing move. It fires on a focus change only, never on
// every frame, because a reveal that fires on every frame fights the reader:
// they wheel the section away to look at something else and the rail wheels
// it straight back. So it remembers what the last frame was drawn for and acts
// only when the focused pane or the attached session is not what it was.
//
// It scrolls the least it can, the way the keyboard cursor does: a row below
// the fold lands on the last visible line, a row above it on the first. No
// centring and no easing. The rows are one line tall and the wheel jumps, so a
// scroll that slid over several frames would be motion in the corner of the
// eye on every pane switch, and the cursor auto-scroll that runs after it has
// to agree with it about what is on screen.
//
// A wheel or a drag between frames outranks the reveal, as it outranks the
// agents anchor: the reveal remembers the offset each section came to rest on
// and stands down when the offset is no longer that one, because a changed
// offset is the reader saying where to look now.

// sidebarRevealState is what the last frame was drawn for: the focused pane,
// the attached session, and the offsets the two sections settled on.
type sidebarRevealState struct {
	WindowID  string
	SessionID string
	ScrollT   int
	ScrollS   int
	Valid     bool
}

// sidebarRevealFocus scrolls the terminals section to the focused pane and the
// sessions section to the attached session, each only when that identity has
// changed since the last frame and the section's offset is still the one the
// last frame left. rowsT and rowsS are the sections' folds in rows.
func (m *OS) sidebarRevealFocus(sessions []sessiontree.Node, terminals []sidebarTerminalEntry, rowsT, rowsS int) {
	r := m.sidebarReveal
	focused := m.sidebarFocusedWindowID()
	attached := m.sidebarCurrentSessionID()
	if focused != "" && (!r.Valid || focused != r.WindowID) && (!r.Valid || m.SidebarScrollT == r.ScrollT) {
		for i, e := range terminals {
			if e.WindowID == focused {
				m.SidebarScrollT = sidebarRevealOffset(m.SidebarScrollT, i, rowsT, len(terminals))
				break
			}
		}
	}
	if (!r.Valid || attached != r.SessionID) && (!r.Valid || m.SidebarScrollS == r.ScrollS) {
		for i, s := range sessions {
			if s.ID == attached && !isRemoteNode(s) {
				m.SidebarScrollS = sidebarRevealOffset(m.SidebarScrollS, i, rowsS, len(sessions))
				break
			}
		}
	}
}

// sidebarRecordReveal remembers what this frame was drawn for, after the
// windowing has had the last word on the offsets.
func (m *OS) sidebarRecordReveal() {
	m.sidebarReveal = sidebarRevealState{
		WindowID:  m.sidebarFocusedWindowID(),
		SessionID: m.sidebarCurrentSessionID(),
		ScrollT:   m.SidebarScrollT,
		ScrollS:   m.SidebarScrollS,
		Valid:     true,
	}
}

// sidebarFocusedWindowID is the focused pane's id, or "" with no pane focused.
func (m *OS) sidebarFocusedWindowID() string {
	if w := m.GetFocusedWindow(); w != nil {
		return w.ID
	}
	return ""
}

// sidebarRevealOffset is the least scroll that puts row idx on screen in a
// section of total rows with a fold of rows lines, starting from scroll.
//
// It knows the section's overflow rule: a section scrolled short of its end
// spends its last line on "… +N", so only rows-1 of its rows are drawn there,
// and a row placed on that last line would be under the count rather than on
// screen. At the end of the section every line is a row again.
func sidebarRevealOffset(scroll, idx, rows, total int) int {
	if rows <= 0 || total <= rows || idx < 0 || idx >= total {
		return scroll
	}
	maxScroll := total - rows
	scroll = max(min(scroll, maxScroll), 0)
	if idx < scroll {
		return idx
	}
	visible := rows
	if scroll < maxScroll {
		visible = rows - 1
	}
	if idx < scroll+visible {
		return scroll
	}
	// Land the row on the last drawn line: one above the "+N" line unless the
	// section is then at its end, where the count goes and the row takes the
	// line it stood on.
	return max(min(idx-rows+2, maxScroll), 0)
}
