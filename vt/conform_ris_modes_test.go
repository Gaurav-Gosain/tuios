package vt_test

import (
	"fmt"
	"testing"
)

// The initial, changed and reset reports for these modes were observed from
// xterm 379 on Debian 12 with its default resources. Each case starts in a
// fresh terminal: mouse modes in particular are mutually exclusive in xterm.
func TestConform_RISModeReports(t *testing.T) {
	for _, tc := range []struct {
		mode    string
		change  string
		changed int
		reset   int
	}{
		{"?1", "h", 1, 2}, {"?6", "h", 1, 2},
		{"?7", "l", 2, 1}, {"?9", "h", 1, 2},
		{"?25", "l", 2, 1}, {"?47", "h", 1, 2},
		{"?66", "h", 1, 2}, {"?69", "h", 1, 2},
		{"?1000", "h", 1, 2}, {"?1001", "h", 1, 2},
		{"?1002", "h", 1, 2}, {"?1003", "h", 1, 2},
		{"?1004", "h", 1, 2}, {"?1005", "h", 1, 2},
		{"?1006", "h", 1, 2}, {"?1015", "h", 1, 2},
		{"?1016", "h", 1, 2}, {"?1047", "h", 1, 2},
		{"?1049", "h", 1, 2}, {"?2004", "h", 1, 2},
		{"4", "h", 1, 2}, {"20", "h", 1, 2},
	} {
		t.Run(tc.mode, func(t *testing.T) {
			change := fmt.Sprintf("\x1b[%s%s", tc.mode, tc.change)
			query := fmt.Sprintf("\x1b[%s$p", tc.mode)
			wantChanged := fmt.Sprintf("\x1b[%s;%d$y", tc.mode, tc.changed)
			wantReset := fmt.Sprintf("\x1b[%s;%d$y", tc.mode, tc.reset)
			if got := reply(t, 80, 24, change+query); got != wantChanged {
				t.Errorf("after change: got %q, want %q", got, wantChanged)
			}
			if got := reply(t, 80, 24, change+"\x1bc"+query); got != wantReset {
				t.Errorf("after RIS: got %q, want %q", got, wantReset)
			}
		})
	}
}
