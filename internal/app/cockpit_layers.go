package app

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
	CockpitLayerMenu
	CockpitLayerShip
	CockpitLayerSnooze
	CockpitLayerBulkSheet
	cockpitLayers
)

var cockpitLayerNames = [cockpitLayers]string{
	"base", "copy", "image", "spawn", "compare", "artifact", "new card", "confirm", "peek",
	"reply", "reason", "select", "menu", "ship", "snooze", "bulk sheet",
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
	case ck.menu != nil:
		return CockpitLayerMenu
	case ck.ship != nil:
		return CockpitLayerShip
	case m.InboxSnoozePicking():
		return CockpitLayerSnooze
	case m.InboxAgentsSheetOpen():
		return CockpitLayerBulkSheet
	}
	return CockpitBase
}

func (l CockpitLayer) oneKey() bool {
	switch l {
	case CockpitLayerConfirm, CockpitLayerSelect, CockpitLayerMenu, CockpitLayerShip, CockpitLayerSnooze, CockpitLayerBulkSheet:
		return true
	}
	return false
}

func (l CockpitLayer) transient() bool {
	switch l {
	case CockpitLayerImage, CockpitLayerSpawn, CockpitLayerNewCard, CockpitLayerConfirm, CockpitLayerPeek,
		CockpitLayerMenu, CockpitLayerReason, CockpitLayerSelect, CockpitLayerSnooze, CockpitLayerBulkSheet:
		return true
	}
	return false
}

func (m *OS) cockpitOneKeyOpen(l CockpitLayer) bool {
	st := &m.Inbox
	switch l {
	case CockpitLayerConfirm:
		return st.approvals.answer.step == answerConfirm
	case CockpitLayerSelect:
		return st.selectEditing
	case CockpitLayerMenu:
		return st.ck.menu != nil
	case CockpitLayerShip:
		return st.ck.ship != nil
	case CockpitLayerSnooze:
		return st.life.pickFor != ""
	case CockpitLayerBulkSheet:
		return st.agents.sheet != nil
	}
	return false
}

func (m *OS) cockpitCloseOneKey(l CockpitLayer) {
	st := &m.Inbox
	switch l {
	case CockpitLayerConfirm:
		m.answerEnd(answerConfirm, "closed by a click or a newer layer")
	case CockpitLayerSelect:
		m.InboxSelectCancel()
	case CockpitLayerMenu:
		st.ck.menu = nil
	case CockpitLayerShip:
		st.ck.ship = nil
	case CockpitLayerSnooze:
		st.life.pickFor, st.life.closeAfterPick = "", false
	case CockpitLayerBulkSheet:
		st.agents.sheet = nil
	}
	m.cockpitTouch()
}

func (l CockpitLayer) ownsAction(action string) bool {
	switch l {
	case CockpitLayerConfirm:
		return action == "a" || action == "A" || action == "X"
	case CockpitLayerMenu:
		return ckMenuControls[action]
	case CockpitLayerSelect:
		return action == "search"
	}
	return false
}

func (m *OS) cockpitClickDismisses(x, y int) bool {
	top := m.CockpitTop()
	if top == CockpitLayerPeek {
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
