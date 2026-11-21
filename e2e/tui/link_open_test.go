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

const (
	linkTarget    = "https://example.com/real-target"
	linkPlain     = "https://example.com/plain-url"
	linkSplit     = "https://example.com/split-target"
	linkForeign   = "file://far-away-host.invalid/etc/hostname"
	linkScript    = "javascript:alert(1)"
	linkLongPlain = "https://example.com/long/"
)

func linkScriptBody(longTail string) string {
	var b strings.Builder
	b.WriteString("#!/bin/sh\n")
	fmt.Fprintf(&b, "printf 'A: \\033]8;;%s\\033\\\\click here\\033]8;;\\033\\\\ end\\n'\n", linkTarget)
	fmt.Fprintf(&b, "printf 'B: see %s now\\n'\n", linkPlain)
	fmt.Fprintf(&b, "printf 'C: \\033]8;id=split1;%s\\007part-one\\033]8;;\\007 gap \\033]8;id=split1;%s\\007part-two\\033]8;;\\007\\n'\n", linkSplit, linkSplit)
	fmt.Fprintf(&b, "printf 'D: \\033]8;;%s\\033\\\\foreign file\\033]8;;\\033\\\\\\n'\n", linkForeign)
	fmt.Fprintf(&b, "printf 'E: \\033]8;;%s\\033\\\\script link\\033]8;;\\033\\\\\\n'\n", linkScript)
	fmt.Fprintf(&b, "printf 'F: %s%s\\n'\n", linkLongPlain, longTail)
	b.WriteString("printf 'LINKS''DONE\\n'\n")
	return b.String()
}

func linkOpenSetup(t *testing.T, extraConfig, longTail string) (*tuitest.Terminal, *lockedBuffer, string) {
	t.Helper()
	base := t.TempDir()
	record := filepath.Join(base, "opened.txt")
	opener := filepath.Join(base, "opener.sh")
	if err := os.WriteFile(opener, []byte("#!/bin/sh\nprintf '%s\\n' \"$1\" >> '"+record+"'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(base, "links.sh")
	if err := os.WriteFile(script, []byte(linkScriptBody(longTail)), 0o755); err != nil {
		t.Fatal(err)
	}
	writeConfig(t, base, fmt.Sprintf("[appearance]\nlink_opener = %q\n%s", opener, extraConfig))

	out := &lockedBuffer{}
	term := startIn(t, base, startOpts{out: out, env: []string{"SSH_CONNECTION=", "SSH_CLIENT=", "SSH_TTY="}})
	waitBoot(t, term)
	newWindow(t, term)
	waitWindowCount(t, term, 1, "opening a shell for the links")
	enterTerminalMode(t, term)
	runInShell(t, term, "sh "+script, "LINKSDONE", uiTimeout)
	leaveTerminalMode(t, term)
	if err := term.WaitStable(uiTimeout); err != nil {
		t.Fatalf("the screen never settled: %v\n%s", err, term.Snapshot())
	}
	return term, out, record
}

func openedLinks(record string) []string {
	data, err := os.ReadFile(record)
	if err != nil {
		return nil
	}
	return strings.Fields(string(data))
}

func waitOpened(t *testing.T, record string, want []string, what string) {
	t.Helper()
	deadline := time.Now().Add(uiTimeout)
	for time.Now().Before(deadline) {
		if got := openedLinks(record); strings.Join(got, "\n") == strings.Join(want, "\n") {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("%s: the opener was started with %q, want %q", what, openedLinks(record), want)
}

func mustFind(t *testing.T, term *tuitest.Terminal, text string) (int, int) {
	t.Helper()
	col, row, ok := findOnGrid(term.Screen(), text)
	if !ok {
		t.Fatalf("%q is not on screen:\n%s", text, term.Snapshot())
	}
	return col, row
}

// TestCtrlClickOpensLinksWithTheirTarget is the reported bug and the extra
// cases a link's text and target can disagree on.
//
// Negative controls, all confirmed red (see NEGATIVE_CONTROLS.md): with the
// ctrl branch of the link click disabled in handleMouseRelease, the first
// wait fails with the opener never started; with the OSC 8 transition
// disabled in the cell loop of renderTerminal (the old behaviour of a focused
// pane), every OSC 8 assertion on the frame fails; with linkFilePath taking
// any host as this machine, the foreign file link opens /etc/hostname in a
// new editor pane, which covers the fixture; with "javascript" added to
// linkOpenSchemes, the record holds the script address.
func TestCtrlClickOpensLinksWithTheirTarget(t *testing.T) {
	term, out, record := linkOpenSetup(t, "", strings.Repeat("w", 150))

	col, row := mustFind(t, term, "click here")
	mouseClick(t, term, col+3, row, tuitest.MouseLeft, tuitest.ModCtrl)
	waitOpened(t, record, []string{linkTarget}, "ctrl+click on an OSC 8 label")

	col, row = mustFind(t, term, linkPlain)
	mouseClick(t, term, col+5, row, tuitest.MouseLeft, tuitest.ModCtrl)
	waitOpened(t, record, []string{linkTarget, linkPlain}, "ctrl+click on a bare URL")

	col, row = mustFind(t, term, "part-two")
	mouseClick(t, term, col+1, row, tuitest.MouseLeft, tuitest.ModCtrl)
	waitOpened(t, record, []string{linkTarget, linkPlain, linkSplit}, "ctrl+click on the second run of an id= link")

	long := linkLongPlain + strings.Repeat("w", 150)
	col, row = mustFind(t, term, "F: "+linkLongPlain)
	mouseClick(t, term, col+3, row+1, tuitest.MouseLeft, tuitest.ModCtrl)
	waitOpened(t, record, []string{linkTarget, linkPlain, linkSplit, long}, "ctrl+click on the wrapped half of a bare URL")

	col, row = mustFind(t, term, "foreign file")
	mouseClick(t, term, col+2, row, tuitest.MouseLeft, tuitest.ModCtrl)
	col, row = mustFind(t, term, "script link")
	mouseClick(t, term, col+2, row, tuitest.MouseLeft, tuitest.ModCtrl)
	col, row = mustFind(t, term, linkPlain)
	mouseClick(t, term, col+5, row, tuitest.MouseLeft, tuitest.ModShift)
	waitOpened(t, record, []string{linkTarget, linkPlain, linkSplit, long, linkPlain},
		"a refused file and script link, then shift+click on a bare URL")
	if n := countWindows(term.Screen()); n != 1 {
		t.Errorf("a link opened a pane; there are %d panes, want 1:\n%s", n, term.Snapshot())
	}

	stream := out.String()
	for _, want := range []struct{ what, re string }{
		{"the labelled link", `\x1b\]8;[^;\x07\x1b]*;` + regexp.QuoteMeta(linkTarget) + `(\x07|\x1b\\)[^\x1b]*click`},
		{"the id= link", `\x1b\]8;id=split1;` + regexp.QuoteMeta(linkSplit) + `(\x07|\x1b\\)[^\x1b]*part-`},
		{"the bare URL", `\x1b\]8;id=tuios-[0-9a-z]+;` + regexp.QuoteMeta(linkPlain) + `(\x07|\x1b\\)`},
		{"the wrapped bare URL", `\x1b\]8;id=tuios-[0-9a-z]+;` + regexp.QuoteMeta(long) + `(\x07|\x1b\\)`},
	} {
		if !regexp.MustCompile(want.re).MatchString(stream) {
			t.Errorf("the frame sent to the outer terminal has no OSC 8 for %s", want.what)
		}
	}
	alive(t, term, "after opening links")
}

// TestHoverLightsEveryRunOfAnIDLink: hovering one run of an id= link
// underlines the other run as well, and the label names the target rather
// than the text.
//
// The pointer then goes from the link straight onto the rail, which takes
// the motion before the pane's hover is asked, and the label goes with it.
//
// Negative controls, confirmed red: with hoverByID forced false in
// renderTerminal, the wait for part-two's underline times out; without the
// DropLinkHover call on the sidebar branch of handleMouseMotion, the label
// stays up over the rail.
func TestHoverLightsEveryRunOfAnIDLink(t *testing.T) {
	term, _, _ := linkOpenSetup(t, "\n[appearance.sidebar]\nenabled = true\n", "x")

	col1, row := mustFind(t, term, "part-one")
	col2, _ := mustFind(t, term, "part-two")
	gap, _ := mustFind(t, term, " gap ")
	mouseHover(t, term, col1+2, row)
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return cellUnderlined(s, col1+2, row) && cellUnderlined(s, col2+2, row)
	}, uiTimeout); err != nil {
		t.Fatalf("hovering part-one did not underline part-two: %v\n%s", err, term.Snapshot())
	}
	if cellUnderlined(term.Screen(), gap+2, row) {
		t.Error("the gap between the two runs is underlined")
	}
	if err := term.WaitForText(linkSplit, uiTimeout); err != nil {
		t.Fatalf("the hover label does not name the target: %v\n%s", err, term.Snapshot())
	}
	if err := term.WaitForText("ctrl+click to open", uiTimeout); err != nil {
		t.Fatalf("the hover label does not name ctrl+click: %v\n%s", err, term.Snapshot())
	}

	railCol, railRow := mustFind(t, term, "local")
	mouseHover(t, term, railCol+1, railRow)
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return !strings.Contains(s.Text(), "ctrl+click to open") && !cellUnderlined(s, col1+2, row)
	}, uiTimeout); err != nil {
		t.Fatalf("the link label stayed up after the pointer moved onto the rail: %v\n%s", err, term.Snapshot())
	}
}

// TestLinkClickSettingIsHonoured: link_click = "ctrl" turns shift+click off,
// and ctrl+click still opens.
func TestLinkClickSettingIsHonoured(t *testing.T) {
	term, _, record := linkOpenSetup(t, "link_click = \"ctrl\"\n", "x")

	col, row := mustFind(t, term, linkPlain)
	mouseClick(t, term, col+5, row, tuitest.MouseLeft, tuitest.ModShift)
	mouseClick(t, term, col+5, row, tuitest.MouseLeft, tuitest.ModCtrl)
	waitOpened(t, record, []string{linkPlain}, "shift+click with link_click = ctrl, then ctrl+click")
}
