package app

import (
	"slices"
	"strconv"

	"github.com/Gaurav-Gosain/tuios/internal/transcriptview"
)

func threadLayout(t *agentThread, lines, room int) transcriptview.Layout {
	l := transcriptview.Layout{Lines: lines, Room: room, Taken: t.taken, PromptAt: -1}
	for _, en := range slices.Backward(t.entries) {
		if en.Kind == "text" && en.Role == "user" && en.Image == nil && !transcriptview.InterruptText(en.Text) {
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
	} else if !next && t.older {
		t.keep *= 2
		m.cockpitTouch()
	}
}

func (m *OS) threadFollowOnSend(host, sess, window string) {
	if t := m.Inbox.threads.byKey[host+"\x00"+sess+"\x00"+window]; t != nil {
		t.follow.Bottom()
	}
}

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

func drawThreadRuler(c *ckCanvas, l ckLook, t *agentThread, x, y, rows, lines, start int) {
	ckRuler{lines: lines, start: start, marks: t.prompts}.draw(c, l, x, y, rows)
}

type ckRuler struct {
	lines, start int
	marks        []int
	track        bool
}

const ckRulerMarksShare = 3

func (r ckRuler) shows(rows int) bool { return r.lines > rows && rows >= 2 }

func (r ckRuler) markRows(rows int) []int {
	marks := transcriptview.RulerMarks(r.marks, r.lines, rows)
	if len(marks)*ckRulerMarksShare > rows {
		return nil
	}
	return marks
}

func (r ckRuler) draw(c *ckCanvas, l ckLook, x, y, rows int) bool {
	if !r.shows(rows) {
		return false
	}
	thumb, mark, track := "┃", "━", "│"
	if l.ascii {
		thumb, mark, track = "|", "-", "|"
	}
	thumbInk := l.pal.FgMute
	if r.track {
		for i := range rows {
			c.text(x, y+i, x+1, ckSeg{t: track, fg: l.pal.FgMute})
		}
		thumbInk = l.pal.Fg
		if l.ascii {
			thumb = "#"
		}
	}
	if top, size, ok := sidebarScrollThumb(r.start, r.lines, rows); ok {
		for i := top; i < top+size; i++ {
			c.text(x, y+i, x+1, ckSeg{t: thumb, fg: thumbInk})
		}
	}
	for _, i := range r.markRows(rows) {
		c.text(x, y+i, x+1, ckSeg{t: mark, fg: l.pal.AccentBright, attrs: ckBold})
	}
	return true
}

func (r ckRuler) thumb(rows int) (top, size int) {
	top, size, _ = sidebarScrollThumb(r.start, r.lines, rows)
	return top, size
}

func (r ckRuler) startFor(top, rows int) int {
	_, size := r.thumb(rows)
	travel := rows - size
	if travel <= 0 {
		return 0
	}
	top = min(max(top, 0), travel)
	return roundDiv(top*(r.lines-rows), travel)
}
