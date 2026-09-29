package tuie2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// capabilityQuery is the exact query ratatui-image 11.1.0 sends from
// Picker::from_query_stdio(): a kitty graphics query, DA1, the cell size and
// DSR 5. Its read loop ends only on "[0n", so the reply to DSR 5 must be the
// ANSI form CSI 0 n (issue #253).
const capabilityQuery = `\033_Gi=31,s=1,v=1,a=q,t=d,f=24;AAAA\033\\\033[c\033[16t\033[5n`

// TestCapabilityQueryEndsWithANSIStatusReport sends the capability query from
// a pane in raw mode and checks what the pane reads back. It runs in the
// standalone TUI and against a daemon, whose kitty query answers are its own.
func TestCapabilityQueryEndsWithANSIStatusReport(t *testing.T) {
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
			// Raw mode so the reply reaches the file unchanged and unechoed.
			// With min 0 time 10 a read returns nothing after a second of
			// silence, so cat collects every chunk and then sees end of file.
			body := "stty raw -echo min 0 time 10\n" +
				"printf '" + capabilityQuery + "'\n" +
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
			if !strings.HasSuffix(got, "\x1b[0n") {
				t.Fatalf("reply does not end with CSI 0 n: %q\n%s", got, term.Snapshot())
			}
			if strings.Contains(got, "\x1b[?0n") {
				t.Fatalf("reply holds the private CSI ? 0 n: %q", got)
			}
			if !strings.Contains(got, "\x1b_Gi=31;OK\x1b\\") {
				t.Errorf("reply has no kitty query answer: %q", got)
			}
			if !strings.Contains(got, "\x1b[?62;") {
				t.Errorf("reply has no DA1 answer: %q", got)
			}
		})
	}
}
