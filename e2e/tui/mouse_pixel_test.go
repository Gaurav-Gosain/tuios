package tuie2e

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// pixelReporter is a pane program that tracks the mouse in SGR-pixel mode
// (1003, 1006 and 1016), the modes a kitty-graphics browser or a Wayland
// window turns on. It prints each motion report on its own line as
// PX<button>;<x>;<y>. The input "off" followed by M turns 1016 off again, and
// the reports that follow are in cells.
const pixelReporter = `bash -c 'stty -echo -icanon; ` +
	`printf "\033[?1003h\033[?1006h\033[?1016h"; echo PIX""ON; ` +
	`while IFS= read -r -d M r; do case $r in ` +
	`*off) printf "\033[?1016l"; echo PIX""OFF;; ` +
	`*) printf "PX%s.\n" "${r##*<}";; esac; done'`

var pixelReport = regexp.MustCompile(`PX35;(\d+);(\d+)\.`)

// waitPixelReport waits for a report at exactly (x, y), 1-based as on the wire.
// The send function runs again on every poll, because a report sent before
// tuios has read the host's answer to its 1016 request is read in the old
// encoding.
func waitPixelReport(t *testing.T, term *tuitest.Terminal, what string, x, y int, send func()) {
	t.Helper()
	want := fmt.Sprintf("PX35;%d;%d.", x, y)
	deadline := time.Now().Add(uiTimeout)
	for time.Now().Before(deadline) {
		send()
		if strings.Contains(term.Screen().Text(), want) {
			return
		}
		time.Sleep(150 * time.Millisecond)
	}
	t.Fatalf("%s: the pane never got the report %q\n%s", what, want, term.Snapshot())
}

// TestSGRPixelMouseCarriesTheHostPixel covers mode 1016 end to end.
//
// A pane program in SGR-pixel mode must be told where the pointer is in
// pixels. tuios used to tell it the centre of the cell under the pointer, so
// a Wayland window in a pane could only be pointed at to within one cell. Now
// tuios turns 1016 on in its own terminal while such a pane is on screen and
// passes the offset inside the cell through.
//
// The host here is the tuitest emulator, which answers XTWINOPS 16 with an 8x16
// cell and DECRQM 1016 with the state tuios set. TUIOS_CELL_SIZE gives the
// panes the same cell, so the expected pixel is arithmetic.
//
// The positive half: a report sent in host pixels arrives in the pane with
// its offset inside the cell. The negative half: once the program turns 1016
// off, tuios turns it off in its terminal and reads cell reports as cells
// again, and the pane gets SGR cell reports unchanged.
func TestSGRPixelMouseCarriesTheHostPixel(t *testing.T) {
	for _, tc := range []struct {
		name   string
		daemon bool
	}{{"standalone", false}, {"daemon", true}} {
		t.Run(tc.name, func(t *testing.T) {
			out := &lockedBuffer{}
			term, _ := start(t, startOpts{
				daemonDefault: tc.daemon,
				out:           out,
				env:           []string{"TUIOS_CELL_SIZE=8x16"},
			})
			waitBoot(t, term)
			newWindow(t, term)
			enterTerminalMode(t, term)
			runInShell(t, term, pixelReporter, "PIXON", shellTimeout)

			// One report in cells. tuios reads it as cells, since it has not
			// turned 1016 on yet, and the pane gets the cell centre. That
			// gives the pane cell under (col, row) without knowing the layout.
			col, row := paneCell(t, term)
			mouseHover(t, term, col, row)
			var cx, cy int
			if err := term.WaitFor(func(s tuitest.Screen) bool {
				m := pixelReport.FindStringSubmatch(s.Text())
				if m == nil {
					return false
				}
				cx, _ = strconv.Atoi(m[1])
				cy, _ = strconv.Atoi(m[2])
				return true
			}, uiTimeout); err != nil {
				t.Fatalf("the first motion never reached the pane: %v\n%s", err, term.Snapshot())
			}
			// The centre of an 8x16 cell is (4, 8) into it, and the wire adds 1.
			termX, termY := (cx-1-4)/8, (cy-1-8)/16

			// tuios asks its terminal for 1016 after that event.
			if err := waitOutput(out, "\x1b[?1016h", uiTimeout); err != nil {
				t.Fatalf("tuios never turned on SGR-pixel reports in its terminal: %v", err)
			}

			// The pointer at (1, 3) pixels into the same host cell.
			const subX, subY = 1, 3
			waitPixelReport(t, term, "pixel report", termX*8+subX+1, termY*16+subY+1, func() {
				sendMouseThenWait(t, term, "pixel hover", tuitest.MouseEvent{
					Col: col*8 + subX, Row: row*16 + subY,
					Button: tuitest.MouseNone, Action: tuitest.MouseMove, Pixel: true,
				}, mouseGap)
			})

			// The program turns 1016 off. The next report still arrives in
			// pixels, since the terminal is in 1016 until tuios turns it off.
			if err := term.SendKeys("offM"); err != nil {
				t.Fatal(err)
			}
			if err := term.WaitForText("PIXOFF", uiTimeout); err != nil {
				t.Fatalf("the program never turned 1016 off: %v\n%s", err, term.Snapshot())
			}
			sendMouseThenWait(t, term, "pixel hover", tuitest.MouseEvent{
				Col: col*8 + subX, Row: row*16 + subY,
				Button: tuitest.MouseNone, Action: tuitest.MouseMove, Pixel: true,
			}, mouseGap)
			// 1006 and 1016 are one setting in ghostty, xterm and kitty, so
			// 1016 off alone leaves the terminal in X10, not in SGR.
			if err := waitOutput(out, "\x1b[?1016l\x1b[?1006h", uiTimeout); err != nil {
				t.Fatalf("tuios never turned off SGR-pixel reports and put SGR back in its terminal: %v", err)
			}

			// A cell report one cell to the right now reaches the pane as an
			// SGR cell report.
			waitPixelReport(t, term, "cell report", termX+2, termY+1, func() {
				mouseHover(t, term, col+1, row)
			})
			alive(t, term, "after SGR-pixel mouse reports")
		})
	}
}

// TestSGRPixelMouseAfterTheEditor covers the terminal going to $EDITOR while
// tuios has 1016 on in it.
//
// Bubble Tea writes 1006 when it takes the terminal back from ExecProcess,
// and in ghostty, xterm and kitty that ends 1016. tuios kept reading every
// report as pixels, so a click at a cell was divided by the cell size and
// landed near the top left of the screen. Now tuios turns 1016 off before the
// editor runs, reads reports as cells when it comes back, and asks for 1016
// again on the next mouse event.
func TestSGRPixelMouseAfterTheEditor(t *testing.T) {
	for _, tc := range []struct {
		name   string
		daemon bool
	}{{"standalone", false}, {"daemon", true}} {
		t.Run(tc.name, func(t *testing.T) {
			// The editor shows a marker for a moment and exits.
			editor := filepath.Join(t.TempDir(), "editor")
			if err := os.WriteFile(editor, []byte("#!/bin/sh\necho EDITOR\"\"RAN\nsleep 1\n"), 0o755); err != nil {
				t.Fatal(err)
			}
			out := &lockedBuffer{}
			term, _ := start(t, startOpts{
				daemonDefault: tc.daemon,
				out:           out,
				env:           []string{"TUIOS_CELL_SIZE=8x16", "EDITOR=" + editor, "VISUAL="},
			})
			waitBoot(t, term)
			newWindow(t, term)
			enterTerminalMode(t, term)
			runInShell(t, term, pixelReporter, "PIXON", shellTimeout)

			// Find the pane cell under (col, row) from one cell report, as
			// TestSGRPixelMouseCarriesTheHostPixel does.
			col, row := paneCell(t, term)
			mouseHover(t, term, col, row)
			var cx, cy int
			if err := term.WaitFor(func(s tuitest.Screen) bool {
				m := pixelReport.FindStringSubmatch(s.Text())
				if m == nil {
					return false
				}
				cx, _ = strconv.Atoi(m[1])
				cy, _ = strconv.Atoi(m[2])
				return true
			}, uiTimeout); err != nil {
				t.Fatalf("the first motion never reached the pane: %v\n%s", err, term.Snapshot())
			}
			termX, termY := (cx-1-4)/8, (cy-1-8)/16
			if err := waitOutput(out, "\x1b[?1016h", uiTimeout); err != nil {
				t.Fatalf("tuios never turned on SGR-pixel reports in its terminal: %v", err)
			}
			waitPixelReport(t, term, "pixel report before the editor", termX*8+2, termY*16+4, func() {
				sendMouseThenWait(t, term, "pixel hover", tuitest.MouseEvent{
					Col: col*8 + 1, Row: row*16 + 3,
					Button: tuitest.MouseNone, Action: tuitest.MouseMove, Pixel: true,
				}, mouseGap)
			})

			// The editor runs on tuios's own terminal and exits.
			before := strings.Count(out.String(), "\x1b[?1016h")
			openPaletteRow(t, term, "edit scrollback", "Edit scrollback in $EDITOR")
			if err := term.WaitForText("EDITORRAN", uiTimeout); err != nil {
				t.Fatalf("the editor never ran: %v\n%s", err, term.Snapshot())
			}
			if err := term.WaitFor(func(s tuitest.Screen) bool {
				return !strings.Contains(s.Text(), "EDITORRAN")
			}, uiTimeout); err != nil {
				t.Fatalf("tuios never drew its screen again after the editor: %v\n%s", err, term.Snapshot())
			}
			if !strings.Contains(out.String(), "\x1b[?1016l\x1b[?1006h") {
				t.Fatalf("tuios did not turn SGR-pixel reports off before the editor")
			}

			// The terminal is back in SGR cells, and reports in cells until
			// tuios asks for 1016 again; the send below plays a real terminal
			// and follows that. The pointer is on the centre of the cell to
			// the right, so the pane must get that centre in its own pixels
			// either way. Read as pixels, the cell report lands near the top
			// left of the screen instead.
			waitPixelReport(t, term, "report after the editor", (termX+1)*8+4+1, termY*16+8+1, func() {
				if strings.Count(out.String(), "\x1b[?1016h") > before {
					sendMouseThenWait(t, term, "pixel hover", tuitest.MouseEvent{
						Col: (col+1)*8 + 4, Row: row*16 + 8,
						Button: tuitest.MouseNone, Action: tuitest.MouseMove, Pixel: true,
					}, mouseGap)
					return
				}
				mouseHover(t, term, col+1, row)
			})

			// tuios asks for 1016 again, and pixel reports work as before.
			for deadline := time.Now().Add(uiTimeout); strings.Count(out.String(), "\x1b[?1016h") <= before; {
				if time.Now().After(deadline) {
					t.Fatalf("tuios never turned SGR-pixel reports on again after the editor")
				}
				mouseHover(t, term, col, row)
				time.Sleep(50 * time.Millisecond)
			}
			waitPixelReport(t, term, "pixel report after the editor", termX*8+3, termY*16+6, func() {
				sendMouseThenWait(t, term, "pixel hover", tuitest.MouseEvent{
					Col: col*8 + 2, Row: row*16 + 5,
					Button: tuitest.MouseNone, Action: tuitest.MouseMove, Pixel: true,
				}, mouseGap)
			})
			alive(t, term, "after the editor")
		})
	}
}

// waitOutput waits until tuios has written s to its terminal.
func waitOutput(out *lockedBuffer, s string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if strings.Contains(out.String(), s) {
			return nil
		}
		time.Sleep(50 * time.Millisecond)
	}
	return fmt.Errorf("%q not written within %v", s, timeout)
}
