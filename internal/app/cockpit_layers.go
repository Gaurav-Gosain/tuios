package app

// The agents view's layers. Sixteen sub-views can sit over the list: copy
// mode, the image view, the New agent sheet, a comparison, a surface, the new
// card line, an answer that waits for enter, the peek, the reply, the reason
// line, the search line, and the one-key menus (group, Ready to merge, snooze,
// the bulk sheet). Each keeps its own state in its own file. Which one is on
// top, and so owns the keys, the key bar and the clicks, is decided here and
// only here: CockpitTop. The key router, the key bar, the click handler and
// the frame cache all read it, so the key bar always says what the next key
// does. internal/lint holds the readers to it (TestCockpitLayersHaveOneDecider).
//
// The order is the order keys have always taken: copy mode first, then the
// sheets that take every key, then the answer asks, the lines, and the
// one-key menus last.
//
// The peek takes the keys of the prompt it reads, and any click closes it
// without acting: it has no controls a click works.
//
// A one-key menu takes the next key, and any other key closes it. A click
// that is not on it closes it too and does nothing else: the click is spent
// on the dismissal, so a click meant to close Ready to merge or a confirm can
// never also act under it. A one-key menu left open under a newer layer is
// closed by CockpitLayerCheck after the message, so two of them never stand at
// once.

// CockpitLayer is what sits on top of the agents view and owns its keys.
type CockpitLayer uint8

// The layers, top first. CockpitBase is the list and its regions.
const (
	CockpitBase CockpitLayer = iota
	CockpitLayerCopy
	CockpitLayerImage
	CockpitLayerSpawn
	CockpitLayerCompare
	CockpitLayerArtifact
	CockpitLayerNewCard
	CockpitLayerConfirm
	CockpitLayerPeek
	CockpitLayerReply
	CockpitLayerReason
	CockpitLayerSelect
	CockpitLayerGroupMenu
	CockpitLayerShip
	CockpitLayerSnooze
	CockpitLayerBulkSheet
	cockpitLayers
)

// cockpitLayerNames name the layers in the verbose log.
var cockpitLayerNames = [cockpitLayers]string{
	"base", "copy", "image", "spawn", "compare", "artifact", "new card", "confirm", "peek",
	"reply", "reason", "select", "group menu", "ship", "snooze", "bulk sheet",
}

// CockpitTop is the layer on top of the agents view now. One ordered switch:
// the first layer that is open wins.
func (m *OS) CockpitTop() CockpitLayer {
	st := &m.Inbox
	ck := &st.ck
	switch {
	case m.threadCopy() != nil:
		return CockpitLayerCopy
	case ck.img != nil:
		return CockpitLayerImage
	case ck.spawn != nil:
		return CockpitLayerSpawn
	case ck.cmp != nil:
		return CockpitLayerCompare
	case m.CockpitArtifactFocused():
		return CockpitLayerArtifact
	case ck.kb.add != nil:
		return CockpitLayerNewCard
	case m.InboxConfirmOpen():
		return CockpitLayerConfirm
	case st.Peek != nil:
		return CockpitLayerPeek
	case m.InboxReplyOpen():
		return CockpitLayerReply
	case m.InboxReasonOpen():
		return CockpitLayerReason
	case st.selectEditing:
		return CockpitLayerSelect
	case ck.groupMenu:
		return CockpitLayerGroupMenu
	case ck.ship != nil:
		return CockpitLayerShip
	case m.InboxSnoozePicking():
		return CockpitLayerSnooze
	case m.InboxAgentsSheetOpen():
		return CockpitLayerBulkSheet
	}
	return CockpitBase
}

// oneKey reports whether layer l takes the next key and closes on any other
// key or on a click outside it.
func (l CockpitLayer) oneKey() bool {
	switch l {
	case CockpitLayerConfirm, CockpitLayerSelect, CockpitLayerGroupMenu, CockpitLayerShip, CockpitLayerSnooze, CockpitLayerBulkSheet:
		return true
	}
	return false
}

// transient reports whether layer l draws what the frame cache key does not
// hold, so a frame with it on top is drawn afresh. The others are in the key
// (the reply's draft, the thread under copy mode) or drawn by the frame
// around them.
func (l CockpitLayer) transient() bool {
	switch l {
	case CockpitLayerImage, CockpitLayerSpawn, CockpitLayerNewCard, CockpitLayerConfirm, CockpitLayerPeek,
		CockpitLayerReason, CockpitLayerSelect, CockpitLayerSnooze, CockpitLayerBulkSheet:
		return true
	}
	return false
}

// cockpitOneKeyOpen reports whether one-key layer l holds state now, on top
// or not.
func (m *OS) cockpitOneKeyOpen(l CockpitLayer) bool {
	st := &m.Inbox
	switch l {
	case CockpitLayerConfirm:
		return st.approvals.confirm != nil
	case CockpitLayerSelect:
		return st.selectEditing
	case CockpitLayerGroupMenu:
		return st.ck.groupMenu
	case CockpitLayerShip:
		return st.ck.ship != nil
	case CockpitLayerSnooze:
		return st.life.pickFor != ""
	case CockpitLayerBulkSheet:
		return st.agents.sheet != nil
	}
	return false
}

// cockpitCloseOneKey closes one-key layer l, as a key other than its own
// does.
func (m *OS) cockpitCloseOneKey(l CockpitLayer) {
	st := &m.Inbox
	switch l {
	case CockpitLayerConfirm:
		st.approvals.confirm = nil
	case CockpitLayerSelect:
		m.InboxSelectCancel()
	case CockpitLayerGroupMenu:
		st.ck.groupMenu = false
	case CockpitLayerShip:
		st.ck.ship = nil
	case CockpitLayerSnooze:
		st.life.pickFor, st.life.closeAfterPick = "", false
	case CockpitLayerBulkSheet:
		st.agents.sheet = nil
	}
	m.cockpitTouch()
}

// ownsAction reports whether a button with action is one of layer l's own
// controls: a click on it goes on, where a click elsewhere dismisses l. A
// confirm's are the card's answer buttons (a second click answers), the
// group menu's the Group button, and the search line's the search field.
func (l CockpitLayer) ownsAction(action string) bool {
	switch l {
	case CockpitLayerConfirm:
		return action == "a" || action == "A" || action == "r"
	case CockpitLayerGroupMenu:
		return action == "group"
	case CockpitLayerSelect:
		return action == "search"
	}
	return false
}

// cockpitClickDismisses closes the one-key menu on top when a click at x, y
// lands off it, and reports whether it did: the click is then spent.
func (m *OS) cockpitClickDismisses(x, y int) bool {
	top := m.CockpitTop()
	if top == CockpitLayerPeek {
		// The peek reads one agent's prompt and has no controls a click
		// works: a click anywhere closes it and does nothing else, so a
		// click on another agent's row never leaves the peek of the first
		// answering the digits.
		m.InboxPeekBack()
		m.cockpitTouch()
		m.cockpitLayerSettle("click off the peek")
		return true
	}
	if !top.oneKey() {
		return false
	}
	for _, h := range m.Inbox.ck.hits {
		if x >= h.x0 && x < h.x1 && y >= h.y0 && y < h.y1 && h.kind == ckHitAction && top.ownsAction(h.action) {
			return false
		}
	}
	m.cockpitCloseOneKey(top)
	m.cockpitLayerSettle("click outside it")
	return true
}

// CockpitLayerCheck runs after every message. A one-key menu that a newer
// layer covers closes, so the key bar and the next key never name two
// questions. A change of the top layer is logged at verbose level. When no
// layer is open it is a few pointer checks.
func (m *OS) CockpitLayerCheck() {
	m.cockpitLayerSettle("")
}

// cockpitLayerSettle closes the covered one-key menus and logs a change of
// the top layer, with why when the caller knows it.
func (m *OS) cockpitLayerSettle(why string) {
	top := m.CockpitTop()
	if top != CockpitBase {
		for l := top + 1; l < cockpitLayers; l++ {
			if l.oneKey() && m.cockpitOneKeyOpen(l) {
				m.cockpitCloseOneKey(l)
			}
		}
	}
	ck := &m.Inbox.ck
	if top == ck.lastTop {
		return
	}
	if verboseLog() {
		if why == "" {
			why = "a message"
		}
		m.LogInfo("agents view layer: %s to %s (%s)", cockpitLayerNames[ck.lastTop], cockpitLayerNames[top], why)
	}
	ck.lastTop = top
}
