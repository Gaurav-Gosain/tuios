package tuie2e

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

const cutLinkRows = 12

func cutLinkScript(urlFile string) string {
	return `#!/bin/sh
set -- $(stty size)
rows=$1 cols=$2
head='F: https://example.com/long/'
n=$((cols * ` + fmt.Sprint(cutLinkRows) + ` - ${#head}))
tail=$(printf "%${n}s" '' | tr ' ' w)
printf '%s' "https://example.com/long/$tail" > '` + urlFile + `'
i=1
while [ $i -le $rows ]; do
	printf 'PRE-%03d\n' $i
	i=$((i + 1))
done
printf '%s%s\n' "$head" "$tail"
i=1
while [ $i -le $((rows - 4)) ]; do
	printf 'FILL-%03d\n' $i
	i=$((i + 1))
done
printf 'CUT''DONE\n'
`
}

func cutLinkSetup(t *testing.T) (*tuitest.Terminal, *lockedBuffer, string, string) {
	t.Helper()
	base := t.TempDir()
	record := filepath.Join(base, "opened.txt")
	opener := filepath.Join(base, "opener.sh")
	if err := os.WriteFile(opener, []byte("#!/bin/sh\nprintf '%s\\n' \"$1\" >> '"+record+"'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	urlFile := filepath.Join(base, "url.txt")
	script := filepath.Join(base, "cut.sh")
	if err := os.WriteFile(script, []byte(cutLinkScript(urlFile)), 0o755); err != nil {
		t.Fatal(err)
	}
	writeConfig(t, base, fmt.Sprintf("[appearance]\nlink_opener = %q\n", opener))

	out := &lockedBuffer{}
	term := startIn(t, base, startOpts{out: out, env: []string{"SSH_CONNECTION=", "SSH_CLIENT=", "SSH_TTY="}})
	waitBoot(t, term)
	newWindow(t, term)
	waitWindowCount(t, term, 1, "opening a shell for the cut link")
	enterTerminalMode(t, term)
	runInShell(t, term, "sh "+script, "CUTDONE", uiTimeout)
	leaveTerminalMode(t, term)
	if err := term.WaitStable(uiTimeout); err != nil {
		t.Fatalf("the screen never settled: %v\n%s", err, term.Snapshot())
	}
	url, err := os.ReadFile(urlFile)
	if err != nil || len(url) < 200 {
		t.Fatalf("the script did not record the URL it printed (%d bytes): %v", len(url), err)
	}
	return term, out, record, string(url)
}

func paneBottomRow(t *testing.T, term *tuitest.Terminal) int {
	t.Helper()
	s := term.Screen()
	cols, rows := s.Size()
	for r := range rows {
		for c := range cols {
			if s.Cell(c, r).Content == "╰" {
				return r - 1
			}
		}
	}
	t.Fatalf("the pane has no bottom border on screen:\n%s", term.Snapshot())
	return 0
}

func bareLinkFrame(url string) *regexp.Regexp {
	return regexp.MustCompile(`\x1b\]8;id=tuios-[0-9a-z]+;` + regexp.QuoteMeta(url) + `(\x07|\x1b\\)`)
}

// TestACutURLOpensWhole clicks a URL the viewport cuts at the top, and then,
// scrolled back, one it cuts at the bottom. Both open the whole address, and
// the frame drawn while scrolled back carries the whole address as OSC 8.
//
// Negative control, confirmed red (see NEGATIVE_CONTROLS.md): with the line
// read from the viewport rows only (link_hover.go, link_emit.go and
// render_terminal.go as before the fix), the first click opens nothing, since
// the rows on screen hold no scheme, and with that click left out the second
// opens the URL cut at the pane's last row.
func TestACutURLOpensWhole(t *testing.T) {
	term, out, record, url := cutLinkSetup(t)

	col, row := mustFind(t, term, "FILL-001")
	if !gridMatchesAt(term.Screen(), col, row-1, "wwww") {
		t.Fatalf("the row above FILL-001 is not the URL's tail:\n%s", term.Snapshot())
	}
	if _, _, ok := findOnGrid(term.Screen(), "https://"); ok {
		t.Fatalf("the URL's head is on screen, so nothing cuts it:\n%s", term.Snapshot())
	}
	mouseClick(t, term, col+2, row-1, tuitest.MouseLeft, tuitest.ModCtrl)
	waitOpened(t, record, []string{url}, "ctrl+click on the tail of a URL whose head is in the scrollback")

	pc, pr := paneCell(t, term)
	bottom := paneBottomRow(t, term)
	mark := len(out.String())
	cut := false
	for range 30 {
		wheelAt(t, term, pc, pr, tuitest.MouseWheelUp, 1)
		if err := term.WaitStable(uiTimeout); err != nil {
			t.Fatalf("the screen never settled after a wheel notch: %v\n%s", err, term.Snapshot())
		}
		_, head, ok := findOnGrid(term.Screen(), "F: https://")
		if !ok {
			continue
		}
		if head+cutLinkRows-1 > bottom {
			cut = true
			break
		}
	}
	if !cut {
		t.Fatalf("scrolling back never left the URL's tail below the pane:\n%s", term.Snapshot())
	}
	col, row = mustFind(t, term, "F: https://")
	mouseClick(t, term, col+6, row, tuitest.MouseLeft, tuitest.ModCtrl)
	waitOpened(t, record, []string{url, url}, "ctrl+click on the head of a URL whose tail is below the pane")

	deadline := time.Now().Add(uiTimeout)
	for !bareLinkFrame(url).MatchString(out.String()[mark:]) {
		if time.Now().After(deadline) {
			t.Fatalf("no frame drawn while scrolled back carries the whole URL as OSC 8")
		}
		time.Sleep(50 * time.Millisecond)
	}
	if strings.Contains(term.Screen().Text(), "CUTDONE") {
		t.Errorf("the pane is not scrolled back:\n%s", term.Snapshot())
	}
	alive(t, term, "after opening cut links")
}
