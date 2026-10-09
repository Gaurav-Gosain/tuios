package tuie2e

import (
	"bytes"
	"os"
	"path/filepath"
	"regexp"
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

// TestExitTurnsOffWhatItTurnedOn sets a bar cursor in a pane, leaves tuios,
// and reads the host stream for every mode and style tuios left behind.
//
// The exit reset used to start with RIS, which put every mode back whether or
// not tuios named it. Without RIS, each mode tuios turns on needs its own
// reset, from Bubble Tea's exit or from terminal.ResetSequence. This test is
// the list of them.
//
// How this could pass wrongly, written down first:
//   - The pane's cursor style could never reach the host, so there is nothing
//     to reset. Each run requires a bar style before tuios leaves the
//     alternate screen.
//   - A mode could be reset early and set again later. The test reads the
//     last value of each mode in the whole stream.
//   - Bubble Tea draws no cursor in window management mode, so a reset tied
//     to the last frame could be missed there. Both modes are run.
func TestExitTurnsOffWhatItTurnedOn(t *testing.T) {
	for _, wm := range []bool{false, true} {
		name := "from-terminal-mode"
		if wm {
			name = "from-window-management-mode"
		}
		t.Run(name, func(t *testing.T) {
			streamPath := filepath.Join(t.TempDir(), "host.log")
			stream, err := newBoundedLog(streamPath, ptyLogLimit)
			if err != nil {
				t.Fatalf("create the stream log: %v", err)
			}
			t.Cleanup(func() { _ = stream.Close() })
			term, _ := start(t, startOpts{cols: 100, rows: 30, out: stream})
			waitBoot(t, term)
			newWindow(t, term)
			enterTerminalMode(t, term)
			runInShell(t, term, `printf '\033[6 q'; echo CUR""SET`, "CURSET", shellTimeout)
			if err := term.WaitStable(uiTimeout); err != nil {
				t.Fatalf("the screen never settled: %v", err)
			}
			if wm {
				leaveTerminalMode(t, term)
			}
			if err := term.SendKeys(tuitest.Ctrl('b'), "q"); err != nil {
				t.Fatalf("send leader q: %v", err)
			}
			if _, err := term.WaitExit(uiTimeout); err != nil {
				t.Fatalf("tuios did not exit: %v\n%s", err, term.Snapshot())
			}

			raw, err := os.ReadFile(streamPath)
			if err != nil {
				t.Fatalf("read the stream log: %v", err)
			}
			_ = os.WriteFile(filepath.Join(artifactDir(t), "host-stream.bin"), raw, 0o644)
			leave := bytes.LastIndex(raw, []byte("\x1b[?1049l"))
			if leave < 0 {
				t.Fatalf("tuios never left the alternate screen")
			}
			if !bytes.Contains(raw[:leave], []byte("\x1b[6 q")) {
				t.Fatalf("the pane's bar cursor never reached the host, so this run tests nothing")
			}

			// The last value of each DEC private mode. Show cursor (25) and
			// autowrap (7) are on by default, so they must end on.
			last := map[string]string{}
			for _, m := range decModeRE.FindAllSubmatch(raw, -1) {
				for _, n := range strings.Split(string(m[1]), ";") {
					last[n] = string(m[2])
				}
			}
			for n, v := range last {
				want := "l"
				if n == "25" || n == "7" {
					want = "h"
				}
				if v != want {
					t.Errorf("tuios left DEC mode %s at %s, want %s", n, v, want)
				}
			}
			checkLast := func(what string, re *regexp.Regexp, want string) {
				t.Helper()
				all := re.FindAll(raw, -1)
				if len(all) > 0 && string(all[len(all)-1]) != want {
					t.Errorf("tuios left the %s at %q, want %q", what, all[len(all)-1], want)
				}
			}
			checkLast("cursor style", regexp.MustCompile(`\x1b\[[0-9]? q`), "\x1b[0 q")
			checkLast("kitty keyboard flags", regexp.MustCompile(`\x1b\[[=>][0-9;]*u`), "\x1b[=0;1u")
			checkLast("modifyOtherKeys", regexp.MustCompile(`\x1b\[>4(;[0-9]+)?m`), "\x1b[>4m")
			checkLast("pointer shape", regexp.MustCompile(`\x1b\]22;[^\x1b\x07]*`), "\x1b]22;default")
		})
	}
}

// decModeRE matches a DEC private mode set or reset: CSI ? Pm h or l.
var decModeRE = regexp.MustCompile(`\x1b\[\?([0-9;]+)([hl])`)
