//go:build unix

package terminal

import (
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// TestLocalPaneClipboardQueryIsEmpty pins that a local PTY pane answers an
// OSC 52 query with empty content, as xterm does by default, even after the
// program in the pane wrote the clipboard with OSC 52. The answer goes to the
// pane's input, so it must not hold text the program chose.
//
// The program writes the clipboard, queries it, reads the answer in raw mode
// and prints it without ESC and BEL.
func TestLocalPaneClipboardQueryIsEmpty(t *testing.T) {
	exitChan := make(chan string, 1)
	script := `stty raw -echo
printf '\033]52;c;//usr/bin/id\007\033]52;c;?\007'
reply=$(dd bs=256 count=1 2>/dev/null)
stty sane
printf 'R:%sEND' "$(printf '%s' "$reply" | tr -d '\033\007')"
sleep 30`
	w, err := NewWindow("test-local-osc52", "Test", 0, 0, 80, 24, 0, exitChan, make(chan struct{}, 1),
		config.DefaultScrollbackLines, "/bin/sh", "-c", script)
	if err != nil {
		t.Skipf("cannot start a local PTY: %v", err)
	}
	defer w.Close()

	const want = "R:]52;c;END"
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if windowShows(w, want) {
			return
		}
		if windowShows(w, "END") {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if windowShows(w, "R:]52;c;//usr/bin/idEND") {
		t.Fatalf("the OSC 52 query answered with the content the program wrote, want %q", want)
	}
	t.Fatalf("the pane never showed %q", want)
}
