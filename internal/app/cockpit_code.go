package app

import (
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
)

// A code line wider than the thread is cut at the block's edge. The block
// scrolls sideways under a sideways wheel or shift and the wheel, and under
// ( and ) for the lowest such block on screen. Its lines move together, and
// its gutter, ground and edge stay. How far each block is scrolled is the
// thread's, by the block's key, so it holds while the thread grows.

// ckCodeWheelStep and ckCodeKeyStep are how far one wheel notch and one key
// move a code block, in cells.
const (
	ckCodeWheelStep = 3
	ckCodeKeyStep   = 8
)

// ckCodeView is where a frame drew a thread's lines: line i of the thread
// is on screen row top+i, for start <= i < end, from column x0 to x1.
type ckCodeView struct {
	t           *agentThread
	x0, x1, top int
	start, end  int
}

// cockpitCodeAt is the thread and the key of the code block drawn at x, y,
// "" for none.
func (m *OS) cockpitCodeAt(x, y int) (*agentThread, string) {
	v := m.Inbox.ck.code
	if v.t == nil || x < v.x0 || x >= v.x1 {
		return nil, ""
	}
	i := y - v.top
	if i < v.start || i >= v.end || i >= len(v.t.lines) {
		return nil, ""
	}
	return v.t, v.t.lines[i].code
}

// cockpitCodeLowest is the thread and the key of the lowest code block on
// screen that is wider than the thread, "" for none.
func (m *OS) cockpitCodeLowest() (*agentThread, string) {
	v := m.Inbox.ck.code
	if v.t == nil {
		return nil, ""
	}
	for i := min(v.end, len(v.t.lines)) - 1; i >= v.start; i-- {
		if key := v.t.lines[i].code; key != "" && v.t.codeMax[key] > 0 {
			return v.t, key
		}
	}
	return nil, ""
}

// scrollCode moves the code block key dx cells sideways, held to its
// bounds, and reports whether it moved.
func (t *agentThread) scrollCode(key string, dx int) bool {
	x := min(max(t.codeX[key]+dx, 0), t.codeMax[key])
	if x == t.codeX[key] {
		return false
	}
	if t.codeX == nil {
		t.codeX = make(map[string]int)
	}
	if t.codeGen == nil {
		t.codeGen = make(map[string]int)
	}
	t.codeX[key] = x
	id := key[:max(strings.LastIndexByte(key, '/'), 0)]
	t.codeGen[id]++
	t.gen++
	return true
}

// CockpitCodeWheel scrolls the code block under the pointer sideways, on a
// sideways wheel or shift and the wheel, and reports whether it took the
// event. A sideways wheel in a gesture that is mostly up and down is drift:
// it is taken and does nothing, so the block does not creep while the
// thread scrolls.
func (m *OS) CockpitCodeWheel(x, y int, button tea.MouseButton, shift bool, now time.Time) bool {
	if !m.ShowInbox || m.inboxCovered() {
		return false
	}
	t, key := m.cockpitCodeAt(x, y)
	if key == "" {
		return false
	}
	horizontal := button == tea.MouseWheelLeft || button == tea.MouseWheelRight
	if !shift && !m.WheelAxisAccepts(horizontal, now) {
		return horizontal
	}
	if !horizontal && !shift {
		return false
	}
	dx := ckCodeWheelStep
	if button == tea.MouseWheelLeft || button == tea.MouseWheelUp {
		dx = -dx
	}
	if t.scrollCode(key, dx) {
		m.cockpitTouch()
	}
	return true
}

// CockpitCodeScroll is ( and ): the lowest code block on screen that is
// wider than the thread moves left (dir -1) or right.
func (m *OS) CockpitCodeScroll(dir int) {
	t, key := m.cockpitCodeLowest()
	if key == "" {
		m.ShowNotification("No code block on screen is wider than the thread.", "info", m.Settings.NotificationDuration)
		return
	}
	if t.scrollCode(key, dir*ckCodeKeyStep) {
		m.cockpitTouch()
	}
}
