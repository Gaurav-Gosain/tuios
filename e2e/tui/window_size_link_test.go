package tuie2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
	"tuios.dev/shot"
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

// leftRailConfig is a rail on the left edge, width columns wide.
func leftRailConfig(width string) string {
	return "[appearance.sidebar]\nenabled = true\nposition = \"left\"\nwidth = " + width + "\n"
}

// TestWindowSizeLeftoverTakesNoClick clicks the dots a smaller session leaves
// on the larger client. A click there acts on nothing: the focus stays.
//
// The setup is the one where the dots cover layout columns that hold a pane.
// The 80x24 client has a rail on the left, so the session's panes start right
// of it in the layout. The 200x50 client draws them from its own left edge,
// and its dots start inside the columns the layout gives the panes. Before the
// fix the click went to the window manager with its screen column, which the
// layout read as a click on the second pane.
//
// The positive half: a click on each pane on the same screen moves the focus
// to it, so the focus is a thing these clicks can change.
//
// NEGATIVE CONTROL: with the PointerSwallowed check cut from HandleInput, the
// click on the dots moves the focus from the left pane to the right one.
func TestWindowSizeLeftoverTakesNoClick(t *testing.T) {
	home := t.TempDir()
	writeConfigIn(t, home, leftRailConfig("24"))
	big, _, base := windowSizePair(t, "smallest", rightRailConfig("28"), []string{"XDG_CONFIG_HOME=" + home})
	waitWSSize(t, base, wsSmallCols, wsSmallRows, "under smallest")
	rects, _ := focusedLayout(t, base, wsSession)
	if len(rects) != 2 {
		t.Fatalf("want two panes, got %+v", rects)
	}
	boxLeft := min(rects[0].X, rects[1].X)
	if boxLeft == 0 {
		t.Fatalf("the session's panes start at column 0, so the dots cover no pane column: %+v", rects)
	}
	// The focus goes to the left pane first. The dots cover layout columns
	// of the right pane, so a click that leaks through would move it there.
	left, right := rects[0], rects[1]
	if right.X < left.X {
		left, right = right, left
	}
	focusOn := func(r focusedRect, what string) {
		t.Helper()
		leftClick(t, big, r.X-boxLeft+2, r.Y+2)
		deadline := time.Now().Add(uiTimeout)
		for {
			if _, now := focusedLayout(t, base, wsSession); now == r.ID {
				return
			}
			if time.Now().After(deadline) {
				t.Fatalf("%s: a click on the pane never moved the focus to it\n%s", what, big.Snapshot())
			}
			time.Sleep(100 * time.Millisecond)
		}
	}
	focusOn(left, "the left pane")
	focused := left.ID

	// The first dot on a row inside the panes, and every dot after it that
	// is still inside the session's 80 columns.
	row := 5
	var dots []int
	waitSizeScreen(t, big, "the big client draws no dots", func(s tuitest.Screen) bool {
		dots = dots[:0]
		for x := range wsSmallCols {
			if s.Cell(x, row).Content == leftoverDot {
				dots = append(dots, x)
			}
		}
		return len(dots) > 0
	})
	saveSizeFrame(t, big, "before-clicks")
	for _, x := range dots {
		leftClick(t, big, x, row)
	}
	time.Sleep(time.Second)
	if _, now := focusedLayout(t, base, wsSession); now != focused {
		saveSizeFrame(t, big, "failed")
		t.Fatalf("a click on the dots at columns %v moved the focus from %s to %s", dots, focused, now)
	}

	// The positive half: a click inside the right pane, where the big client
	// draws it, moves the focus there.
	focusOn(right, "the right pane")
}

// TestWindowSizeOldClientChromeCounts runs a client from before ViewChrome
// beside a current one under latest. The old client is smaller and has a
// wider rail. It lays the panes out around its own rail whatever the session
// agrees, so the session has to agree a box that leaves its rail room, even
// though another client sets the size.
//
// TUIOS_VIEW_CHROME_LEGACY=1 makes this build behave as the old one.
// TUIOS_E2E_OLD_CLIENT names a real old build to use instead.
//
// NEGATIVE CONTROL: with the ViewChrome check cut from
// calculateSessionReserve, the session agrees the current client's 20 column
// reserve, the old client pushes its own 30, and the panes start at column 20
// while the old client draws them from 30.
func TestWindowSizeOldClientChromeCounts(t *testing.T) {
	base := t.TempDir()
	writeConfig(t, base, leftRailConfig("20")+"[startup]\nopen_default_window = true\ntiled = true\n")
	killDaemon(t, base)
	if out, err := tuiosCLI(t, base, "new", wsSession, "--detach"); err != nil {
		t.Fatalf("create session: %v: %s", err, out)
	}
	home := t.TempDir()
	writeConfigIn(t, home, leftRailConfig("30"))
	var old *tuitest.Terminal
	withBinary("TUIOS_E2E_OLD_CLIENT", func() {
		old = startIn(t, base, startOpts{cols: 120, rows: 40, args: []string{"attach", wsSession},
			env: []string{"XDG_CONFIG_HOME=" + home, "TUIOS_VIEW_CHROME_LEGACY=1"}})
	})
	waitSizeScreen(t, old, "the old client never drew its rail", func(s tuitest.Screen) bool {
		return strings.Contains(s.Text(), "sessions")
	})
	// The current client takes the session before it is settled in window
	// management mode, and the order matters. Under latest the first key a
	// client sends makes it the size, so attachIn's Alt+Esc would be that key:
	// the "Session size" notice the resize brings lands just after the mode
	// notice, the dock shows the newer one, and the mode notice was on screen
	// only when a frame happened to fall between the two.
	cur := startIn(t, base, startOpts{cols: 160, rows: 45, args: []string{"attach", wsSession}})
	if err := cur.WaitFor(func(s tuitest.Screen) bool {
		return countWindows(s) >= 0
	}, bootTimeout); err != nil {
		t.Fatalf("client never rehydrated session %q: %v\n%s", wsSession, err, cur.Snapshot())
	}
	time.Sleep(wsHoldGap)
	activity(t, cur)
	waitWSSize(t, base, 160, 45, "after input on the current client")
	// The daemon answering 160x45 does not mean this client has drawn it yet.
	// Its notice is the resize reaching the client, after which the mode
	// notice is the newer one.
	if err := cur.WaitForText("Session size: 160x45", uiTimeout); err != nil {
		t.Fatalf("the current client never took the session's new size: %v\n%s", err, cur.Snapshot())
	}
	windowManagementMode(t, cur)
	enableTiling(t, cur)
	newWindow(t, cur)
	waitWindowCount(t, cur, 2, "two windows on the current client")

	// The panes start right of the old client's 30 column rail, and stay
	// there: sampled over two seconds, since the bug was a layout the two
	// clients kept pushing at each other.
	for i := range 8 {
		time.Sleep(250 * time.Millisecond)
		rects, _ := focusedLayout(t, base, wsSession)
		left := 1 << 30
		for _, r := range rects {
			left = min(left, r.X)
		}
		if left != 30 {
			saveSizeFrame(t, old, "old-client")
			saveSizeFrame(t, cur, "current-client")
			t.Fatalf("sample %d: the session's panes start at column %d, want 30, the old client's rail: %+v", i, left, rects)
		}
	}
	saveSizeFrame(t, old, "old-client")
	saveSizeFrame(t, cur, "current-client")
}

// TestWindowSizeRailOverLeftoverTakesClicks opens a hidden right rail for the
// keyboard on the larger client. That rail is out of the reserve, so it is
// drawn over the dots. A click on its row for the other pane moves the focus
// there: the dots under the rail do not take the click.
//
// NEGATIVE CONTROL: with the SidebarBandContains check cut from
// pointerOverLeftover, the click is swallowed and the focus stays.
func TestWindowSizeRailOverLeftoverTakesClicks(t *testing.T) {
	cfg := "[appearance.sidebar]\nenabled = false\nposition = \"right\"\nwidth = 28\n"
	big, _, base := windowSizePair(t, "smallest", cfg, nil)
	waitWSSize(t, base, wsSmallCols, wsSmallRows, "under smallest")
	_, focused := focusedLayout(t, base, wsSession)
	if focused == "" {
		t.Fatal("no pane is focused")
	}
	if err := big.SendKeys(tuitest.Ctrl('b'), "e"); err != nil {
		t.Fatalf("open the rail: %v", err)
	}
	// The row of the pane that is not focused, in the rail at the right edge.
	railX := wsBigCols - linkRail
	row, col := -1, -1
	waitSizeScreen(t, big, "the rail never opened with both terminal rows", func(s tuitest.Screen) bool {
		row, col = -1, -1
		n := 0
		for y := range wsBigRows {
			line := []rune(s.Line(y))
			if len(line) <= railX {
				continue
			}
			tail := string(line[railX:])
			i := strings.Index(tail, "Terminal ")
			if i < 0 {
				continue
			}
			n++
			id := strings.Fields(tail[i+len("Terminal "):])
			if len(id) > 0 && !strings.HasPrefix(focused, id[0]) {
				row, col = y, railX+len([]rune(tail[:i]))+2
			}
		}
		return n == 2 && row >= 0
	})
	saveSizeFrame(t, big, "rail-over-dots")
	leftClick(t, big, col, row)
	deadline := time.Now().Add(uiTimeout)
	for {
		if _, now := focusedLayout(t, base, wsSession); now != focused {
			return
		}
		if time.Now().After(deadline) {
			saveSizeFrame(t, big, "failed")
			t.Fatalf("a click on the rail's row at %d,%d over the dots never moved the focus\n%s", col, row, big.Snapshot())
		}
		time.Sleep(100 * time.Millisecond)
	}
}
