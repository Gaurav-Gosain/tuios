package tuie2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/shot"
	"github.com/Gaurav-Gosain/tuitest"
)

// Two clients of different sizes on one session, each with a rail on the
// right edge, drawn the way the report saw it: a laptop attached over a host
// link, and the desktop the session's daemon runs on attached locally.
//
// The report: the desktop, the wider client, showed its panes ending three
// columns before its rail, and a second blank strip after the rail, with
// [daemon] window_size = "latest" already set.
//
// What was wrong, in order of the columns:
//   - The three columns. The daemon laid the panes out around the largest
//     chrome reserve any client asked for, so the laptop's 31 column rail
//     took 31 columns off the desktop's layout, which only draws a 28 column
//     rail. Under latest the reserve is now the latest client's own.
//   - The strip after the rail. Until the desktop has input the laptop is
//     the latest client (an attach does not take the size, see
//     window_size.go), and the desktop drew all of its chrome inside the
//     laptop's size: the rail sat just past the panes, and the rest of the
//     screen was blank. The rail and the dock now sit at the client's own
//     edges, and the space between is a dotted pattern with a line that
//     names the laptop.
//
// How these tests could pass wrongly, written down first:
//   - The rail could be found at the right edge because the session is the
//     desktop's size. The first stage checks the rail while the session is
//     the laptop's 140 columns, read from session-info.
//   - A pane border could be mistaken for the rail's. The rail is found by
//     its header text, on the row where it starts, and the column checked is
//     the rail's first.
//   - The pattern could be the shell's output. The pane prints nothing like
//     it, and the dots are checked in the columns past the session's width.
//   - The hint could name the wrong client. The laptop runs in a user and UTS
//     namespace whose host name is "macbook", when unshare can make one, and
//     the hint has to say that name.

const (
	linkRail        = 28
	linkMacRail     = 31
	linkMacCols     = 140
	linkMacRows     = 40
	linkDeskCols    = 200
	linkDeskRows    = 50
	linkMacHostName = "macbook"
	leftoverDot     = "·"
	leftoverHint    = "sets the session size"
)

// rightRailConfig is a rail on the right edge, width columns wide.
func rightRailConfig(width string) string {
	return "[appearance.sidebar]\nenabled = true\nposition = \"right\"\nwidth = " + width + "\n"
}

// railLeft is the first column of the right rail: the column of the border
// on the row whose text holds the rail's "sessions" header. -1 when no rail
// is drawn.
func railLeft(s tuitest.Screen) int {
	_, rows := s.Size()
	for y := range rows {
		line := []rune(s.Line(y))
		i := strings.Index(string(line), "│ sessions")
		if i < 0 {
			continue
		}
		return len([]rune(string(line)[:i]))
	}
	return -1
}

// renamedHostBinary is a wrapper that runs the tuios binary in a user and
// UTS namespace whose host name is name, or "" when unshare cannot make one
// on this machine.
func renamedHostBinary(t *testing.T, name string) string {
	t.Helper()
	unshare, err := exec.LookPath("unshare")
	if err != nil {
		return ""
	}
	if out, err := exec.Command(unshare, "-ru", "sh", "-c", "hostname "+name+" && hostname").CombinedOutput(); err != nil || strings.TrimSpace(string(out)) != name {
		return ""
	}
	path := filepath.Join(t.TempDir(), "tuios-on-"+name)
	body := "#!/bin/sh\nexec " + unshare + " -ru sh -c 'hostname " + name + " && exec \"$0\" \"$@\"' " + tuiosBin + " \"$@\"\n"
	if err := os.WriteFile(path, []byte(body), 0o700); err != nil {
		t.Fatalf("write the host name wrapper: %v", err)
	}
	return path
}

// saveSizeFrame keeps the screen as text and as a PNG.
func saveSizeFrame(t *testing.T, term *tuitest.Terminal, name string) {
	t.Helper()
	dir := artifactDir(t)
	saveArtifact(t, term, dir, name)
	savePNG(t, term.Screen(), shot.XTermPalette(), dir, name)
}

// waitSizeScreen waits for cond on the screen. On a timeout it keeps the
// screen as "failed" beside the other frames, and fails with it.
func waitSizeScreen(t *testing.T, term *tuitest.Terminal, what string, cond func(tuitest.Screen) bool) {
	t.Helper()
	if err := term.WaitFor(cond, uiTimeout); err != nil {
		saveSizeFrame(t, term, "failed")
		t.Fatalf("%s: %v\n%s", what, err, term.Snapshot())
	}
}

// TestWindowSizeLinkClientsUseTheirOwnEdges is the report's setup: a
// session on one machine, a laptop attached over a link at 140x40 with a 31
// column rail, then the desktop attached locally at 200x50 with a 28 column
// rail. It runs under the default policy and under window_size = "latest"
// set in the file, which is what the report had.
//
// NEGATIVE CONTROL: on origin/main the default run fails on the policy
// (smallest), and the latest run fails in the first stage, with the
// desktop's rail at column 112 inside the laptop's 140 columns. With the
// daemon's reserve the largest again under latest, the second stage fails
// with three blank columns between the panes and the rail.
func TestWindowSizeLinkClientsUseTheirOwnEdges(t *testing.T) {
	t.Run("default", func(t *testing.T) { linkClientsUseTheirOwnEdges(t, "") })
	t.Run("latest", func(t *testing.T) { linkClientsUseTheirOwnEdges(t, "[daemon]\nwindow_size = \"latest\"\n") })
}

func linkClientsUseTheirOwnEdges(t *testing.T, policy string) {
	desk := remoteMachine(t)
	writeConfig(t, desk, policy+rightRailConfig("28"))
	laptop := t.TempDir()
	ssh := writeFakeSSHTo(t, laptop, desk)
	writeConfig(t, laptop, rightRailConfig("31")+
		"[hosts.build]\naddr = \"someone@buildbox\"\ncommand = \""+tuiosBin+"\"\nconnect_timeout = 5\n")
	if out, err := tuiosCLI(t, desk, "new", wsSession, "--detach"); err != nil {
		t.Fatalf("create the session: %v\n%s", err, out)
	}

	wrapped := renamedHostBinary(t, linkMacHostName)
	var mac *tuitest.Terminal
	attachMac := func() {
		mac = startIn(t, laptop, startOpts{cols: linkMacCols, rows: linkMacRows,
			args: []string{"attach", "--host", "build", wsSession}, env: []string{"TUIOS_SSH=" + ssh}})
	}
	if wrapped != "" {
		prev := tuiosBin
		tuiosBin = wrapped
		attachMac()
		tuiosBin = prev
	} else {
		t.Log("unshare cannot rename the host here: the hint is checked for the sentence, not the name")
		attachMac()
	}
	waitSizeScreen(t, mac, "the laptop never drew the session with its rail", func(s tuitest.Screen) bool {
		return railLeft(s) == linkMacCols-linkMacRail && countWindows(s) == 1
	})
	windowManagementMode(t, mac)
	newWindow(t, mac)
	waitWindowCount(t, mac, 2, "two windows on the laptop")
	enableTiling(t, mac)

	// startIn, not attachIn: attachIn waits for the dock on the last rows,
	// which is the thing under test.
	deskTerm := startIn(t, desk, startOpts{cols: linkDeskCols, rows: linkDeskRows, args: []string{"attach", wsSession}})
	waitSizeScreen(t, deskTerm, "the desktop never drew the session", func(s tuitest.Screen) bool {
		return railLeft(s) >= 0
	})
	saveSizeFrame(t, deskTerm, "desktop-attached")
	if w, h, policy := wsSize(t, desk); policy != "latest" || w != linkMacCols || h != linkMacRows {
		t.Fatalf("with the laptop latest the session is %dx%d under %s, want %dx%d under latest", w, h, policy, linkMacCols, linkMacRows)
	}

	// Stage 1: the laptop sets the size. The desktop's rail is at its own
	// right edge, and the space the session does not reach is the pattern
	// and the hint, naming the laptop.
	waitSizeScreen(t, deskTerm, "the desktop's rail is not at its right edge while the laptop sets the size", func(s tuitest.Screen) bool {
		return railLeft(s) == linkDeskCols-linkRail
	})
	wantHint := leftoverHint
	if wrapped != "" {
		wantHint = "client on " + linkMacHostName + " " + leftoverHint
	}
	waitSizeScreen(t, deskTerm, "the desktop does not fill the space past the session", func(s tuitest.Screen) bool {
		dotted := false
		for y := range linkMacRows - 2 {
			line := []rune(s.Line(y))
			if len(line) > linkMacCols+4 && strings.Contains(string(line[linkMacCols:min(len(line), linkDeskCols-linkRail)]), leftoverDot) {
				dotted = true
			}
		}
		return dotted && strings.Contains(s.Text(), wantHint)
	})
	saveSizeFrame(t, deskTerm, "desktop-laptop-sets-size")

	// Stage 2: input on the desktop takes the size. Its panes reach its own
	// rail: the column before the rail is a pane's border, not blank.
	time.Sleep(wsHoldGap)
	activity(t, deskTerm)
	waitWSSize(t, desk, linkDeskCols, linkDeskRows, "after input on the desktop")
	waitSizeScreen(t, deskTerm, "the desktop's panes do not reach its rail", func(s tuitest.Screen) bool {
		rail := railLeft(s)
		return rail == linkDeskCols-linkRail &&
			isWindowBorder(s.Cell(rail-1, linkDeskRows/2).Rune) &&
			!strings.Contains(s.Text(), leftoverHint)
	})
	saveSizeFrame(t, deskTerm, "desktop-sets-size")

	// The laptop shows part of the session, with its own rail at its own edge.
	waitMark(t, mac, true, "the laptop, with the desktop latest")
	waitSizeScreen(t, mac, "the laptop's rail is not at its right edge", func(s tuitest.Screen) bool {
		return railLeft(s) == linkMacCols-linkMacRail
	})
	saveSizeFrame(t, mac, "laptop-part-of-session")
}

// TestWindowSizeSmallestFillsTheLeftover is the same space under smallest,
// set on purpose: an 80x24 client sets the size, and the 200x50 one runs at
// 16 colours. Its rail and dock are at its own edges, and the space between
// is the pattern and a hint that says what to change.
//
// At 16 colours every ground is unset, so the pattern has to be ink, not a
// fill: each dot is checked for an ANSI foreground and no background.
//
// NEGATIVE CONTROL: on origin/main the rail is at column 52, inside the
// small client's 80 columns, and no dot is drawn.
func TestWindowSizeSmallestFillsTheLeftover(t *testing.T) {
	big, small, base := windowSizePair(t, "smallest", rightRailConfig("28"), nil, "TERM=xterm", "COLORTERM=")
	waitWSSize(t, base, wsSmallCols, wsSmallRows, "under smallest")
	waitSizeScreen(t, small, "the small client never drew the session", func(s tuitest.Screen) bool {
		return railLeft(s) >= 0
	})
	time.Sleep(time.Second)
	saveSizeFrame(t, big, "smallest-big-attached")
	waitSizeScreen(t, big, "the big client's rail is not at its right edge", func(s tuitest.Screen) bool {
		return railLeft(s) == wsBigCols-linkRail
	})
	waitSizeScreen(t, big, "the big client does not say what sets the size", func(s tuitest.Screen) bool {
		return strings.Contains(s.Text(), leftoverHint) && strings.Contains(s.Text(), "set window_size to latest")
	})
	s := big.Screen()
	dots := 0
	for y := range wsBigRows - 2 {
		for x := wsSmallCols; x < wsBigCols-linkRail; x++ {
			c := s.Cell(x, y)
			if c.Content != leftoverDot {
				continue
			}
			dots++
			if c.Bg.Kind != tuitest.ColorDefault || c.Fg.Kind != tuitest.ColorIndexed || c.Fg.Index >= 16 {
				t.Fatalf("the dot at %d,%d has ink %+v on ground %+v, want an ANSI colour on no ground", x, y, c.Fg, c.Bg)
			}
		}
	}
	if dots == 0 {
		t.Fatalf("no dot in the space past the session\n%s", big.Snapshot())
	}
	// The dock is at the client's bottom edge, not under the session.
	if line := s.Line(wsBigRows - 2); !strings.Contains(line, "────") {
		t.Fatalf("the dock's rule is not on the big client's last rows: %q\n%s", line, big.Snapshot())
	}
	saveSizeFrame(t, big, "smallest-big-16-colours")
	saveSizeFrame(t, small, "smallest-small")
}
