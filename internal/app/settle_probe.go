package app

import (
	"os"
	"strconv"
	"strings"
	"sync"

	tea "charm.land/bubbletea/v2"
	uv "github.com/charmbracelet/ultraviolet"

	"github.com/Gaurav-Gosain/tuios/internal/testknob"
)

// The settle probe is how a test waits for the client to catch up, rather
// than for a guess of how long that takes. The test writes an APC to the
// client's input,
//
//	ESC _ tuios-settle;ID ESC \
//
// which the client reads in order with the keys sent before it. With the
// settle_marks knob set to a file, the client draws the output its panes
// have read so far, outside the frame pacing, and answers the probe once that
// frame is written: it appends one line,
//
//	ID SENT BUSY SCREEN
//
// SENT is the number of bytes the client had written to its terminal by
// then, so the test can wait until it has read as many. BUSY is true while
// an animation runs, and the test probes again. Pane output that arrives
// after the probe does not make the client busy: a pane that prints without
// end would keep it busy for good. SCREEN is false for a client without its own screen, whose frames
// Bubble Tea's renderer writes later, on its own goroutine.
//
// Without the knob the probe is an unknown APC like any other, and the
// client does nothing for it. Nothing here runs at idle.

// settleProbeTag starts the payload of a settle probe.
const settleProbeTag = "tuios-settle;"

// settleMarks is the file the answers go to, "" when the knob is not set.
var settleMarks = sync.OnceValue(func() string { return testknob.String("settle_marks") })

// announceSettleProbes creates the marks file, which tells a test that this
// client answers probes. An older client never creates it, and the test
// pauses for it as it always did. BindProgram calls it.
func announceSettleProbes() {
	if p := settleMarks(); p != "" {
		if f, err := os.OpenFile(p, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600); err == nil {
			_ = f.Close()
		}
	}
}

// takeSettleProbe reports whether msg is a settle probe, and keeps its ID to
// answer after the next flush.
func (m *OS) takeSettleProbe(msg tea.Msg) bool {
	e, ok := msg.(uv.UnknownApcEvent)
	if !ok || settleMarks() == "" {
		return false
	}
	_, id, found := strings.Cut(string(e), settleProbeTag)
	if !found {
		return false
	}
	id = strings.TrimSuffix(id, "\x1b\\")
	m.settleOwed = append(m.settleOwed, id)
	return true
}

// answerSettleProbes writes the answer to each probe taken since the last
// flush. handleFlush calls it after the frame is written.
func (m *OS) answerSettleProbes() {
	if len(m.settleOwed) == 0 {
		return
	}
	busy := m.HasActiveAnimations()
	sent := testknob.Written()
	var b []byte
	for _, id := range m.settleOwed {
		b = append(b, id...)
		b = append(b, ' ')
		b = strconv.AppendInt(b, sent, 10)
		b = append(b, ' ')
		b = strconv.AppendBool(b, busy)
		b = append(b, ' ')
		b = strconv.AppendBool(b, m.screen != nil)
		b = append(b, '\n')
	}
	m.settleOwed = m.settleOwed[:0]
	f, err := os.OpenFile(settleMarks(), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	_, _ = f.Write(b)
	_ = f.Close()
}
