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
// settle_marks knob set to a file, the client answers the probe once the
// frame for everything before it is written: it appends one line,
//
//	ID SENT BUSY SCREEN
//
// SENT is the number of bytes the client had written to its terminal by
// then, so the test can wait until it has read as many. BUSY is true while
// an animation runs or pane output waits for its frame, and the test probes
// again. SCREEN is false for a client without its own screen, whose frames
// Bubble Tea's renderer writes later, on its own goroutine.
//
// Without the knob the probe is an unknown APC like any other, and the
// client does nothing for it. Nothing here runs at idle.

// settleProbeTag starts the payload of a settle probe.
const settleProbeTag = "tuios-settle;"

// settleMarks is the file the answers go to, "" when the knob is not set.
var settleMarks = sync.OnceValue(func() string { return testknob.String("settle_marks") })

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
	busy := m.HasActiveAnimations() || m.frameRate.dueArmed || m.anyPaneOutput()
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
