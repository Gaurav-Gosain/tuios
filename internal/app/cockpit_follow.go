package app

import (
	"slices"
	"strconv"

	"github.com/Gaurav-Gosain/tuios/internal/transcriptview"
)

// The thread's scroll: it follows the newest line, puts a new prompt at the
// top, and holds still once the person scrolls up (transcriptview.Follow has
// the rules). A held thread says how many entries arrived under it, and G
// shows them. The ruler in the thread's right column marks each prompt, and
// { and } jump between them. Nothing here runs on a timer: the view moves on
// a frame drawn for a delivery, a key or a send.

// threadLayout is what Follow needs of a thread drawn as lines lines with
// room rows to show them.
func threadLayout(t *agentThread, lines, room int) transcriptview.Layout {
	l := transcriptview.Layout{Lines: lines, Room: room, Taken: t.taken, PromptAt: -1}
	for _, en := range slices.Backward(t.entries) {
		if en.Kind == "text" && en.Role == "user" {
			l.Prompt = en.ID
			break
		}
	}
	if n := len(t.prompts); n > 0 {
		l.PromptAt = t.prompts[n-1]
	}
	return l
}

// CockpitThreadBottom shows the newest lines of the thread on screen and
// follows it again. It reports whether the thread held still above them.
func (m *OS) CockpitThreadBottom() bool {
	t := m.cockpitThread()
	if t == nil || !m.threadShown() || !t.follow.Bottom() {
		return false
	}
	m.cockpitTouch()
	return true
}

// CockpitPromptJump puts the next prompt of the thread on screen at the top
// of the thread, or the previous one.
func (m *OS) CockpitPromptJump(next bool) {
	t := m.cockpitThread()
	if t == nil || !m.threadShown() {
		return
	}
	d := -1
	if next {
		d = 1
	}
	if t.follow.Jump(t.prompts, d) {
		m.cockpitTouch()
	}
}

// threadFollowOnSend makes the thread of the agent a reply went to follow
// again, so the prompt goes to the top when its transcript shows it.
func (m *OS) threadFollowOnSend(host, sess, window string) {
	if t := m.Inbox.threads.byKey[host+"\x00"+sess+"\x00"+window]; t != nil {
		t.follow.Bottom()
	}
}

// drawThreadUnseen draws the line that says how many entries arrived under a
// held thread, on its last row. A click on it shows them.
func (m *OS) drawThreadUnseen(c *ckCanvas, l ckLook, t *agentThread, x, w, y, top int) {
	n := t.follow.Unseen
	if !t.follow.Held() || n <= 0 {
		return
	}
	text := strconv.Itoa(n) + " new entries "
	if n == 1 {
		text = "1 new entry "
	}
	arrow := "↓"
	if l.ascii {
		arrow = "v"
	}
	c.fill(x, y, w, 1, l.canvas)
	x1 := c.text(x+2, y, x+w, ckSeg{t: text + arrow, fg: l.pal.AccentBright, attrs: ckBold})
	m.Inbox.ck.hits = append(m.Inbox.ck.hits, ckHit{x0: x, y0: top + y, x1: x1, y1: top + y + 1, kind: ckHitAction, action: "follow"})
}

// drawThreadRuler draws the thread's ruler in column x, rows y to y+rows,
// for a thread of lines lines shown from line start: where the view is, and
// a mark for each prompt. A thread that fits has none.
func drawThreadRuler(c *ckCanvas, l ckLook, t *agentThread, x, y, rows, lines, start int) {
	if lines <= rows || rows < 2 {
		return
	}
	thumb, mark := "┃", "━"
	if l.ascii {
		thumb, mark = "|", "-"
	}
	if top, size, ok := sidebarScrollThumb(start, lines, rows); ok {
		for r := top; r < top+size; r++ {
			c.text(x, y+r, x+1, ckSeg{t: thumb, fg: l.pal.FgMute})
		}
	}
	for _, r := range transcriptview.RulerMarks(t.prompts, lines, rows) {
		c.text(x, y+r, x+1, ckSeg{t: mark, fg: l.pal.AccentBright, attrs: ckBold})
	}
}
