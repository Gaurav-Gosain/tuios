package tuie2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// kittyProbeQuery is how a program learns whether the terminal draws kitty
// graphics, as kitty's documentation describes it: a query, then DA1, which
// every terminal answers. A terminal without kitty graphics answers only the
// DA1. DSR 5 after it marks the end of the replies.
const kittyProbeQuery = `\033_Gi=31,s=1,v=1,a=q,t=d,f=24;AAAA\033\\\033[c\033[5n`

// TestKittyQueryFollowsTheHostTerminal sends the kitty graphics probe from a
// pane in raw mode and reads what the pane is told, with a host terminal that
// draws kitty graphics and one that does not.
//
// The daemon answered the query OK whatever the attached client's terminal
// was. A client without kitty graphics drops every image, so yazi, kitten
// icat and timg drew nothing where they would have drawn a text fallback. The
// standalone TUI was already silent there, which is what a terminal without
// kitty graphics does, and the daemon now follows the attached clients the way
// its DA1 answer does for sixel.
//
// How this could pass wrongly, written down first:
//   - The query could go unanswered because the pane answers nothing at all.
//     Every run requires the DA1 answer and the DSR 5 that ends the replies.
//   - Silence could be the answer for every host. The kitty host run is the
//     positive half: it must read OK for the same query.
//   - The reply could be read before it is complete. The file is read only
//     after the shell's cat saw a second of silence, and it must end in the
//     DSR 5 answer.
//
// The replies are saved under artifactDir.
func TestKittyQueryFollowsTheHostTerminal(t *testing.T) {
	for _, daemon := range []bool{false, true} {
		mode := "standalone"
		if daemon {
			mode = "daemon"
		}
		for _, kitty := range []bool{true, false} {
			host := "kitty-host"
			if !kitty {
				host = "plain-host"
			}
			t.Run(mode+"/"+host, func(t *testing.T) {
				var term *tuitest.Terminal
				if kitty {
					term, _ = startGraphicsPane(t, daemon)
				} else {
					term = startPlainPane(t, daemon)
				}
				got := probePane(t, term, kittyProbeQuery)
				if err := os.WriteFile(filepath.Join(artifactDir(t), "reply.txt"), []byte(got), 0o644); err != nil {
					t.Fatal(err)
				}
				if !strings.HasSuffix(got, "\x1b[0n") {
					t.Fatalf("the replies do not end with the DSR 5 answer: %q", got)
				}
				if !strings.Contains(got, "\x1b[?62;") {
					t.Fatalf("the pane gave no DA1 answer: %q", got)
				}
				answered := strings.Contains(got, "\x1b_G")
				switch {
				case kitty && !strings.Contains(got, "\x1b_Gi=31;OK\x1b\\"):
					t.Errorf("a host that draws kitty graphics, and the pane was not told OK: %q", got)
				case !kitty && answered:
					t.Errorf("a host without kitty graphics, and the pane was answered as if it had them: %q", got)
				}
			})
		}
	}
}

// startPlainPane boots tuios on a host terminal that draws neither kitty
// graphics nor sixel, opens one pane and leaves it in terminal mode at a
// shell prompt.
func startPlainPane(t *testing.T, daemon bool) *tuitest.Terminal {
	t.Helper()
	term, base := start(t, startOpts{
		cols: 120, rows: 40,
		env:           []string{"TUIOS_KITTY_GRAPHICS=0", "TUIOS_SIXEL_GRAPHICS=0"},
		daemonDefault: daemon,
	})
	if daemon {
		killDaemon(t, base)
	}
	waitBoot(t, term)
	newWindow(t, term)
	enterTerminalMode(t, term)
	runInShell(t, term, "echo RE\"\"ADY", "READY", shellTimeout)
	return term
}

// probePane writes query (printf syntax) from the focused pane's shell in raw
// mode and returns every byte the pane read back.
func probePane(t *testing.T, term *tuitest.Terminal, query string) string {
	t.Helper()
	dir := t.TempDir()
	out := filepath.Join(dir, "reply")
	script := filepath.Join(dir, "probe.sh")
	// Raw mode so the reply reaches the file unchanged and unechoed. With
	// min 0 time 10 a read returns nothing after a second of silence, so cat
	// collects every chunk and then sees end of file.
	body := "stty raw -echo min 0 time 10\n" +
		"printf '" + query + "'\n" +
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
	return string(raw)
}
