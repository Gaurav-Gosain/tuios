package tuie2e

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuitest"
)

// TestExitKeepsTheHostScreen starts tuios from a shell that has already
// printed a screenful of lines, leaves tuios, and reads what the host terminal
// shows afterwards. Discussion #588.
//
// tuios left the alternate screen and then sent RIS (ESC c) as part of its
// exit reset. RIS wipes the screen that leaving the alternate screen had just
// put back, and kitty and ghostty also drop the whole scrollback on it, so
// everything printed before tuios started was gone. tmux and zellij leave the
// alternate screen and stop there.
//
// How this could pass wrongly, written down first:
//   - The lines could still be on screen because tuios never took the screen.
//     The test waits for the TUI to be up, and for the lines to be gone,
//     before it leaves.
//   - The check could run before tuios has finished writing. It waits for the
//     shell's AFTER-EXIT line, which the shell prints only once tuios exited.
//   - A screen check alone cannot see the scrollback, which tuitest does not
//     model. The raw stream is also checked for RIS and ED 3, the two
//     sequences that clear a host's scrollback.
//
// The final screen and the raw stream are saved under artifactDir.
func TestExitKeepsTheHostScreen(t *testing.T) {
	// The shell prints the lines, runs tuios ("$@"), then marks the exit and
	// stays up so the screen can be read.
	wrap := []string{"/bin/sh", "-c",
		`i=1; while [ $i -le 60 ]; do echo HOSTLINE-$i; i=$((i+1)); done; "$@"; echo AFTER-EXIT; exec sleep 120`,
		"sh"}

	cases := []struct {
		name   string
		daemon bool
		leave  []any
	}{
		{name: "standalone-quit", leave: []any{tuitest.Ctrl('b'), "q"}},
		{name: "daemon-detach", daemon: true, leave: []any{tuitest.Ctrl('b'), "d"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// The host direction only, bounded: the pty log also holds the
			// keys and the replies this test's terminal sends.
			streamPath := filepath.Join(t.TempDir(), "host.log")
			stream, err := newBoundedLog(streamPath, ptyLogLimit)
			if err != nil {
				t.Fatalf("create the stream log: %v", err)
			}
			t.Cleanup(func() { _ = stream.Close() })
			term, _ := start(t, startOpts{cols: 100, rows: 30, wrap: wrap, daemonDefault: tc.daemon, out: stream})

			if err := term.WaitFor(func(s tuitest.Screen) bool {
				txt := s.Text()
				return !strings.Contains(txt, "HOSTLINE-60") &&
					(strings.Contains(txt, welcomeText) || countWindows(s) >= 1)
			}, bootTimeout); err != nil {
				t.Fatalf("tuios never took the screen: %v\n%s", err, term.Snapshot())
			}
			if err := term.WaitStable(uiTimeout); err != nil {
				t.Fatalf("the screen never settled: %v", err)
			}
			if err := term.SendKeys(tc.leave...); err != nil {
				t.Fatalf("send the leave keys: %v", err)
			}
			if err := term.WaitForText("AFTER-EXIT", shellTimeout); err != nil {
				t.Fatalf("tuios did not exit: %v\n%s", err, term.Snapshot())
			}

			final := term.Snapshot()
			dir := artifactDir(t)
			_ = os.WriteFile(filepath.Join(dir, "screen.txt"), []byte(final), 0o644)
			raw, err := os.ReadFile(streamPath)
			if err != nil {
				t.Fatalf("read the stream log: %v", err)
			}
			_ = os.WriteFile(filepath.Join(dir, "host-stream.bin"), raw, 0o644)

			// The 30-row screen showed lines 32 to 60 before tuios started.
			// Leaving the alternate screen puts them back.
			for _, want := range []string{"HOSTLINE-40", "HOSTLINE-60"} {
				if !strings.Contains(final, want) {
					t.Errorf("the screen from before tuios is gone: %q is missing\n%s", want, final)
				}
			}
			if !bytes.Contains(raw, []byte("\x1b[?1049l")) {
				t.Errorf("tuios never left the alternate screen")
			}
			for name, seq := range map[string]string{"RIS (ESC c)": "\x1bc", "ED 3 (CSI 3 J)": "\x1b[3J"} {
				if n := bytes.Count(raw, []byte(seq)); n > 0 {
					t.Errorf("tuios sent %s to the host terminal %d times", name, n)
				}
			}
		})
	}
}
