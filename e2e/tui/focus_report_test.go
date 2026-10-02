package tuie2e

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestFocusReportOnEnable: a program that turns on focus reporting (DECSET
// 1004) in the focused pane reads one focus-in report (CSI I) at once, as
// xterm and kitty send the current state on the set. With the host terminal
// out of focus it reads nothing. It runs in the standalone TUI and against a
// daemon, whose emulator is the one that answers.
//
// How this could pass wrongly, written down first:
//   - the report might come from the harness rather than tuios, since the
//     test types CSI I to the host side to set its focus; so the program
//     reads only bytes sent after it set 1004, and the unfocused half sends
//     the same host bytes and must read nothing;
//   - a report sent by both the daemon and the client would still contain
//     CSI I, so the reply must be exactly one CSI I;
//   - the unfocused half could read nothing because the pane never set 1004,
//     so it runs the same script as the focused half, which must answer.
//
// Negative controls: with the noteFocusReporting call cut from vtWriter in
// internal/session/session.go the daemon subtest reads nothing in the focused
// half; with the edge check cut from the PTY reader in
// internal/terminal/window_io.go the standalone one does. With sessionShown
// accepting a client out of focus, the daemon subtest fails the unfocused half.
func TestFocusReportOnEnable(t *testing.T) {
	for _, daemon := range []bool{false, true} {
		name := "standalone"
		if daemon {
			name = "daemon"
		}
		t.Run(name, func(t *testing.T) {
			term, _ := startGraphicsPane(t, daemon)
			dir := t.TempDir()
			art := artifactDir(t)
			probe := func(step string) string {
				t.Helper()
				out := filepath.Join(dir, step)
				script := filepath.Join(dir, step+".sh")
				// Raw mode so the report reaches the file unchanged. With
				// min 0 time 10 cat ends after a second of silence. 1004 is
				// set after stty, so nothing typed before it is read.
				body := "stty raw -echo min 0 time 10\n" +
					"printf '\\033[?1004h'\n" +
					"cat > " + out + "\n" +
					"printf '\\033[?1004l'\n" +
					"stty sane\n" +
					"echo " + splitMarker("PROBEDONE"+step) + "\n"
				if err := os.WriteFile(script, []byte(body), 0o600); err != nil {
					t.Fatal(err)
				}
				runInShell(t, term, "sh "+script, "PROBEDONE"+step, 15*time.Second)
				raw, err := os.ReadFile(out)
				if err != nil {
					t.Fatalf("read reply: %v", err)
				}
				_ = os.WriteFile(filepath.Join(art, step+".reply"), raw, 0o644)
				return string(raw)
			}

			// Host focused: one focus-in report.
			if err := term.Type("\x1b[I"); err != nil {
				t.Fatal(err)
			}
			time.Sleep(500 * time.Millisecond)
			if got := probe("focused"); got != "\x1b[I" {
				t.Fatalf("focused pane read %q after setting 1004, want exactly %q\n%s", got, "\x1b[I", term.Snapshot())
			}

			// Host out of focus: nothing.
			if err := term.Type("\x1b[O"); err != nil {
				t.Fatal(err)
			}
			time.Sleep(500 * time.Millisecond)
			if got := probe("unfocused"); got != "" {
				t.Fatalf("pane read %q with the host out of focus, want nothing\n%s", got, term.Snapshot())
			}
			saveArtifact(t, term, art, "final")
			t.Logf("replies in %s", art)
		})
	}
}
