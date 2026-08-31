package tuie2e

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// popupResult is what `tuios popup --json` prints.
type popupResult struct {
	WindowID string `json:"window_id"`
	Name     string `json:"name"`
	Width    string `json:"width"`
	Height   string `json:"height"`
}

// openPopup runs `tuios popup` against the daemon under base and returns what it
// reported. The command is an argv, exactly as a user types it after --.
func openPopup(t *testing.T, base string, args ...string) popupResult {
	t.Helper()
	out, err := tuiosCLI(t, base, append([]string{"popup", "--json"}, args...)...)
	if err != nil {
		t.Fatalf("tuios popup %v: %v\n%s", args, err, out)
	}
	var res popupResult
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("tuios popup printed something that is not JSON: %v\n%s", err, out)
	}
	if res.WindowID == "" {
		t.Fatalf("tuios popup reported no window id:\n%s", out)
	}
	return res
}

// rectFor finds one window's geometry in a settled list.
func rectFor(t *testing.T, rects []winRect, id string) winRect {
	t.Helper()
	for _, r := range rects {
		if r.ID == id {
			return r
		}
	}
	t.Fatalf("window %s is not in the settled geometry %v", id, rects)
	return winRect{}
}

// TestPopupOverEveryLayout opens a popup over each of the three tiling layouts
// and checks the two things a popup has to get right on all of them: it is
// centred in the pane region at the size that was asked for, and the panes
// underneath it keep the layout they had.
//
// It is run against every layout because a popup is a floating pane, and the
// three layouts skip floats in three different places. The scrolling layout is
// the one worth naming: its strip is longer than the view and slides under the
// panes, so a popup that were placed on the strip rather than on the screen
// would drift the moment anything scrolled.
func TestPopupOverEveryLayout(t *testing.T) {
	for _, mode := range []string{"bsp", "master-stack", "scrolling"} {
		t.Run(mode, func(t *testing.T) {
			base := t.TempDir()
			writeConfig(t, base, fmt.Sprintf(
				"[startup]\nopen_default_window = true\ntiled = true\nlayout = %q\n", mode))

			term := startIn(t, base, startOpts{cols: 120, rows: 40, args: []string{"new", "pop"}})
			waitWindowCount(t, term, 1, "after starting a new session")
			// Through the daemon rather than by keystroke, so the test does not
			// depend on which mode the client booted into.
			if out, err := tuiosCLI(t, base, "run-command", "NewWindow"); err != nil {
				t.Fatalf("open a second pane: %v\n%s", err, out)
			}
			waitWindowCount(t, term, 2, "second pane")

			// The scrolling layout is only itself once the strip is longer than
			// the view, which is what the extra columns are for.
			panes := 2
			if mode == "scrolling" {
				for panes < 5 {
					if out, err := tuiosCLI(t, base, "run-command", "NewWindow"); err != nil {
						t.Fatalf("open another pane: %v\n%s", err, out)
					}
					panes++
					waitWindowCount(t, term, panes, "another column on the strip")
				}
			}

			// The layout as it stands before the popup, so the check afterwards
			// is that nothing moved rather than that something looks plausible.
			before := waitForSettledGeometryIn(t, base, "pop", panes)

			res := openPopup(t, base, "--width", "50%", "--height", "40%",
				"--", "sh", "-c", "printf 'POPUP-IS-HERE\\n'; sleep 120")
			if err := term.WaitForText("POPUP-IS-HERE", uiTimeout); err != nil {
				t.Fatalf("the popup never printed in %s mode: %v\n%s", mode, err, term.Snapshot())
			}
			t.Logf("a popup over the %s layout:\n%s", mode, term.Snapshot())

			rects := waitForSettledGeometryIn(t, base, "pop", panes+1)
			popup := rectFor(t, rects, res.WindowID)

			// 120 columns and 40 rows, less one dock row and its separator: the
			// pane region is 120x38. Half its width and two fifths of its height,
			// centred.
			wantW, wantH := 60, 15
			if popup.Width != wantW || popup.Height != wantH {
				t.Errorf("popup in %s mode is %dx%d, want %dx%d",
					mode, popup.Width, popup.Height, wantW, wantH)
			}
			if popup.X != (120-wantW)/2 {
				t.Errorf("popup in %s mode is at x=%d, want it centred at x=%d",
					mode, popup.X, (120-wantW)/2)
			}
			if popup.X < 0 || popup.Y < 0 || popup.X+popup.Width > 120 || popup.Y+popup.Height > 40 {
				t.Errorf("popup in %s mode is outside the screen: (%d,%d) %dx%d",
					mode, popup.X, popup.Y, popup.Width, popup.Height)
			}

			// The panes under it are where they were. A popup that joined the
			// tiling would have taken a third of the box off both of them.
			for _, was := range before {
				now := rectFor(t, rects, was.ID)
				if now != was {
					t.Errorf("the popup moved a tiled pane in %s mode: %s was (%d,%d) %dx%d, now (%d,%d) %dx%d",
						mode, was.ID, was.X, was.Y, was.Width, was.Height,
						now.X, now.Y, now.Width, now.Height)
				}
			}
			if mode == "scrolling" {
				// The strip is longer than the view, and moving along it slides
				// every column under the screen. A popup is placed on the screen
				// and not on the strip, so it must not move with it.
				if out, err := tuiosCLI(t, base, "focus-window", before[0].ID); err != nil {
					t.Fatalf("step along the strip: %v\n%s", err, out)
				}
				if err := term.WaitFor(func(s tuitest.Screen) bool {
					return screenHas(s, "POPUP-IS-HERE")
				}, uiTimeout); err != nil {
					t.Fatalf("the popup left the screen when the strip moved: %v\n%s", err, term.Snapshot())
				}
				scrolled := waitForSettledGeometryIn(t, base, "pop", panes+1)
				if now := rectFor(t, scrolled, res.WindowID); now != popup {
					t.Errorf("scrolling the strip moved the popup: was (%d,%d) %dx%d, now (%d,%d) %dx%d",
						popup.X, popup.Y, popup.Width, popup.Height, now.X, now.Y, now.Width, now.Height)
				}
				t.Logf("the same popup after stepping along the strip:\n%s", term.Snapshot())
			}

			alive(t, term, "with a popup open")
		})
	}
}

// TestPopupClosesWhenItsCommandExits is the whole lifetime rule: a popup lives
// as long as its command and not one moment longer. Nothing closes it, and no
// keystroke is sent.
func TestPopupClosesWhenItsCommandExits(t *testing.T) {
	term, base := start(t, startOpts{cols: 100, rows: 32, args: []string{"new", "pop"}})
	waitBoot(t, term)
	newWindow(t, term)
	waitWindowCount(t, term, 1, "first pane")

	openPopup(t, base, "--", "sh", "-c", "printf 'BRIEF-POPUP\\n'; sleep 1")
	if err := term.WaitForText("BRIEF-POPUP", uiTimeout); err != nil {
		t.Fatalf("the popup never opened: %v\n%s", err, term.Snapshot())
	}
	waitWindowCount(t, term, 2, "with the popup open")
	waitWindowCount(t, term, 1, "after the popup's command exited")
	alive(t, term, "after a popup closed itself")
}

// TestPopupRunsAPickerAndTheSelectionLands is the reason the command exists: an
// ordinary picker becomes an overlay, and the answer it produces reaches
// somewhere the caller can use.
//
// The selection goes to a file the popup's own command redirects into, because
// that is the whole of what tuios promises here. A popup writes to its own
// screen and never to the stdout of the command that opened it.
func TestPopupRunsAPickerAndTheSelectionLands(t *testing.T) {
	if _, err := os.Stat("/usr/bin/fzf"); err != nil {
		t.Skip("fzf is not installed on this machine")
	}
	term, base := start(t, startOpts{cols: 110, rows: 36, args: []string{"new", "pop"}})
	waitBoot(t, term)
	newWindow(t, term)
	waitWindowCount(t, term, 1, "first pane")

	pick := filepath.Join(t.TempDir(), "pick")
	res := openPopup(t, base, "--width", "60%", "--height", "50%", "--", "sh", "-c",
		"printf 'alpha\\nbeta\\ngamma\\n' | fzf > "+pick)

	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return screenHas(s, "alpha", "beta", "gamma")
	}, uiTimeout); err != nil {
		t.Fatalf("fzf never drew its list in the popup: %v\n%s", err, term.Snapshot())
	}
	t.Logf("fzf running as a tuios overlay:\n%s", term.Snapshot())

	if out, err := tuiosCLI(t, base, "send-text", "-w", res.WindowID, "bet"); err != nil {
		t.Fatalf("type into the popup: %v\n%s", err, out)
	}
	if err := term.WaitForText("1/3", uiTimeout); err != nil {
		t.Fatalf("fzf never narrowed to one match: %v\n%s", err, term.Snapshot())
	}
	t.Logf("fzf narrowed to one match:\n%s", term.Snapshot())
	if out, err := tuiosCLI(t, base, "send-text", "-w", res.WindowID, "\r"); err != nil {
		t.Fatalf("accept the fzf selection: %v\n%s", err, out)
	}

	// fzf exits on the selection, so the popup closes itself.
	waitWindowCount(t, term, 1, "after fzf accepted a selection")

	deadline := time.Now().Add(uiTimeout)
	var got string
	for time.Now().Before(deadline) {
		data, err := os.ReadFile(pick)
		if err == nil && strings.TrimSpace(string(data)) != "" {
			got = strings.TrimSpace(string(data))
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if got != "beta" {
		t.Fatalf("the popup's selection reached %s as %q, want %q", pick, got, "beta")
	}
	alive(t, term, "after a picker popup closed")
}

// TestPopupIsSharedBetweenClients pins the decision that a popup is session
// state rather than one client's own: a popup opened while two clients are
// attached is drawn by both, and the daemon holds one popup rather than two.
//
// This is why it is shared. The pane is the session's and its shell has one
// size, so a client that did not know about the popup would count it among the
// panes it tiles, tile it back into the box, and push that layout to everybody -
// which is exactly what a peer does to a float it has not been told about.
//
// The second client is started narrower than the first on purpose. The session
// settles to the smaller of the two, so both end up rendering the same box here;
// the client-by-client arithmetic that box comes out of is measured directly in
// TestPopupBoxFollowsTheClientNotThePeer.
func TestPopupIsSharedBetweenClients(t *testing.T) {
	base := t.TempDir()
	writeConfig(t, base, "[startup]\nopen_default_window = true\ntiled = true\n")

	wide := startIn(t, base, startOpts{cols: 120, rows: 40, args: []string{"new", "shared"}})
	waitWindowCount(t, wide, 1, "the first client's pane")
	narrow := attachIn(t, base, "shared", startOpts{cols: 80, rows: 30})
	waitWindowCount(t, narrow, 1, "the second client's pane")

	res := openPopup(t, base, "--width", "50%", "--height", "50%",
		"--", "sh", "-c", "printf 'SHARED-POPUP\\n'; sleep 120")

	for name, term := range map[string]*tuitest.Terminal{"wide": wide, "narrow": narrow} {
		if err := term.WaitForText("SHARED-POPUP", uiTimeout); err != nil {
			t.Fatalf("the %s client never drew the popup: %v\n%s", name, err, term.Snapshot())
		}
		t.Logf("the %s client with the shared popup:\n%s", name, term.Snapshot())
	}

	// Both clients drew it, and the daemon holds one popup, not two.
	out, err := tuiosCLI(t, base, "list-windows", "--json", "--session", "shared")
	if err != nil {
		t.Fatalf("list-windows: %v\n%s", err, out)
	}
	if strings.Count(out, res.WindowID) == 0 {
		t.Fatalf("the popup %s is not in the session's window list:\n%s", res.WindowID, out)
	}
	alive(t, wide, "with a shared popup open")
	alive(t, narrow, "with a shared popup open")
}

// TestPopupNeedsAnAttachedClient holds the one refusal the command makes. A
// popup is a thing on a screen for the length of one command, so opening one on
// a session nobody is looking at would run a program in a box no one can see.
func TestPopupNeedsAnAttachedClient(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)
	if out, err := tuiosCLI(t, base, "new", "-d", "detached"); err != nil {
		t.Fatalf("create the detached session: %v\n%s", err, out)
	}
	out, err := tuiosCLI(t, base, "popup", "--", "true")
	if err == nil {
		t.Fatalf("popup on a detached session succeeded, output:\n%s", out)
	}
	if !strings.Contains(out, "attached client") {
		t.Fatalf("the refusal does not say a client is needed:\n%s", out)
	}
	t.Logf("popup on a detached session:\n%s", strings.TrimSpace(out))
}

// TestEscClosesAPopupThatWillNotExit is the keyboard's way out of a popup whose
// command never ends. It runs a sleep that outlives the test, so nothing but the
// keystroke can close it.
//
// Esc is pressed in window-management mode, which is the only mode it reaches
// tuios in: the pane owns esc in terminal mode, because fzf, gum and vim all
// quit on it.
func TestEscClosesAPopupThatWillNotExit(t *testing.T) {
	term, base := start(t, startOpts{cols: 100, rows: 32, args: []string{"new", "pop"}})
	waitBoot(t, term)
	newWindow(t, term)
	waitWindowCount(t, term, 1, "first pane")

	openPopup(t, base, "--", "sh", "-c", "printf 'STUBBORN-POPUP\\n'; sleep 600")
	if err := term.WaitForText("STUBBORN-POPUP", uiTimeout); err != nil {
		t.Fatalf("the popup never opened: %v\n%s", err, term.Snapshot())
	}
	waitWindowCount(t, term, 2, "with the stubborn popup open")

	// The user's own way out, in the order they press the keys: esc leaves the
	// pane, esc closes the box. Entering terminal mode first is what makes the
	// first half real rather than assumed.
	enterTerminalMode(t, term)
	leaveTerminalMode(t, term)
	if err := term.SendKeys(tuitest.Esc); err != nil {
		t.Fatalf("press esc: %v", err)
	}
	waitWindowCount(t, term, 1, "after esc closed the popup")
	if strings.Contains(term.Screen().Text(), "STUBBORN-POPUP") {
		t.Fatalf("the popup is still on screen after esc:\n%s", term.Snapshot())
	}
	t.Logf("after esc closed the popup:\n%s", term.Snapshot())
	alive(t, term, "after esc closed a popup")
}

// TestAPopupShowsOverAZoomedPane covers the one place a pane may legitimately
// hide every other pane. A zoomed pane owns the region and the renderer draws
// nothing else in it, which is right for a tiled pane and wrong for a popup: a
// popup runs one command for as long as the user is looking at it, so a popup
// nobody can see is a program nobody can answer.
func TestAPopupShowsOverAZoomedPane(t *testing.T) {
	base := t.TempDir()
	writeConfig(t, base, "[startup]\nopen_default_window = true\ntiled = true\n")

	term := startIn(t, base, startOpts{cols: 110, rows: 36, args: []string{"new", "pop"}})
	waitWindowCount(t, term, 1, "after starting a new session")
	if out, err := tuiosCLI(t, base, "run-command", "NewWindow"); err != nil {
		t.Fatalf("open a second pane: %v\n%s", err, out)
	}
	waitWindowCount(t, term, 2, "second pane")
	if out, err := tuiosCLI(t, base, "run-command", "ToggleZoom"); err != nil {
		t.Fatalf("zoom a pane: %v\n%s", err, out)
	}

	openPopup(t, base, "--width", "50%", "--height", "40%",
		"--", "sh", "-c", "printf 'POPUP-OVER-ZOOM\\n'; sleep 120")
	if err := term.WaitForText("POPUP-OVER-ZOOM", uiTimeout); err != nil {
		t.Fatalf("the popup is hidden behind the zoomed pane: %v\n%s", err, term.Snapshot())
	}
	t.Logf("a popup over a zoomed pane:\n%s", term.Snapshot())
	alive(t, term, "with a popup over a zoomed pane")
}
