package tuie2e

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"testing"

	"github.com/Gaurav-Gosain/tuitest"
)

// #2157: a guest that scrolls the way Neovim does, in a lone pane. Each guest
// frame is one synchronized update (DEC 2026) that sets a scroll region,
// scrolls it up a line, writes the new bottom line, and writes a status line
// below the region. Every frame the host presents must be one whole guest
// frame: the region holds consecutive lines and the status names the first.
//
// On a client that writes cells, a lone pane is drawn from its emulator
// (pane_cells.go). A frame drawn while the guest's output held the pane's lock
// went the usual way instead, and that way served the text it last drew, from
// before the pane took the cell path: the shell prompt, under a scrolled
// Neovim. The string path read the emulator half way through an update when
// the update opened between its check and its lock.

const (
	scrollRegion = 20
	// scrollRounds bursts of scrollBurst frames each. A burst this long is
	// still being read into the pane's emulator when the client draws, so
	// some frames find the pane's lock taken, as a held j in Neovim does.
	scrollRounds = 10
	scrollBurst  = 1500
)

func scrollLine(n int) string {
	return fmt.Sprintf("L%06d %s", n, strings.Repeat(string(rune('a'+n%26)), 40))
}

var (
	scrollLineRE   = regexp.MustCompile(`L(\d{6}) ([a-z]+)`)
	scrollStatusRE = regexp.MustCompile(`STEP (\d{6})`)
)

// scrollGuestFirst draws lines 0 to scrollRegion-1 and the status STEP 0.
func scrollGuestFirst() string {
	var b strings.Builder
	b.WriteString("\x1b[?2026h\x1b[2J")
	for i := range scrollRegion {
		fmt.Fprintf(&b, "\x1b[%d;1H%s", i+1, scrollLine(i))
	}
	fmt.Fprintf(&b, "\x1b[%d;1HSTEP %06d", scrollRegion+1, 0)
	b.WriteString("\x1b[?2026l")
	return b.String()
}

// scrollGuestFrame is the guest's frame k, lines k to k+scrollRegion-1,
// drawn from frame k-1 by a scroll.
func scrollGuestFrame(k int) string {
	var b strings.Builder
	b.WriteString("\x1b[?2026h")
	fmt.Fprintf(&b, "\x1b[1;%dr", scrollRegion)
	b.WriteString("\x1b[1S")
	fmt.Fprintf(&b, "\x1b[%d;1H\x1b[2K%s", scrollRegion, scrollLine(k+scrollRegion-1))
	b.WriteString("\x1b[r")
	fmt.Fprintf(&b, "\x1b[%d;1H\x1b[2KSTEP %06d", scrollRegion+1, k)
	b.WriteString("\x1b[?2026l")
	return b.String()
}

// scrollFrameProblem says what is wrong with the pane on one presented
// screen, or returns "" when the screen shows one whole guest frame.
func scrollFrameProblem(s tuitest.Screen) string {
	_, rows := s.Size()
	var nums []int
	step := -1
	for y := range rows {
		line := s.Line(y)
		if m := scrollStatusRE.FindStringSubmatch(line); m != nil {
			step, _ = strconv.Atoi(m[1])
		}
		m := scrollLineRE.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		n, _ := strconv.Atoi(m[1])
		if m[2] != strings.Repeat(string(rune('a'+n%26)), 40) {
			return fmt.Sprintf("row %d: line %d is cut or has the text of another line", y, n)
		}
		nums = append(nums, n)
	}
	if step < 0 {
		return "no status line: the pane shows something the guest did not draw"
	}
	if len(nums) != scrollRegion {
		return fmt.Sprintf("status STEP %06d with %d lines, want %d: %v", step, len(nums), scrollRegion, nums)
	}
	for i, n := range nums {
		if n != step+i {
			return fmt.Sprintf("status STEP %06d but the lines are %v", step, nums)
		}
	}
	return ""
}

func TestScrollingGuestPresentsWholeFrames(t *testing.T) {
	for _, path := range []struct{ name, env string }{{"cells", "1"}, {"string", "0"}} {
		t.Run(path.name, func(t *testing.T) {
			base := t.TempDir()
			writeConfig(t, base, "[startup]\nopen_default_window = true\ntiled = true\nstart_in_terminal_mode = true\n")
			tape := &hostTape{}
			const cols, rows = 100, 30
			term := startIn(t, base, startOpts{cols: cols, rows: rows, out: tape,
				env: []string{"TUIOS_CELL_FRAME=" + path.env}})
			if err := term.WaitFor(func(s tuitest.Screen) bool { return countWindows(s) == 1 }, bootTimeout); err != nil {
				t.Fatalf("boot: %v\n%s", err, term.Snapshot())
			}
			pipe := filepath.Join(base, "guest")
			if err := syscall.Mkfifo(pipe, 0o600); err != nil {
				t.Fatal(err)
			}
			if err := term.SendKeys("exec cat '"+pipe+"'", tuitest.Enter); err != nil {
				t.Fatal(err)
			}
			w := openFifo(t, pipe)
			t.Cleanup(func() { _ = w.Close() })
			if _, err := w.WriteString(scrollGuestFirst()); err != nil {
				t.Fatal(err)
			}
			if err := term.WaitForText(fmt.Sprintf("STEP %06d", 0), uiTimeout); err != nil {
				t.Fatalf("first frame: %v\n%s", err, term.Snapshot())
			}
			if err := term.WaitStable(uiTimeout); err != nil {
				t.Fatal(err)
			}
			from := tape.len()
			k := 0
			for range scrollRounds {
				var burst strings.Builder
				for range scrollBurst {
					k++
					burst.WriteString(scrollGuestFrame(k))
				}
				if _, err := w.WriteString(burst.String()); err != nil {
					t.Fatal(err)
				}
				if err := term.WaitForText(fmt.Sprintf("STEP %06d", k), uiTimeout); err != nil {
					t.Fatalf("frame %d: %v\n%s", k, err, term.Snapshot())
				}
			}
			if err := term.WaitStable(uiTimeout); err != nil {
				t.Fatal(err)
			}
			b, full := tape.bytes()
			if full {
				t.Fatal("the host tape is full")
			}
			if dir := os.Getenv("TUIOS_E2E_FRAMES"); dir != "" {
				if err := os.WriteFile(filepath.Join(dir, "scroll-"+path.name+".ansi"), b, 0o600); err != nil {
					t.Error(err)
				}
			}
			screens := presentedScreens(b, cols, rows, from)
			bad := 0
			for i, s := range screens {
				if p := scrollFrameProblem(s); p != "" {
					bad++
					if bad <= 3 {
						t.Errorf("presented frame %d of %d: %s\n%s", i, len(screens), p, s.Text())
					}
				}
			}
			t.Logf("%s: %d presented frames, %d partly painted, %d bytes", path.name, len(screens), bad, len(b)-from)
			if len(screens) < scrollRounds {
				t.Errorf("%d presented frames for %d bursts: the host was not shown the scroll", len(screens), scrollRounds)
			}
			if p := scrollFrameProblem(term.Screen()); p != "" {
				t.Errorf("final screen: %s\n%s", p, term.Snapshot())
			}
		})
	}
}
