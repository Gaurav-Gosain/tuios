package app

import (
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	uv "github.com/charmbracelet/ultraviolet"
)

const splitMouseHold = time.Second

const splitMouseMax = 40

type splitMouseReport struct {
	buf      []byte
	keys     []tea.KeyPressMsg
	at       time.Time
	dropping bool
}

func (sp *splitMouseReport) reset() {
	sp.buf, sp.keys, sp.dropping = sp.buf[:0], sp.keys[:0], false
}

func (sp *splitMouseReport) holding() bool {
	return len(sp.buf) > 0 || sp.dropping
}

func (m *OS) rejoinSplitMouse(msg tea.Msg) tea.Msg {
	sp := &m.splitMouse
	switch e := msg.(type) {
	case uv.UnknownEvent:
		sp.reset()
		if splitMouseHead(string(e)) {
			sp.buf = append(sp.buf, e...)
			sp.at = time.Now()
			return nil
		}
	case tea.KeyPressMsg:
		if !sp.holding() {
			return msg
		}
		if time.Since(sp.at) > splitMouseHold || len(e.Text) != 1 {
			return sp.giveBack(msg)
		}
		switch c := e.Text[0]; {
		case c >= '0' && c <= '9', c == ';', c == '-':
			if sp.dropping {
				return nil
			}
			if len(sp.buf) >= splitMouseMax {
				sp.buf, sp.keys, sp.dropping = sp.buf[:0], sp.keys[:0], true
				return nil
			}
			sp.buf = append(sp.buf, c)
			sp.keys = append(sp.keys, e)
			return nil
		case c == 'M', c == 'm':
			if sp.dropping {
				sp.reset()
				return nil
			}
			sp.buf = append(sp.buf, c)
			joined := clampedSGRMouse(sp.buf)
			sp.reset()
			return joined
		}
		return sp.giveBack(msg)
	case tea.KeyReleaseMsg:
	case tea.MouseMsg, tea.PasteMsg, tea.PasteStartMsg:
		sp.reset()
	}
	return msg
}

func (sp *splitMouseReport) giveBack(msg tea.Msg) tea.Msg {
	if len(sp.keys) == 0 {
		sp.reset()
		return msg
	}
	cmds := make([]tea.Cmd, 0, len(sp.keys)+1)
	for _, k := range sp.keys {
		cmds = append(cmds, func() tea.Msg { return k })
	}
	cmds = append(cmds, func() tea.Msg { return msg })
	sp.reset()
	return tea.Sequence(cmds...)()
}

func splitMouseHead(s string) bool {
	rest, ok := strings.CutPrefix(s, "\x1b[<")
	if !ok {
		return false
	}
	rest = strings.TrimSuffix(rest, "-")
	for i := range len(rest) {
		if c := rest[i]; (c < '0' || c > '9') && c != ';' {
			return false
		}
	}
	return true
}

func clampedSGRMouse(report []byte) tea.Msg {
	body, ok := strings.CutPrefix(string(report), "\x1b[<")
	if !ok || len(body) < 2 {
		return nil
	}
	final := body[len(body)-1]
	params := strings.Split(body[:len(body)-1], ";")
	if len(params) != 3 {
		return nil
	}
	nums := make([]int, 3)
	for i, p := range params {
		n, err := strconv.Atoi(p)
		if err != nil {
			return nil
		}
		nums[i] = n
	}
	if nums[0] < 0 {
		return nil
	}
	x, y := max(nums[1], 1), max(nums[2], 1)
	seq := "\x1b[<" + strconv.Itoa(nums[0]) + ";" + strconv.Itoa(x) + ";" + strconv.Itoa(y) + string(final)
	var dec uv.EventDecoder
	n, ev := dec.Decode([]byte(seq))
	if n != len(seq) {
		return nil
	}
	switch e := ev.(type) {
	case uv.MouseClickEvent:
		return tea.MouseClickMsg(e)
	case uv.MouseReleaseEvent:
		return tea.MouseReleaseMsg(e)
	case uv.MouseMotionEvent:
		return tea.MouseMotionMsg(e)
	case uv.MouseWheelEvent:
		return tea.MouseWheelMsg(e)
	}
	return nil
}
