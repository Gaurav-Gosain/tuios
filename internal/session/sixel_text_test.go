package session

import (
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/vt"
)

// A two-row sixel at 10x20 cells: 30x40 pixels.
const sixelTextImage = "\x1bPq\"1;1;30;40#1;2;100;0;0!30~-!30~-!30~-!30~-!30~-!30~-!30~\x1b\\"

// TestSixelCellsLeaveAsBlanks: a pane that drew a sixel image holds marker
// cells, and none of the ways its text leaves the daemon may carry them: the
// plain and styled captures (capture-pane, MCP, agent reads), the screen tail
// (agent screen reads, the harness transcript), and the saved history.
func TestSixelCellsLeaveAsBlanks(t *testing.T) {
	term := vt.NewWithScrollback(20, 4, 50)
	term.SetCellSize(10, 20)
	_, _ = term.Write([]byte("before\r\n" + sixelTextImage + "after\r\n"))
	for range 6 {
		_, _ = term.Write([]byte("more\r\n"))
	}
	_, _ = term.Write([]byte(sixelTextImage))
	p := &PTY{terminal: term}

	for name, text := range map[string]string{
		"plain capture":  p.CaptureContent(true, false),
		"styled capture": p.CaptureContent(true, true),
		"tail":           strings.Join(term.TailText(4), "\n"),
	} {
		if strings.Contains(text, vt.SixelMarkerLead) {
			t.Errorf("%s carries image markers", name)
		}
	}
	rows := captureHistoryRows(term, 50)
	for _, line := range append(rows.screen, rows.history...) {
		for _, c := range line {
			if vt.IsSixelMarker(c.Content) {
				t.Fatalf("the saved history holds an image marker")
			}
		}
	}
}
