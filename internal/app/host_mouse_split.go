package app

import (
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	uv "github.com/charmbracelet/ultraviolet"
)

// SGR mouse reports at a negative position.
//
// A terminal in SGR-pixel mode (1016, see host_pixel_mouse.go) does not clamp
// the pointer to its grid. Ghostty reports a pointer in the window padding
// left of the first column, or past the window during a drag, at a negative
// x, and one above the first row at a negative y. A drag that snaps a pane to
// the left edge puts the pointer exactly there.
//
// The input decoder reads CSI parameters as digits only. At the minus sign it
// gives up on the sequence: it hands on the bytes up to the sign as an
// UnknownEvent and decodes the rest as typed keys. "\x1b[<32;-5;328M" becomes
// an unknown "\x1b[<32;-" and the keys 5 ; 3 2 8 M. In window management mode
// those keys are bindings: digits select panes, M restores every minimized
// pane, and the release ends in m, which minimizes the focused pane. Every
// motion past the edge fired them, and the release that should have ended the
// drag never arrived as one.
//
// rejoinSplitMouse puts such a report back together in the filter, before
// anything reads keys or mouse events. It holds the unknown head, takes the
// keys that can only be the rest of the report, and on the final M or m
// decodes the whole report again with each negative position clamped to the
// grid's first column or row.
//
// The reader in uv holds an unknown sequence until its escape timeout (50 ms)
// runs out, and then emits the head and the keys after it back to back. So
// the rest of a report normally arrives at once. The head can also arrive
// alone: a report split between two reads by more than the timeout, which a
// slow link can do, ends the head before the minus sign. The head is then
// digits and separators only, and the keys start with the sign.
//
// When a key that cannot be part of the report ends the hold, the keys held
// so far are typed after all, in order, before that key. They were the
// person's. A report longer than any terminal sends is dropped whole, up to
// its final byte, so none of it is typed.

// splitMouseHold bounds how long a head waits for the rest of its report. A
// head that waits longer was not a split report, and the keys after it are the
// person's.
const splitMouseHold = time.Second

// splitMouseMax bounds the held report. A real one is a prefix, three numbers
// of a few digits each, two separators and a final byte.
const splitMouseMax = 40

// splitMouseReport is a mouse report the decoder split, held until the rest of
// it arrives.
type splitMouseReport struct {
	buf  []byte
	keys []tea.KeyPressMsg
	at   time.Time
	// dropping says the report outgrew splitMouseMax. Its keys are swallowed
	// up to its final byte.
	dropping bool
}

// reset ends the hold and forgets what it held.
func (sp *splitMouseReport) reset() {
	sp.buf, sp.keys, sp.dropping = sp.buf[:0], sp.keys[:0], false
}

// holding reports whether a head is waiting for the rest of its report.
func (sp *splitMouseReport) holding() bool {
	return len(sp.buf) > 0 || sp.dropping
}

// rejoinSplitMouse holds the pieces of a split SGR mouse report and returns
// the report as one mouse message when its final byte arrives. It returns nil
// for a piece it holds, and msg unchanged for anything else.
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
		// A terminal reporting key releases does it for real keys only, and
		// the pieces of a split report were never keys.
	case tea.MouseMsg, tea.PasteMsg, tea.PasteStartMsg:
		sp.reset()
	}
	return msg
}

// giveBack ends a hold that was not a report. The keys it held are delivered
// in order, then msg.
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

// splitMouseHead reports whether s is what the decoder hands on when an SGR
// mouse report breaks: the prefix, then digits and separators, then the
// minus sign that stopped the decoder or nothing when a read ended there.
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

// clampedSGRMouse decodes a whole SGR mouse report with each position below
// the grid's first cell clamped to it. It returns nil for a report that does
// not parse, so a malformed one is dropped rather than typed.
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
	// The wire is 1-based, so 1 is the first column or row.
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
