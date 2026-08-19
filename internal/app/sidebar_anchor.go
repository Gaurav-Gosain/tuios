package app

// The rail's cursor has always been anchored to a row identity rather than to a
// position: sidebarPublishNav re-finds it after every relayout, which is why a
// reorder cannot activate the wrong pane. The agents section's viewport was
// not, and that section is the one whose order is a function of live agent
// state. With the default priority sort, a pane going needs_input thirty rows
// below the fold moves to the front and pushes every row under the reader down
// one, with nothing to compensate: the rows on screen were addressed by index,
// so the index kept pointing at the same slot while the list moved through it.
//
// Anchoring the viewport the same way the cursor is anchored is what makes the
// priority sort safe to keep as the default. The two mechanisms have to agree
// about what is on screen, so the anchor is applied before the cursor's
// auto-scroll and recorded after the frame's windowing, which leaves the cursor
// the last word.
//
// Only the agents section needs it. The sessions section is in the user's own
// order and never resorts spontaneously; the terminals section is ordered by
// workspace and then by the session's pane order, and neither moves on agent
// state.

// sidebarScrollAnchor is the row a section's viewport was left resting on, plus
// the offset that row was at when it was recorded.
type sidebarScrollAnchor struct {
	SessionID string
	WindowID  string
	Offset    int
	Valid     bool
}

// sidebarReanchorAgents puts the agents viewport back on the row it was left on,
// wherever the sort has since moved it. It takes the section's final order and
// runs before anything is windowed.
//
// The anchor only speaks for an offset nothing else has touched, which is what
// the recorded Offset is for: a wheel, a drag or a jump between frames is the
// reader saying where to look now, and it outranks what they were looking at a
// moment ago.
//
// When the anchored row is gone (closed, filtered out, its session killed) the
// offset stands and sidebarWindowSection clamps it to the list that exists. The
// next frame re-anchors onto whatever row that landed on, so a vanished anchor
// costs one frame of drift instead of a permanent one, and never a jump to the
// top of the section.
func (m *OS) sidebarReanchorAgents(agents []sidebarAgentEntry) {
	a := m.sidebarAgentAnchor
	if !a.Valid || a.Offset != m.SidebarScrollA {
		return
	}
	for i, e := range agents {
		if e.WindowID == a.WindowID && e.SessionID == a.SessionID {
			m.SidebarScrollA = i
			return
		}
	}
}

// sidebarRecordAgentAnchor remembers the row the agents viewport came to rest
// on, after the frame's windowing has had the last word on the offset. A frame
// that drew no agent rows records nothing, so a rail too short for the section
// cannot leave an anchor behind that a taller one would act on.
//
// A section resting at the top records nothing either, and that is a rule rather
// than an economy. The top of a priority-sorted list is where the loudest thing
// is, and a reader who has not scrolled is reading exactly that; anchoring them
// to the row that happened to be first would scroll the arriving alarm out of
// sight to keep a calmer row in place. Nothing is preserved by holding position
// zero, so a resting rail also leaves the signature exactly as it found it.
func (m *OS) sidebarRecordAgentAnchor(agents []sidebarAgentEntry, start, shown int) {
	if shown <= 0 || start <= 0 || start >= len(agents) {
		m.sidebarAgentAnchor = sidebarScrollAnchor{}
		return
	}
	e := agents[start]
	m.sidebarAgentAnchor = sidebarScrollAnchor{
		SessionID: e.SessionID,
		WindowID:  e.WindowID,
		Offset:    start,
		Valid:     true,
	}
}
