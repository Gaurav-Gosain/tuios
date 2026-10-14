package tuie2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestPaneXTVERSIONNamesTuios asks a pane for the terminal's name (XTVERSION)
// and checks the answer names tuios. The emulator's own default is "vt", and
// tuios sets its name at startup, so the answer shows that both binaries, and
// the daemon that answers for its panes, still do. It runs in the standalone
// TUI and against a daemon.
func TestPaneXTVERSIONNamesTuios(t *testing.T) {
	for _, daemon := range []bool{false, true} {
		name := "standalone"
		if daemon {
			name = "daemon"
		}
		t.Run(name, func(t *testing.T) {
			term, _ := startGraphicsPane(t, daemon)
			dir := t.TempDir()
			out := filepath.Join(dir, "reply")
			script := filepath.Join(dir, "probe.sh")
			// DSR 5 after the question, so the reply is complete when its
			// answer arrives. Raw mode so the reply reaches the file unchanged.
			body := "stty raw -echo min 0 time 10\n" +
				`printf '\033[>q\033[5n'` + "\n" +
				"cat > " + out + "\n" +
				"stty sane\n" +
				"echo PROBE\"\"-DONE\n"
			if err := os.WriteFile(script, []byte(body), 0o600); err != nil {
				t.Fatal(err)
			}
			runInShell(t, term, "sh "+script, "PROBE-DONE", 15*time.Second)

			raw, err := os.ReadFile(out)
			if err != nil {
				t.Fatalf("read reply: %v", err)
			}
			got := string(raw)
			start := strings.Index(got, "\x1bP>|")
			if start < 0 {
				t.Fatalf("no XTVERSION answer in the reply: %q", got)
			}
			answer := got[start+len("\x1bP>|"):]
			if end := strings.Index(answer, "\x1b\\"); end >= 0 {
				answer = answer[:end]
			}
			if answer != "tuios" && !strings.HasPrefix(answer, "tuios ") {
				t.Fatalf("XTVERSION answered %q, want tuios and the version", answer)
			}
		})
	}
}
