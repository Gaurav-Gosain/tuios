package tuie2e

import (
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// pageKey sends the xterm form of PageUp or PageDown with one modifier. The
// terminal really sends these bytes, so this drives Bubble Tea's decoder as
// well as tuios's binding lookup. modifier is 2 for Shift and 5 for Ctrl.
func pageKey(t *testing.T, term *tuitest.Terminal, key, modifier int) {
	t.Helper()
	if err := term.SendKeys(tuitest.Key(fmt.Sprintf("\x1b[%d;%d~", key, modifier))); err != nil {
		t.Fatalf("send modified page key: %v", err)
	}
}

// exercisePageScroll proves that one configured page-up press moves by a
// screen-sized step, and that its matching page-down press returns to live
// output. It returns the rendered frame at the bottom as the repeatable
// artifact for a failure report or a verbose test run.
func exercisePageScroll(t *testing.T, term *tuitest.Terminal, modifier int) string {
	t.Helper()
	const total = 300
	fillScrollback(t, term, "PAGEBIND", total)
	before := settledScroll(t, term, "PAGEBIND")
	visible := strings.Count(term.Screen().Text(), "PAGEBIND-")
	if visible < 2 {
		t.Fatalf("the pane shows only %d tagged lines, not enough to measure a page\n%s", visible, term.Snapshot())
	}

	pageKey(t, term, 5, modifier)
	after := settledScroll(t, term, "PAGEBIND")
	if moved := before - after; moved < visible-2 || moved > visible+2 {
		t.Fatalf("one page-up moved the newest visible line from %d to %d (%d lines), want about the %d tagged rows the pane shows\n%s",
			before, after, moved, visible, term.Snapshot())
	}

	pageKey(t, term, 6, modifier)
	waitScrolledTo(t, term, "PAGEBIND", "page-down did not return to live output",
		func(n int) bool { return n == total })
	return term.Snapshot()
}

// pasteWithMiddleClick drives a physical middle-button press and release over
// the pane. The OSC 52 responder is the host clipboard, so seeing clip at the
// shell prompt proves the click reached tuios's paste path rather than the
// pane's ordinary mouse handling.
func pasteWithMiddleClick(t *testing.T, term *tuitest.Terminal, resp *osc52Responder, mods tuitest.KeyMods, clip string) {
	t.Helper()
	resp.arm(term)
	col, row := paneCell(t, term)
	mouseClick(t, term, col, row, tuitest.MouseMiddle, mods)
	if err := term.WaitForText(clip, shellTimeout); err != nil {
		t.Fatalf("middle-click paste never reached the pane: %v\n%s", err, term.Snapshot())
	}
}

// TestDefaultPageAndMiddleBindings exercises the shipped bindings from a real
// terminal: Shift+PageUp/Down scroll a page, and a plain middle click pastes.
func TestDefaultPageAndMiddleBindings(t *testing.T) {
	t.Run("page-scroll", func(t *testing.T) {
		term, _ := start(t, startOpts{cols: 120, rows: 40})
		waitBoot(t, term)
		newWindow(t, term)
		enterTerminalMode(t, term)

		frame := exercisePageScroll(t, term, 2) // Shift
		t.Logf("page-scroll frame after the round trip:\n%s", frame)
		alive(t, term, "after the default page-scroll bindings")
	})

	t.Run("middle-paste", func(t *testing.T) {
		const clip = "MIDDLEDEFAULTXYZ"
		resp := &osc52Responder{replyB: base64.StdEncoding.EncodeToString([]byte(clip))}
		term, _ := start(t, startOpts{cols: 120, rows: 40, out: resp})
		waitBoot(t, term)
		newWindow(t, term)
		enterTerminalMode(t, term)

		pasteWithMiddleClick(t, term, resp, 0, clip)
		t.Logf("middle-paste frame:\n%s", term.Snapshot())
		alive(t, term, "after the default middle-paste binding")
	})
}

// TestPageAndMiddleBindingsCanBeChanged proves the gestures are bindings, not
// literals in the input handlers. This config moves page scrolling from Shift
// to Ctrl and requires Ctrl on the middle click.
func TestPageAndMiddleBindingsCanBeChanged(t *testing.T) {
	config := `[keybindings.terminal_mode]
terminal_scroll_page_up = ["ctrl+pgup"]
terminal_scroll_page_down = ["ctrl+pgdown"]
terminal_paste_host = ["ctrl+middle"]
`
	t.Run("page-scroll", func(t *testing.T) {
		base := t.TempDir()
		writeConfig(t, base, config)
		term := startIn(t, base, startOpts{cols: 120, rows: 40})
		waitBoot(t, term)
		newWindow(t, term)
		enterTerminalMode(t, term)

		frame := exercisePageScroll(t, term, 5) // Ctrl
		t.Logf("rebound page-scroll frame after the round trip:\n%s", frame)
		alive(t, term, "after the rebound page-scroll bindings")
	})

	t.Run("middle-paste", func(t *testing.T) {
		const clip = "MIDDLEREBOUNDXYZ"
		resp := &osc52Responder{replyB: base64.StdEncoding.EncodeToString([]byte(clip))}
		base := t.TempDir()
		writeConfig(t, base, config)
		term := startIn(t, base, startOpts{cols: 120, rows: 40, out: resp})
		waitBoot(t, term)
		newWindow(t, term)
		enterTerminalMode(t, term)

		pasteWithMiddleClick(t, term, resp, tuitest.ModCtrl, clip)
		t.Logf("rebound middle-paste frame:\n%s", term.Snapshot())
		alive(t, term, "after the rebound middle-paste binding")
	})
}

// sgrMiddleClick matches a middle press and its release as a pane receives
// them in SGR encoding, at the same cell.
var sgrMiddleClick = regexp.MustCompile(`^\x1b\[<1;(\d+);(\d+)M\x1b\[<1;(\d+);(\d+)m$`)

// TestMousePaneKeepsPageKeysAndMiddleButton checks a pane whose program turned
// a mouse mode on, as vim with mouse=a, lazygit and tmux do. Shift+PageUp and
// a plain middle click reach the program, as they did before the bindings.
// A ctrl+middle binding still runs over the program, and its release stays
// with tuios, so the program never sees a release without its press.
func TestMousePaneKeepsPageKeysAndMiddleButton(t *testing.T) {
	bin := buildKeyEcho(t)
	base := t.TempDir()
	writeConfig(t, base, `[keybindings.terminal_mode]
terminal_paste_host = ["middle", "ctrl+middle"]
`)
	log := filepath.Join(base, "pane-bytes")
	term := startIn(t, base, startOpts{cols: 120, rows: 40})
	waitBoot(t, term)
	newWindow(t, term)
	enterTerminalMode(t, term)
	runInShell(t, term, `printf '\033[?1000h\033[?1006h'; stty raw -echo; `+bin+" 0 "+log, "KEYECHO-READY", shellTimeout)

	const shiftPageUp = "\x1b[5;2~"
	sendRaw(t, term, shiftPageUp)
	waitPaneBytes(t, term, log, shiftPageUp)

	// The binding takes the press, so the release is tuios's too.
	col, row := paneCell(t, term)
	mouseClick(t, term, col, row, tuitest.MouseMiddle, tuitest.ModCtrl)

	mouseClick(t, term, col, row, tuitest.MouseMiddle, 0)
	deadline := time.Now().Add(uiTimeout)
	var got string
	for time.Now().Before(deadline) {
		b, _ := os.ReadFile(log)
		got = strings.TrimPrefix(string(b), shiftPageUp)
		if sgrMiddleClick.MatchString(got) {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	// Anything that was still on its way.
	time.Sleep(300 * time.Millisecond)
	b, _ := os.ReadFile(log)
	got = strings.TrimPrefix(string(b), shiftPageUp)
	m := sgrMiddleClick.FindStringSubmatch(got)
	if m == nil {
		t.Fatalf("after Shift+PageUp the pane received %q, want one middle press and its release\n%s", got, term.Snapshot())
	}
	if m[1] != m[3] || m[2] != m[4] {
		t.Fatalf("the middle press and release are at different cells: %q", got)
	}
	t.Logf("pane bytes: %q\n%s", string(b), term.Snapshot())
}
