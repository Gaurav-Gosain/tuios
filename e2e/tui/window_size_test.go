package tuie2e

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// The window_size policy (internal/session/window_size.go), driven through
// two real clients of different sizes on one daemon, asserted on what each
// client draws and on the size the daemon settled on.
//
// The latest client is decided by input, and a client keeps the session for
// one second after its own last input (latestHold). Every step that hands the
// session over waits out that hold first, so the step tests the hand-over
// and not the hold.

const (
	wsSession            = "ws"
	wsBigCols, wsBigRows = 200, 50
	wsSmallCols          = 80
	wsSmallRows          = 24
	// wsHoldGap is longer than latestHold, with room for the round trip.
	wsHoldGap = 1500 * time.Millisecond
	// wsMark is the text of the mark a client shows over a view of a larger
	// session.
	wsMark = "Part of session"
	// wsLegacyEnv makes a client behave as a build from before window_size.
	// TUIOS_E2E_OLD_CLIENT names such a build to use instead.
	wsLegacyEnv = "TUIOS_WINDOW_SIZE_LEGACY=1"
)

// wsSize is the size the daemon settled on, and the policy it used.
func wsSize(t *testing.T, base string) (w, h int, policy string) {
	t.Helper()
	out, err := tuiosCLI(t, base, "session-info", "-s", wsSession, "--json")
	if err != nil {
		t.Fatalf("session-info: %v\n%s", err, out)
	}
	var res struct {
		W      int    `json:"session_width"`
		H      int    `json:"session_height"`
		Policy string `json:"window_size"`
	}
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("session-info --json printed %q: %v", out, err)
	}
	return res.W, res.H, res.Policy
}

// waitWSSize waits for the daemon to settle the session at w x h.
func waitWSSize(t *testing.T, base string, w, h int, what string) {
	t.Helper()
	deadline := time.Now().Add(uiTimeout)
	var gw, gh int
	var policy string
	for time.Now().Before(deadline) {
		if gw, gh, policy = wsSize(t, base); gw == w && gh == h {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("%s: the session is %dx%d under %s, want %dx%d", what, gw, gh, policy, w, h)
}

// waitMark waits for the client to show, or not show, the mark of a view of
// a larger session.
func waitMark(t *testing.T, term *tuitest.Terminal, shown bool, what string) {
	t.Helper()
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return strings.Contains(s.Text(), wsMark) == shown
	}, uiTimeout); err != nil {
		t.Fatalf("%s: the mark shown is %v, want %v\n%s", what, !shown, shown, term.Snapshot())
	}
}

// activity is a key press: Esc is a key in either mode, and it changes
// nothing in the window manager or in a shell.
func activity(t *testing.T, term *tuitest.Terminal) {
	t.Helper()
	if err := term.SendKeys(tuitest.Esc); err != nil {
		t.Fatalf("send a key: %v", err)
	}
}

// windowSizePair makes a session with two side by side panes, set to the
// policy, and attaches a 200x50 client and then an 80x24 one. extraConfig is
// added to the config file, and smallEnv and bigEnv to each client's
// environment.
func windowSizePair(t *testing.T, policy, extraConfig string, smallEnv []string, bigEnv ...string) (big, small *tuitest.Terminal, base string) {
	t.Helper()
	base = t.TempDir()
	cfg := extraConfig
	if policy != "" {
		cfg = fmt.Sprintf("[daemon]\nwindow_size = %q\n", policy) + cfg
	}
	writeConfig(t, base, strings.ReplaceAll(cfg, "{BASE}", base))
	killDaemon(t, base)
	if out, err := tuiosCLI(t, base, "new", wsSession, "--detach"); err != nil {
		t.Fatalf("create session: %v: %s", err, out)
	}
	big = attachIn(t, base, wsSession, startOpts{cols: wsBigCols, rows: wsBigRows, env: bigEnv})
	newWindow(t, big)
	waitWindowCount(t, big, 2, "two windows on the big client")
	enableTiling(t, big)
	attach := func() {
		small = attachSmall(t, base, wsSession, startOpts{cols: wsSmallCols, rows: wsSmallRows, env: smallEnv})
	}
	if slices.Contains(smallEnv, wsLegacyEnv) {
		// A real build from before window_size, when one is named.
		withBinary("TUIOS_E2E_OLD_CLIENT", attach)
	} else {
		attach()
	}
	return big, small, base
}

// TestWindowSizeLatestFollowsInput is the report: the session takes the size
// of the client that last had input, both ways.
//
// NEGATIVE CONTROL: with handleClientActivity returning before it recalculates,
// the session stays at the big client's size when the small client types, and
// the second stage fails.
func TestWindowSizeLatestFollowsInput(t *testing.T) {
	big, small, base := windowSizePair(t, "latest", "", nil)

	// The big client attached first and nobody else has typed, so it is the
	// latest. The small client shows part of the session, with the mark.
	waitWSSize(t, base, wsBigCols, wsBigRows, "with the big client latest")
	waitMark(t, small, true, "the small client, with the big client latest")
	waitSpanRight(t, big, wsBigCols, "the big client, with itself latest")
	saveArtifact(t, small, artifactDir(t), "small-cropped")

	// Input in the small client hands it the session: 80x24 on both.
	time.Sleep(wsHoldGap)
	activity(t, small)
	waitWSSize(t, base, wsSmallCols, wsSmallRows, "after input in the small client")
	waitSpanRight(t, big, wsSmallCols, "the big client, with the small one latest")
	waitMark(t, small, false, "the small client, with itself latest")
	waitMark(t, big, false, "the big client, larger than the session")
	saveArtifact(t, big, artifactDir(t), "big-letterboxed")

	// Input in the big client takes it back.
	time.Sleep(wsHoldGap)
	activity(t, big)
	waitWSSize(t, base, wsBigCols, wsBigRows, "after input in the big client")
	waitSpanRight(t, big, wsBigCols, "the big client, latest again")
	waitMark(t, small, true, "the small client, after the big one took the session back")
}

// TestWindowSizeSmallestIgnoresInput holds the default: the session is the
// smallest client's size whoever types. Then set-config changes the policy of
// the running session.
//
// NEGATIVE CONTROL: with sessionWindowSize returning latest whatever the
// option says, the session is the big client's 200x50 (it attached first)
// and the first check fails.
func TestWindowSizeSmallestIgnoresInput(t *testing.T) {
	big, small, base := windowSizePair(t, "", "", nil)
	waitWSSize(t, base, wsSmallCols, wsSmallRows, "with both attached")
	if _, _, policy := wsSize(t, base); policy != "smallest" {
		t.Fatalf("the default policy is %q, want smallest", policy)
	}
	for _, term := range []*tuitest.Terminal{big, small, big} {
		time.Sleep(wsHoldGap)
		activity(t, term)
		time.Sleep(wsHoldGap)
		if w, h, _ := wsSize(t, base); w != wsSmallCols || h != wsSmallRows {
			t.Fatalf("after input the session is %dx%d, want %dx%d", w, h, wsSmallCols, wsSmallRows)
		}
	}
	waitSpanRight(t, big, wsSmallCols, "the big client under smallest")
	waitMark(t, small, false, "the small client under smallest")

	// The policy changes at run time, for this session, with no input.
	if out, err := tuiosCLI(t, base, "set-config", "daemon.window_size", "largest", "-s", wsSession); err != nil {
		t.Fatalf("set-config daemon.window_size: %v\n%s", err, out)
	}
	waitWSSize(t, base, wsBigCols, wsBigRows, "after set-config daemon.window_size largest")
	waitMark(t, small, true, "the small client under largest")
}

// TestWindowSizeSwitchToLatestStartsFromInput changes a session to latest at
// run time. A client reports input only under latest, so the switch has to
// give the daemon a starting point: the client that had input just before,
// which is where the person who switched it is.
//
// The big client types and then the policy changes. The small client
// attached last, which is the daemon's choice when no client has reported
// input, so a switch with no starting point settles at 80x24.
//
// NEGATIVE CONTROL: with the policy change case removed from reportActivity,
// the session goes to the small client's 80x24 and the test fails.
func TestWindowSizeSwitchToLatestStartsFromInput(t *testing.T) {
	big, small, base := windowSizePair(t, "largest", "", nil)
	waitWSSize(t, base, wsBigCols, wsBigRows, "under largest")
	// The daemon names the policy to the other clients when one resizes the
	// session. Until then a client does not know it and reports all input.
	// The small client grows past the big one and back, so the big client
	// learns the policy is largest.
	for _, sz := range [][2]int{{wsBigCols + 1, wsSmallRows}, {wsSmallCols, wsSmallRows}} {
		if err := small.Resize(sz[0], sz[1]); err != nil {
			t.Fatalf("resize the small client: %v", err)
		}
		waitWSSize(t, base, max(sz[0], wsBigCols), wsBigRows, "after a resize of the small client")
	}
	waitMark(t, small, true, "the small client under largest")

	time.Sleep(wsHoldGap)
	activity(t, big)
	// A lone Esc reaches the client after its escape timeout. The switch
	// waits for it, so the key is handled under largest, and still well
	// inside the time a switch carries input over.
	time.Sleep(500 * time.Millisecond)
	if out, err := tuiosCLI(t, base, "set-config", "daemon.window_size", "latest", "-s", wsSession); err != nil {
		t.Fatalf("set-config daemon.window_size: %v\n%s", err, out)
	}
	deadline := time.Now().Add(uiTimeout)
	for {
		if _, _, policy := wsSize(t, base); policy == "latest" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the policy never changed to latest")
		}
		time.Sleep(100 * time.Millisecond)
	}
	// Past the hold, so a switch to the client that attached last would have
	// landed.
	time.Sleep(wsHoldGap)
	if w, h, _ := wsSize(t, base); w != wsBigCols || h != wsBigRows {
		t.Fatalf("after the switch to latest the session is %dx%d, want the big client's %dx%d", w, h, wsBigCols, wsBigRows)
	}
	saveArtifact(t, small, artifactDir(t), "switch-to-latest-small")

	// From there input moves it as usual.
	activity(t, small)
	waitWSSize(t, base, wsSmallCols, wsSmallRows, "after input in the small client")
}

// TestWindowSizeLatestDebounce types in both clients in turn, faster than
// the hold. The session must not move while they alternate, and must go to
// the one that typed last once the other one stops.
//
// NEGATIVE CONTROL: with latestHold set to 0 the session moves on every
// press, and the size log records both sizes during the alternation.
func TestWindowSizeLatestDebounce(t *testing.T) {
	big, small, base := windowSizePair(t, "latest", "", nil)
	waitWSSize(t, base, wsBigCols, wsBigRows, "with the big client latest")
	time.Sleep(wsHoldGap)

	stop := make(chan struct{})
	sizes := make(chan string, 1)
	go func() {
		seen := map[string]bool{}
		for {
			select {
			case <-stop:
				var list []string
				for s := range seen {
					list = append(list, s)
				}
				slices.Sort(list)
				sizes <- strings.Join(list, " ")
				return
			default:
			}
			if out, err := tuiosCLI(t, base, "session-info", "-s", wsSession, "--json"); err == nil {
				var res struct {
					W int `json:"session_width"`
					H int `json:"session_height"`
				}
				if json.Unmarshal([]byte(out), &res) == nil {
					seen[fmt.Sprintf("%dx%d", res.W, res.H)] = true
				}
			}
			time.Sleep(50 * time.Millisecond)
		}
	}()
	// Twenty presses, 150 ms apart, the small client last: each client's
	// input is at most 300 ms old when the other one types.
	for i := range 20 {
		if i%2 == 0 {
			activity(t, big)
		} else {
			activity(t, small)
		}
		time.Sleep(150 * time.Millisecond)
	}
	close(stop)
	if got, want := <-sizes, fmt.Sprintf("%dx%d", wsBigCols, wsBigRows); got != want {
		t.Fatalf("while both clients typed the session was %s, want only %s: the layout flapped", got, want)
	}
	// The small client typed last. Once the big client's hold runs out, the
	// daemon hands it the session with no further input.
	waitWSSize(t, base, wsSmallCols, wsSmallRows, "after the alternation, with the small client last")
}

// TestWindowSizeViewFollowsCursorAndClicks drives a small client that shows
// part of a larger session. Its view has to follow the focused pane's cursor,
// and a click in it has to land on the pane drawn under the pointer.
//
// NEGATIVE CONTROLS: with computeSessionView ignoring the cursor (the offset
// left at 0), the right pane never comes into view and the second stage
// fails. With MapPointer returning the event unchanged, the click lands on
// the left pane, which is under the same screen column in the layout, and
// the focus never moves.
func TestWindowSizeViewFollowsCursorAndClicks(t *testing.T) {
	// largest keeps the session at the big client's size while the small
	// client is clicked, which is the whole point here.
	_, small, base := windowSizePair(t, "largest", "", nil)
	waitWSSize(t, base, wsBigCols, wsBigRows, "under largest")
	waitMark(t, small, true, "the small client under largest")

	left, right := viewOverTheSplit(t, base, small)

	// A click on the right pane, as the small client draws it.
	row, col := findText(t, small, "RIGHTSIDE")
	if col >= wsSmallCols || focusedWindowID(t, base, wsSession) != left {
		t.Fatalf("the fixture is wrong: RIGHTSIDE at column %d, focus on %s", col, focusedWindowID(t, base, wsSession))
	}
	leftClick(t, small, col+2, row)
	deadline := time.Now().Add(uiTimeout)
	for focusedWindowID(t, base, wsSession) != right {
		if time.Now().After(deadline) {
			t.Fatalf("a click on the right pane in the small client did not focus it\n%s", small.Snapshot())
		}
		time.Sleep(100 * time.Millisecond)
	}
	if w, h, _ := wsSize(t, base); w != wsBigCols || h != wsBigRows {
		t.Fatalf("under largest the session moved to %dx%d", w, h)
	}
	saveArtifact(t, small, artifactDir(t), "view-after-click")
}

// viewOverTheSplit puts the small client's view over the split between the
// two panes, with the focus on the left one. The right pane prints RIGHTSIDE,
// then the left pane's cursor is moved near its right edge by a long line of
// L, and the view follows it. On the way it checks that the view starts at
// the left edge while the cursor is there. It returns the two panes.
func viewOverTheSplit(t *testing.T, base string, small *tuitest.Terminal) (left, right string) {
	t.Helper()
	left, right = sideBySide(t, base)
	if out, err := tuiosCLI(t, base, "send-text", "-s", wsSession, "-w", right, "echo RIGHT''SIDE\n"); err != nil {
		t.Fatalf("send-text right: %v\n%s", err, out)
	}
	if out, err := tuiosCLI(t, base, "focus-window", "-s", wsSession, left); err != nil {
		t.Fatalf("focus-window left: %v\n%s", err, out)
	}
	// The cursor is at the start of the left pane: the view starts at the
	// left edge, and the right pane is out of it.
	if err := small.WaitFor(func(s tuitest.Screen) bool {
		return !strings.Contains(s.Text(), "RIGHTSIDE") && strings.Contains(s.Text(), wsMark) &&
			!strings.Contains(s.Text(), "←")
	}, uiTimeout); err != nil {
		t.Fatalf("with the cursor at the left the view does not start at the left edge\n%s", small.Snapshot())
	}
	saveArtifact(t, small, artifactDir(t), "view-left")

	// A long line moves the cursor near the left pane's right edge. The view
	// follows it, and the start of the right pane comes into view.
	line := "# " + strings.Repeat("L", 80)
	if out, err := tuiosCLI(t, base, "send-text", "-s", wsSession, "-w", left, line); err != nil {
		t.Fatalf("send-text left: %v\n%s", err, out)
	}
	if err := small.WaitFor(func(s tuitest.Screen) bool {
		return strings.Contains(s.Text(), "RIGHTSIDE") && strings.Contains(s.Text(), "←")
	}, uiTimeout); err != nil {
		t.Fatalf("the view never followed the cursor to the right\n%s", small.Snapshot())
	}
	saveArtifact(t, small, artifactDir(t), "view-follows-cursor")
	return left, right
}

// sideBySide returns the two panes of the session, left first.
func sideBySide(t *testing.T, base string) (left, right string) {
	t.Helper()
	out, err := tuiosCLI(t, base, "list-windows", "-s", wsSession, "--json")
	if err != nil {
		t.Fatalf("list-windows: %v\n%s", err, out)
	}
	var list struct {
		Windows []struct {
			ID string `json:"window_id"`
			X  int    `json:"x"`
		} `json:"windows"`
	}
	if err := json.Unmarshal([]byte(out), &list); err != nil || len(list.Windows) != 2 {
		t.Fatalf("list-windows --json printed %q (%v), want two windows", out, err)
	}
	a, b := list.Windows[0], list.Windows[1]
	if a.X > b.X {
		a, b = b, a
	}
	if b.X < wsSmallCols/2 {
		t.Fatalf("the panes are not side by side: x %d and %d", a.X, b.X)
	}
	return a.ID, b.ID
}

// TestWindowSizeLegacyClient attaches a small client that says nothing about
// window_size, as a build from before it would. TUIOS_E2E_OLD_CLIENT runs a
// real older build as that client. The session then keeps the
// smallest client's size whoever types, and that client draws the whole
// session with no mark.
//
// NEGATIVE CONTROL: with effectiveWindowSize ignoring the capability, the
// big client's input grows the session to 200x50 and the old client, which
// lays its panes out in its own 80 columns, draws them cut off.
func TestWindowSizeLegacyClient(t *testing.T) {
	big, small, base := windowSizePair(t, "latest", "", []string{wsLegacyEnv})
	waitWSSize(t, base, wsSmallCols, wsSmallRows, "with an old client attached")
	if _, _, policy := wsSize(t, base); policy != "smallest" {
		t.Fatalf("with an old client attached the policy in force is %q, want smallest", policy)
	}
	time.Sleep(wsHoldGap)
	activity(t, big)
	time.Sleep(wsHoldGap)
	if w, h, _ := wsSize(t, base); w != wsSmallCols || h != wsSmallRows {
		t.Fatalf("an old client is attached and the session moved to %dx%d", w, h)
	}
	waitSpanRight(t, small, wsSmallCols, "the old client's own frame")
	waitMark(t, small, false, "the old client")
	saveArtifact(t, small, artifactDir(t), "legacy-small")
}

// TestWindowSizeRailStaysWhole shows a small client a session larger than
// itself with the rail on. The rail and the dock are drawn at the small
// client's own size, whole, and the panes are cut beside them.
//
// NEGATIVE CONTROLS: with ViewUsableHeight returning the layout height, the
// rail is laid out the session's 48 rows tall and its footer is drawn off the
// bottom of the screen. With the mark back at the top of the pane area, row
// 0 carries it and the rule does not.
func TestWindowSizeRailStaysWhole(t *testing.T) {
	_, small, base := windowSizePair(t, "largest", "[appearance.sidebar]\nenabled = true\n", nil)
	waitWSSize(t, base, wsBigCols, wsBigRows, "under largest with the rail")
	waitMark(t, small, true, "the small client with the rail")
	// The rail heads the screen and ends with its footer (the fold control,
	// «) on the row above the dock, whose rule runs the full width of the
	// small client's own screen.
	// The mark sits at the right end of that rule and nowhere over a pane.
	rule := strings.Repeat("─", wsSmallCols/2)
	if err := small.WaitFor(func(s tuitest.Screen) bool {
		return strings.Contains(s.Line(0), "sessions") && strings.Contains(s.Line(wsSmallRows-3), "«") &&
			strings.HasPrefix(s.Line(wsSmallRows-2), rule) && strings.Contains(s.Line(wsSmallRows-2), wsMark) &&
			!strings.Contains(s.Line(0), wsMark)
	}, uiTimeout); err != nil {
		t.Fatalf("the rail or the dock is not at the small client's own edges\n%s", small.Snapshot())
	}
	saveArtifact(t, small, artifactDir(t), "small-with-rail")
}

// TestWindowSizeTmuxShim sets the policy the way a tmux tool does, through
// the tmux shim, and reads it back through the shim and the daemon.
//
// NEGATIVE CONTROL: with the set-option window-size branch removed from the
// shim's runOne, set-option is ignored, the shim reads back smallest, and the
// policy stays smallest.
func TestWindowSizeTmuxShim(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)
	if out, err := tuiosCLI(t, base, "new", wsSession, "--detach"); err != nil {
		t.Fatalf("create session: %v: %s", err, out)
	}
	term := attachIn(t, base, wsSession, startOpts{cols: wsSmallCols, rows: wsSmallRows})
	// The quotes keep the typed line itself from matching the marker.
	line := tuiosBin + " tmux-shim -- sh -c 'tmux set -g window-size largest && echo SHIM_$(tmux show -gv window-size)_\"DONE\"'\n"
	if out, err := tuiosCLI(t, base, "send-text", "-s", wsSession, line); err != nil {
		t.Fatalf("send-text: %v\n%s", err, out)
	}
	if err := term.WaitForText("SHIM_largest_DONE", shellTimeout); err != nil {
		t.Fatalf("the shim did not set and read back window-size: %v\n%s", err, term.Snapshot())
	}
	if _, _, policy := wsSize(t, base); policy != "largest" {
		t.Fatalf("after set -g window-size largest the session's policy is %q", policy)
	}
}
