//go:build unix

package terminal

import (
	"strconv"
	"strings"
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
printf 'R:%sEND\nH:%s\n' "$(printf '%s' "$reply" | tr -d '\033\007')" "$(printf '%s' "$reply" | od -An -tx1 | tr -d ' \n')"
sleep 30`
	w, err := NewWindow("test-local-osc52", "Test", 0, 0, 80, 24, 0, exitChan, make(chan struct{}, 1),
		config.DefaultScrollbackLines, "/bin/sh", "-c", script)
	if err != nil {
		t.Skipf("cannot start a local PTY: %v", err)
	}
	defer w.Close()

	// Each pass reads the screen once, so the checks below all see the same
	// moment. A failure says what the pane showed: the answer without ESC and
	// BEL, and the answer's bytes in hex on the H: row, so a wrong terminator
	// or a split answer is named in the log.
	const want = "R:]52;c;END"
	deadline := time.Now().Add(10 * time.Second)
	var seen []string
	for time.Now().Before(deadline) {
		seen = clipboardRows(w)
		var answered bool
		for _, row := range seen {
			if strings.Contains(row, want) {
				return
			}
			answered = answered || strings.Contains(row, "END")
		}
		if answered {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	shown := strings.Join(seen, "\n")
	if strings.Contains(shown, "R:]52;c;//usr/bin/idEND") {
		t.Fatalf("the OSC 52 query answered with the content the program wrote, want %q; the pane showed:\n%s", want, shown)
	}
	t.Fatalf("the pane never showed %q; the pane showed:\n%s", want, shown)
}

// clipboardRows is the screen's R: and H: rows, quoted so a control
// character in them is visible, read under one lock.
func clipboardRows(w *Window) []string {
	w.RLockIO()
	defer w.RUnlockIO()
	if w.Terminal == nil {
		return nil
	}
	width, height := w.Terminal.Width(), w.Terminal.Height()
	var rows []string
	for y := range height {
		var row strings.Builder
		for x := range width {
			if c := w.Terminal.CellAt(x, y); c != nil {
				row.WriteString(c.Content)
			}
		}
		if line := strings.TrimRight(row.String(), " "); strings.HasPrefix(line, "R:") || strings.HasPrefix(line, "H:") {
			rows = append(rows, strconv.Quote(line))
		}
	}
	return rows
}
