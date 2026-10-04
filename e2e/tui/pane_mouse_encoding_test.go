package tuie2e

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// TestPaneMouseReportEncoding: a program in a pane that turns on mouse
// tracking without SGR (DECSET 1000) reads every click in the form it asked
// for, in a pane wide enough to click past column 223.
//
//   - X10 form: each coordinate is one raw byte, 32 + 1 + column. Column 101
//     is the byte 0x85, not the UTF-8 pair c2 85 the emulator used to send.
//     Column 231 does not fit in a byte, so the click is not reported. The
//     emulator used to wrap it, and the program read a control byte.
//   - 1005, UTF-8: the coordinates are UTF-8 characters, so column 231 fits.
//   - 1015, urxvt: decimal coordinates, the button offset by 32.
//   - 1006, SGR: decimal coordinates, and a release keeps its button.
//
// A release is button 3 in every form but SGR.
//
// It runs in the standalone TUI, where the pane's emulator writes the report,
// and against a daemon, where the client encodes it and sends it to the PTY.
//
// How this could pass wrongly, written down first:
//   - the click past column 223 might land outside the pane, so the X10 form
//     reads nothing there for the wrong reason. The SGR step clicks the same
//     cells and must read all three clicks, the far one included.
//   - the program might read the bytes through a line discipline that changes
//     them. It runs in raw mode, and the X10 step must read the raw byte 0x85.
//   - a report could come from the harness rather than tuios. The harness
//     sends SGR reports to tuios, never the X10, urxvt or UTF-8 forms.
//
// Negative control: with mouse_encode.go's encode replaced by the old
// ansi.MouseX10 call for every non-SGR encoding, the x10, utf8 and urxvt
// steps fail in both subtests, and the sgr step still passes.
func TestPaneMouseReportEncoding(t *testing.T) {
	for _, daemon := range []bool{false, true} {
		name := "standalone"
		if daemon {
			name = "daemon"
		}
		t.Run(name, func(t *testing.T) {
			term, base := start(t, startOpts{cols: 260, rows: 24, daemonDefault: daemon})
			if daemon {
				killDaemon(t, base)
			}
			waitBoot(t, term)
			newWindow(t, term)
			enableTiling(t, term)
			enterTerminalMode(t, term)
			runInShell(t, term, "echo RE\"\"ADY", "READY", shellTimeout)

			dir := t.TempDir()
			art := artifactDir(t)
			// The pane is at least 232 columns wide, so pane columns 6, 101
			// and 231 (one-based) are all inside it.
			cols := []int{5, 100, 230}

			for _, step := range []struct {
				name, modes string
				want        func(y int) string
			}{
				{"x10", "\\033[?1000h", func(y int) string {
					b := func(btn, x int) string { return string([]byte{0x1b, '[', 'M', byte(btn), byte(33 + x), byte(33 + y)}) }
					return b(32, 5) + b(35, 5) + b(32, 100) + b(35, 100)
				}},
				{"utf8", "\\033[?1000h\\033[?1005h", func(y int) string {
					b := func(btn, x int) string { return "\x1b[M" + string(rune(btn)) + string(rune(33+x)) + string(rune(33+y)) }
					return b(32, 5) + b(35, 5) + b(32, 100) + b(35, 100) + b(32, 230) + b(35, 230)
				}},
				{"urxvt", "\\033[?1000h\\033[?1015h", func(y int) string {
					b := func(btn, x int) string { return fmt.Sprintf("\x1b[%d;%d;%dM", btn, x+1, y+1) }
					return b(32, 5) + b(35, 5) + b(32, 100) + b(35, 100) + b(32, 230) + b(35, 230)
				}},
				{"sgr", "\\033[?1000h\\033[?1006h", func(y int) string {
					b := func(x int, final byte) string { return fmt.Sprintf("\x1b[<0;%d;%d%c", x+1, y+1, final) }
					return b(5, 'M') + b(5, 'm') + b(100, 'M') + b(100, 'm') + b(230, 'M') + b(230, 'm')
				}},
			} {
				out := filepath.Join(dir, step.name)
				script := filepath.Join(dir, step.name+".sh")
				// Raw mode so the reports reach the file unchanged. With min 0
				// time 40, cat ends after four seconds of silence.
				body := "stty raw -echo min 0 time 40\n" +
					"printf '" + step.modes + "'\n" +
					"printf 'MOUSE%sREADY\\r\\n' " + step.name + "\n" +
					"cat > " + out + "\n" +
					"printf '\\033[?1000l\\033[?1005l\\033[?1006l\\033[?1015l'\n" +
					"stty sane\n" +
					"echo " + splitMarker("PROBEDONE"+step.name) + "\n"
				if err := os.WriteFile(script, []byte(body), 0o600); err != nil {
					t.Fatal(err)
				}
				if err := term.SendKeys("sh "+script, tuitest.Enter); err != nil {
					t.Fatalf("run the %s probe: %v", step.name, err)
				}
				row, col := findText(t, term, "MOUSE"+step.name+"READY")
				for _, x := range cols {
					mouseClick(t, term, col+x, row, tuitest.MouseLeft, 0)
				}
				if err := term.WaitForText("PROBEDONE"+step.name, shellTimeout); err != nil {
					t.Fatalf("the %s probe never finished: %v\n%s", step.name, err, term.Snapshot())
				}
				raw, err := os.ReadFile(out)
				if err != nil {
					t.Fatalf("read the %s reports: %v", step.name, err)
				}
				_ = os.WriteFile(filepath.Join(art, step.name+".reports"), raw, 0o644)
				y, ok := reportRow(step.name, raw)
				if !ok {
					t.Errorf("%s: the pane read %q, which does not start with a report of the first click\n%s",
						step.name, raw, term.Snapshot())
					continue
				}
				if want := step.want(y); string(raw) != want {
					t.Errorf("%s: the pane read %q, want %q", step.name, raw, want)
				}
			}
		})
	}
}

var (
	urxvtReport = regexp.MustCompile(`^\x1b\[32;6;(\d+)M`)
	sgrReport   = regexp.MustCompile(`^\x1b\[<0;6;(\d+)M`)
)

// reportRow reads the zero-based pane row out of the first report, the press
// at pane column 6. The test clicks one row, so every report carries it.
func reportRow(encoding string, raw []byte) (int, bool) {
	switch encoding {
	case "urxvt", "sgr":
		re := urxvtReport
		if encoding == "sgr" {
			re = sgrReport
		}
		m := re.FindSubmatch(raw)
		if m == nil {
			return 0, false
		}
		n, _ := strconv.Atoi(string(m[1]))
		return n - 1, true
	default:
		if len(raw) < 6 || string(raw[:5]) != "\x1b[M "+string(rune(33+5)) {
			return 0, false
		}
		return int(raw[5]) - 33, true
	}
}

var _ = time.Second
