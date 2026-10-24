package tuie2e

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// bareBells returns the offsets of every BEL in stream that is a bell of its
// own: one in the ground state, outside any escape sequence. A BEL that ends an
// OSC (the title, a notification, a hyperlink) is a terminator, not a bell, and
// a terminal does not ring for it. ESC P, ESC _, ESC ^ and ESC X strings end only
// at ST, so a BEL inside one is data.
func bareBells(stream []byte) []int {
	const (
		ground = iota
		escape
		csi
		osc
		str
	)
	var out []int
	state := ground
	for i := 0; i < len(stream); i++ {
		b := stream[i]
		switch state {
		case ground:
			switch b {
			case 0x07:
				out = append(out, i)
			case 0x1b:
				state = escape
			}
		case escape:
			switch b {
			case '[':
				state = csi
			case ']':
				state = osc
			case 'P', '_', '^', 'X':
				state = str
			default:
				// A two-byte escape, or an intermediate the next byte ends.
				if b < 0x20 || b > 0x2f {
					state = ground
				}
			}
		case csi:
			if b >= 0x40 && b <= 0x7e {
				state = ground
			}
		case osc:
			if b == 0x07 {
				state = ground
			} else if b == 0x1b && i+1 < len(stream) && stream[i+1] == '\\' {
				i++
				state = ground
			}
		case str:
			if b == 0x1b && i+1 < len(stream) && stream[i+1] == '\\' {
				i++
				state = ground
			}
		}
	}
	return out
}

// bellsSince counts the bare bells in stream at or past offset from.
func bellsSince(stream []byte, from int) int {
	n := 0
	for _, at := range bareBells(stream) {
		if at >= from {
			n++
		}
	}
	return n
}

// TestPaneBellReachesHost runs printf '\a' in a pane under each value of
// notifications.bell and reads both ends: the raw stream the host terminal
// gets, for a BEL outside any sequence, and the dock, for the bell message.
// Every mode runs against a daemon, where the pane's output reaches the
// client over the wire before the client's emulator sees the bell; forward
// also runs standalone.
func TestPaneBellReachesHost(t *testing.T) {
	for _, tc := range []struct {
		mode           string
		daemon         bool
		forward, toast bool
	}{
		{"toast", true, false, true},
		{"forward", true, true, false},
		{"both", true, true, true},
		{"off", true, false, false},
		{"forward", false, true, false},
	} {
		name := tc.mode
		if !tc.daemon {
			name += "-standalone"
		}
		t.Run(name, func(t *testing.T) {
			base := t.TempDir()
			writeConfig(t, base, fmt.Sprintf(`
[startup]
open_default_window = true
start_in_terminal_mode = true

[notifications]
duration = 30
bell = %q
`, tc.mode))
			stream := &hostStream{}
			opts := startOpts{cols: 120, rows: 30, out: stream}
			if tc.daemon {
				killDaemon(t, base)
				session := "e2e-bell-" + tc.mode
				if out, err := tuiosCLI(t, base, "new", session, "--detach"); err != nil {
					t.Fatalf("create detached session: %v: %s", err, out)
				}
				opts.args = []string{"attach", session}
			}
			term := startIn(t, base, opts)
			if err := term.WaitFor(func(s tuitest.Screen) bool { return countWindows(s) == 1 }, bootTimeout); err != nil {
				t.Fatalf("no pane: %v\n%s", err, term.Snapshot())
			}
			if err := term.SendKeys("echo READY-$((6*7))", tuitest.Enter); err != nil {
				t.Fatal(err)
			}
			if err := term.WaitForText("READY-42", shellTimeout); err != nil {
				t.Fatalf("shell did not answer: %v\n%s", err, term.Snapshot())
			}
			if bells := bellsSince(stream.bytes(), 0); bells != 0 {
				t.Fatalf("the host got %d bells before any pane rang", bells)
			}
			from := len(stream.bytes())
			if err := term.SendKeys(`printf '\a'; echo RANG-$((6*7))`, tuitest.Enter); err != nil {
				t.Fatal(err)
			}
			if err := term.WaitForText("RANG-42", shellTimeout); err != nil {
				t.Fatalf("printf did not run: %v\n%s", err, term.Snapshot())
			}

			hasToast := func(s tuitest.Screen) bool { return strings.Contains(s.Text(), "bell") }
			// A mode that does something is waited for. A mode that does not
			// is given the same time to do it anyway, so its absence means
			// something.
			deadline := time.Now().Add(uiTimeout)
			if !tc.forward && !tc.toast {
				deadline = time.Now().Add(3 * time.Second)
			}
			var bells int
			var toast bool
			for time.Now().Before(deadline) {
				bells = bellsSince(stream.bytes(), from)
				toast = hasToast(term.Screen())
				if (bells > 0) == tc.forward && toast == tc.toast && (tc.forward || tc.toast) {
					break
				}
				time.Sleep(100 * time.Millisecond)
			}
			if !tc.forward || !tc.toast {
				// Give the absent half its chance to show up.
				time.Sleep(time.Second)
				bells = bellsSince(stream.bytes(), from)
				toast = hasToast(term.Screen())
			}
			if dir := os.Getenv("TUIOS_E2E_FRAMES"); dir != "" {
				if err := os.WriteFile(filepath.Join(dir, "pane-bell-"+name+".ansi"), stream.bytes(), 0o600); err != nil {
					t.Error(err)
				}
			}
			t.Logf("mode %s: %d bare bells after the printf, toast %v", name, bells, toast)
			if tc.forward && bells != 1 {
				t.Errorf("the host got %d bells, want 1", bells)
			}
			if !tc.forward && bells != 0 {
				t.Errorf("the host got %d bells, want none", bells)
			}
			if toast != tc.toast {
				t.Errorf("bell message on screen = %v, want %v\n%s", toast, tc.toast, term.Snapshot())
			}
		})
	}
}
