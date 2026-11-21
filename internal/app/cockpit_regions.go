package app

import (
	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
)

type ckRegion uint8

const (
	ckRegionList ckRegion = iota
	ckRegionFocus
	ckRegionReply
	ckRegionSide
	ckRegions
)

var ckRegionNames = [ckRegions]string{"List", "Focus", "Reply", "Side"}

type ckRegionBox struct {
	x0, x1, y0, y1 int
}

func (b ckRegionBox) shown() bool { return b.x1 > b.x0 }

func (b ckRegionBox) has(x, y int) bool {
	return x >= b.x0 && x < b.x1 && y >= b.y0 && y < b.y1
}

// CockpitRegion is the region that has the keys: the reply while its editor
// is open, else the chosen one while the frame draws it, else the list.
func (m *OS) CockpitRegion() ckRegion {
	if m.InboxReplyOpen() {
		return ckRegionReply
	}
	ck := &m.Inbox.ck
	if ck.region != ckRegionList && ck.region != ckRegionReply && ck.regionBox[ck.region].shown() {
		return ck.region
	}
	return ckRegionList
}

func (m *OS) cockpitRegionShown(r ckRegion) bool {
	ck := &m.Inbox.ck
	switch r {
	case ckRegionList:
		return true
	case ckRegionReply:
		if !ck.regionBox[ckRegionFocus].shown() {
			return false
		}
		_, held := m.cockpitHeldItem()
		return !held
	}
	return ck.regionBox[r].shown()
}

// CockpitStepRegion moves the keys to the next region (d > 0) or the one
// before (d < 0) that the frame draws. Stepping onto the reply opens its
// editor; stepping off it closes the editor and keeps the draft, as esc does.
func (m *OS) CockpitStepRegion(d int) tea.Cmd {
	ck := &m.Inbox.ck
	from := m.CockpitRegion()
	to := from
	for range ckRegions {
		to = ckRegion((int(to) + d + int(ckRegions)) % int(ckRegions))
		if m.cockpitRegionShown(to) {
			break
		}
	}
	if to == from {
		return nil
	}
	if from == ckRegionReply {
		m.InboxReplyCancel()
	}
	ck.region = to
	m.cockpitTouch()
	if to == ckRegionReply {
		return m.cockpitReply()
	}
	return nil
}

func (m *OS) cockpitFocusRegionAt(x, y int) {
	ck := &m.Inbox.ck
	for _, r := range [...]ckRegion{ckRegionFocus, ckRegionSide, ckRegionList} {
		if ck.regionBox[r].has(x, y) {
			if ck.region != r {
				ck.region = r
				m.cockpitTouch()
			}
			return
		}
	}
}

// CockpitEscape is esc in the view, one step back each press: leave the
// focus on a narrow screen, else give the keys back to the list, else clear
// the search and the filter, else close the view. A sheet or a field that is
// open closes on its own esc before this runs. q closes at once.
func (m *OS) CockpitEscape() tea.Cmd {
	st := &m.Inbox
	ck := &st.ck
	switch step, _ := m.cockpitEscapeStep(); step {
	case ckEscUnzoom:
		m.cockpitUnzoom()
		return nil
	case ckEscBack:
		return m.CockpitOpenFocus(false)
	case ckEscList:
		ck.region = ckRegionList
		m.cockpitTouch()
		return nil
	case ckEscClear:
		m.cockpitShowAll()
		return nil
	}
	m.CloseInbox()
	return nil
}

func (m *OS) cockpitShowAll() {
	st := &m.Inbox
	ck := &st.ck
	m.setInboxSelector(nil)
	st.Filter, st.agents.query, ck.needsOnly, ck.chip = "", "", false, 0
	ck.scroll, ck.follow = 0, true
	m.cockpitTouchRows()
	m.cockpitRows()
	m.cockpitClamp()
	m.cockpitSync()
}

type ckEscStep uint8

const (
	ckEscClose ckEscStep = iota
	ckEscUnzoom
	ckEscBack
	ckEscList
	ckEscClear
)

func (m *OS) cockpitEscapeStep() (ckEscStep, string) {
	switch {
	case m.Inbox.ck.zoom:
		return ckEscUnzoom, "unzoom"
	case m.Inbox.threads.open:
		return ckEscBack, "back"
	case m.CockpitRegion() != ckRegionList:
		return ckEscList, "list"
	case m.Inbox.ck.chip > 0 && !m.cockpitSearched():
		return ckEscClear, "clear filter"
	case m.cockpitNarrowed():
		return ckEscClear, "clear search"
	}
	return ckEscClose, "close"
}

func (m *OS) cockpitNarrowed() bool {
	st := &m.Inbox
	return st.Select != "" || st.Filter != "" || st.agents.query != "" || st.ck.needsOnly || st.ck.chip > 0
}

func (m *OS) cockpitSearched() bool {
	return m.Inbox.Select != "" || m.Inbox.agents.query != ""
}

func (m *OS) cockpitEscapeLabel() string {
	_, label := m.cockpitEscapeStep()
	return label
}

// CockpitNextNeeds puts the cursor on the next agent in Needs you after the
// cursor, from the top again after the last. It reports false when nothing
// needs the person.
func (m *OS) CockpitNextNeeds() bool {
	rows := m.cockpitRows()
	ck := &m.Inbox.ck
	n := len(rows)
	for i := 1; i <= n; i++ {
		at := (ck.idx + i) % n
		if r := rows[at]; r.group == ckNeeds && r.cursorable() && r.kind != ckRowHead {
			m.cockpitSetCursor(at)
			ck.pending = ""
			m.cockpitTouch()
			m.cockpitReplyFollow()
			m.cockpitSync()
			return true
		}
	}
	return false
}

func (m *OS) cockpitRegionKey(action string) (tea.Cmd, bool) {
	switch m.CockpitRegion() {
	case ckRegionFocus:
		return m.cockpitFocusKey(action)
	case ckRegionSide:
		if action == config.ActionInboxGo {
			return m.InboxAgentsViewPane(), true
		}
	}
	return nil, false
}

func (m *OS) cockpitFocusKey(action string) (tea.Cmd, bool) {
	if action == config.ActionInboxResume && m.CockpitCopyStart() {
		return nil, true
	}
	if m.inboxPlanMove(action, true) {
		return nil, true
	}
	t := m.cockpitThread()
	if t == nil || len(t.entries) == 0 {
		switch action {
		case config.ActionInboxUp, config.ActionInboxDown, config.ActionInboxPageUp, config.ActionInboxPageDown,
			config.ActionInboxFirst, config.ActionInboxLast:
			return nil, true
		}
		return nil, false
	}
	start, end := t.follow.Window()
	page := max(end-start-2, 1)
	switch action {
	case config.ActionInboxDown:
		t.follow.Scroll(-1)
	case config.ActionInboxUp:
		t.follow.Scroll(1)
	case config.ActionInboxPageDown:
		t.follow.Scroll(-page)
	case config.ActionInboxPageUp:
		t.follow.Scroll(page)
	case config.ActionInboxFirst:
		threadKeepAll(t)
		t.follow.Scroll(1 << 20)
	case config.ActionInboxLast:
		t.follow.Bottom()
	default:
		return nil, false
	}
	m.cockpitTouch()
	return nil, true
}

// CockpitKeyAction runs one of the view's own actions and drops whether it
// was handled, for a caller that already knows it is one.
func (m *OS) CockpitKeyAction(action string) tea.Cmd {
	cmd, _ := m.CockpitKey(action)
	return cmd
}

func (m *OS) cockpitEscHints() []overlay.Hint {
	if hints := m.keyHints(config.ActionInboxEscape, m.cockpitEscapeLabel()); len(hints) > 0 {
		return hints
	}
	return m.keyHints(config.ActionInboxClose, "close")
}

func (m *OS) cockpitFocusHints() []overlay.Hint {
	pair := func(a, b, label string) []overlay.Hint {
		ka, kb := m.inboxKey(a), m.inboxKey(b)
		if ka == "" || kb == "" {
			return nil
		}
		return []overlay.Hint{{Key: ka + " " + kb, Label: label}}
	}
	var hints []overlay.Hint
	hints = append(hints, pair(config.ActionInboxDown, config.ActionInboxUp, "scroll")...)
	hints = append(hints, pair(config.ActionInboxFirst, config.ActionInboxLast, "ends")...)
	hints = append(hints, pair(config.ActionInboxPromptPrev, config.ActionInboxPromptNext, "prompts")...)
	hints = append(hints, pair(config.ActionInboxTabPrev, config.ActionInboxTabNext, "tabs")...)
	hints = append(hints, m.keyHints(config.ActionInboxExpand, "expand")...)
	if m.cockpitTab() == "Thread" {
		hints = append(hints, m.keyHints(config.ActionInboxCodeWrap, "wrap code")...)
	}
	if m.cockpitTab() == "Thread" {
		hints = append(hints, m.keyHints(config.ActionInboxResume, "copy")...)
	}
	hints = append(hints, m.keyHints(config.ActionInboxWrite, "reply")...)
	hints = append(hints, m.keyHints(config.ActionInboxRegionNext, "next region")...)
	hints = append(hints, overlay.Hint{Key: "?", Label: "all keys"})
	return append(hints, m.cockpitEscHints()...)
}

func (m *OS) cockpitSideHints() []overlay.Hint {
	hints := m.keyHints(config.ActionInboxGo, "open pane", config.ActionInboxRegionNext, "next region")
	hints = append(hints, overlay.Hint{Key: "?", Label: "all keys"})
	return append(hints, m.cockpitEscHints()...)
}

func (m *OS) cockpitNeedsCount() int {
	n := 0
	for _, r := range m.Inbox.ck.rows {
		if r.group == ckNeeds && r.cursorable() && r.kind != ckRowHead {
			n++
		}
	}
	return n
}
