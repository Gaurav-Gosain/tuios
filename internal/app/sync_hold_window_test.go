package app

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
)

// How long is a guest allowed to hold a synchronized update open?
//
// vt.syncMaxHold answers 1s, and a guest that runs over it is presented
// anyway, which is a tear. The number is only defensible if two things hold:
// that no well-behaved guest legitimately holds an update that long, and that
// the interval tuios measures is the guest's interval.
//
// Neither the DEC 2026 spec nor its iTerm2 ancestor names a value. What the
// implementations chose spans a factor of twenty: Windows Terminal 100ms,
// Alacritty and mintty 150ms, st 200ms, foot, Ghostty, iTerm2, Konsole,
// xterm.js and tmux 1s, kitty 2s, and contour, WezTerm and Zellij no bound at
// all. tuios sat at 150ms, the floor of that range, and now sits with tmux,
// which is the company a multiplexer belongs in: both hold someone else's
// frame rather than drawing their own.
//
// The guests matter more than the company. Ink, which is what Claude Code
// draws with, writes the opening escape, the frame and the closing escape as
// three separate writes, so a slow reader strands the update open for as long
// as the middle write blocks. Neovim spans partial flushes deliberately, and
// Textual opens the update before it renders. None of them re-open the update
// to extend the deadline, so for those guests the bound is absolute from the
// first byte. That is what made 150ms untenable and 1s workable.
//
// The second is the interesting one, and it is what this measures. tuios does
// not time the guest. It times the gap between parsing 2026h and parsing 2026l,
// and between those two parses sit the daemon's ring, a unix socket, the
// client's read loop and its output batcher. A guest that emitted a frame in
// one write() can still be measured at tens of milliseconds here, and a frame
// large enough to be split across many writes accumulates every hop for every
// chunk. The deadline is spent on transport, not on the guest.
//
// The rig is the rehydration rig, so the pipeline is the real one: a real
// daemon, a real PTY, a real socket, a real client emulator. The guest is a cat
// of a pre-built frame, which is exactly what a TUI's write burst looks like on
// the wire, and the frame ends with a marker inside the update. If the renderer
// ever sees the update closed while the marker is not on screen yet, the
// deadline fired and the frame it would have composed is torn.
//
//	go test ./internal/app/ -run TestSyncHoldWindow -v   (needs TUIOS_PERF=1)

// syncEndMark is written as the last thing inside the update, so its absence
// while the update reads as closed means the close was the deadline rather than
// the guest.
const syncEndMark = "SYNCFRAMEEND"

// buildSyncFrame writes a synchronized frame of roughly wantBytes to a file and
// returns the path. The body is SGR-heavy, because a real TUI frame is: colour
// changes are most of what a repaint costs on the wire.
func buildSyncFrame(t *testing.T, wantBytes int) string {
	t.Helper()
	var b strings.Builder
	b.Grow(wantBytes + 1024)
	b.WriteString("\x1b[?2026h\x1b[H\x1b[2J")
	row := 1
	for b.Len() < wantBytes {
		fmt.Fprintf(&b, "\x1b[%d;1H", row%latRows+1)
		for col := 0; col < latCols-len(syncEndMark) && b.Len() < wantBytes; col += 4 {
			fmt.Fprintf(&b, "\x1b[38;5;%dm%s", 16+col%216, "abcd")
		}
		row++
	}
	fmt.Fprintf(&b, "\x1b[0m\x1b[%d;1H%s\x1b[?2026l", latRows, syncEndMark)

	path := filepath.Join(t.TempDir(), "frame.bin")
	if err := os.WriteFile(path, []byte(b.String()), 0o600); err != nil {
		t.Fatalf("write frame: %v", err)
	}
	return path
}

// TestSyncHoldWindow reports, per frame size, how long the renderer observed the
// guest's update open and whether it ever observed it closed before the frame
// had landed.
func TestSyncHoldWindow(t *testing.T) {
	latencyGate(t)

	for _, size := range []int{64 << 10, 256 << 10, 1 << 20, 4 << 20, 16 << 20, 64 << 20} {
		t.Run(fmt.Sprintf("%dKiB", size>>10), func(t *testing.T) {
			r := newRigSized(t, 1, latCols, latRows)
			w := r.m.GetFocusedWindow()
			if w == nil {
				t.Fatal("no focused window")
			}
			r.waitDaemonShows(w.PTYID, "$")
			r.settle()

			path := buildSyncFrame(t, size)
			r.startPTY(w.PTYID, "cat "+path)

			var (
				sawOpen  bool
				sawClose bool
				openedAt time.Time
				held     time.Duration
				torn     int
				frames   int
				landedAt bool
				deadline = time.After(latencyWait)
			)
		pump:
			for {
				select {
				case <-r.m.PTYDataChan:
					r.m.Update(PTYDataMsg{})
					frames++
					// The renderer's own view of the guest, sampled where the
					// renderer samples it.
					active := w.Terminal != nil && w.Terminal.IsSyncActive()
					onScreen := ansi.Strip(frame(r.m))
					landed := strings.Contains(onScreen, syncEndMark)
					switch {
					case active && !sawOpen:
						sawOpen, openedAt = true, time.Now()
					case !active && sawOpen && !sawClose:
						held, sawClose = time.Since(openedAt), true
					}
					if !active && sawOpen && !landed {
						torn++
					}
					if landed {
						landedAt = true
						break pump
					}
				case <-deadline:
					break pump
				}
			}
			switch {
			case !landedAt:
				t.Errorf("the frame never landed within %v", latencyWait)
			case !sawOpen:
				t.Logf("%7d KiB: the renderer never caught the update open (%d frames)", size>>10, frames)
			default:
				verdict := "guest closed it"
				if held >= time.Second {
					verdict = "DEADLINE FIRED"
				}
				t.Logf("%7d KiB: update open %8.2f ms over %3d frames, %d torn, %s",
					size>>10, float64(held)/float64(time.Millisecond), frames, torn, verdict)
			}
			if torn > 0 {
				t.Logf("%7d KiB: %d frame(s) would have been composed from a half-applied update",
					size>>10, torn)
			}
		})
	}
}
