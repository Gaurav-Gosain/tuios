package app

import (
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/Gaurav-Gosain/tuios/internal/plural"
)

const (
	ckCodeWheelStep = 3
	ckCodeKeyStep   = 8
)

type ckCodeView struct {
	t           *agentThread
	x0, x1, top int
	start, end  int
}

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

func (m *OS) cockpitCodeLowest(wide bool) (*agentThread, string) {
	v := m.Inbox.ck.code
	if v.t == nil {
		return nil, ""
	}
	for i := min(v.end, len(v.t.lines)) - 1; i >= v.start; i-- {
		if key := v.t.lines[i].code; key != "" && (!wide || v.t.codeMax[key] > 0) {
			return v.t, key
		}
	}
	return nil, ""
}

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
	t, key := m.cockpitCodeLowest(true)
	if key == "" {
		m.ShowNotification("No code block on screen is wider than the thread.", "info", m.Settings.NotificationDuration)
		return
	}
	if t.scrollCode(key, dir*ckCodeKeyStep) {
		m.cockpitTouch()
	}
}

const ckCodeCopyHit = "code-copy:"

type ckCode struct {
	text string
	open bool
}

// CockpitCodeCopy is C: the lowest code block on screen goes to the
// clipboard.
func (m *OS) CockpitCodeCopy() tea.Cmd {
	t, key := m.cockpitCodeLowest(false)
	if key == "" {
		m.ShowNotification("No code block on screen.", "info", m.Settings.NotificationDuration)
		return nil
	}
	return m.cockpitCopyCode(t, key)
}

func (m *OS) cockpitCopyCode(t *agentThread, key string) tea.Cmd {
	c, ok := t.code[key]
	if !ok {
		return nil
	}
	n := strings.Count(c.text, "\n") + 1
	word := "Copied " + plural.CountAs(n, "line", "lines") + "."
	if c.open && t.entryCut(key[:max(strings.LastIndexByte(key, '/'), 0)]) {
		word += " The thread has only part of this block."
	}
	m.ShowNotification(word, "success", m.Settings.NotificationDuration)
	if m.Settings.CopyCommand != "" {
		return m.Yank(c.text)
	}
	return tea.Batch(tea.SetClipboard(c.text), m.SaveToPasteBuffers(c.text))
}

func (t *agentThread) entryCut(id string) bool {
	for i := range t.entries {
		if t.entries[i].ID == id {
			return t.entries[i].Truncated
		}
	}
	return false
}
