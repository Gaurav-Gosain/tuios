package tuie2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// Moving keyboard focus between panes must not resize the shell in either of
// them.
//
// A report said that moving left and right between windows on macOS left empty
// lines in a pane, with no visible size change, after the click and drag paths
// had been fixed. These pin every keyboard path that moves focus, in every
// configuration the report could plausibly be running: window-mode Tab, the
// alt+arrow chords in terminal mode, own borders and shared borders, a daemon
// session and a standalone one, a host that reports pixel sizes, auto-enter
// on focus, zsh, and the scrolling layout. Each measures zero on Linux.
//
// The pane counts the signals itself, the way click_focus_resize_test.go does:
// its shell prints one marker line per SIGWINCH, and the assertion is on rows
// in the grid, which is exactly the symptom. The swap test at the end is the
// positive half in the same fixture, so a fixture that cannot see a resize at
// all cannot pass the others for the wrong reason.

// focusFixture is one way of building the two-pane fixture.
type focusFixture struct {
	config string
	env    []string
	// standalone runs plain tuios with no daemon behind it.
	standalone bool
	// layout is a palette search that picks a layout after tiling is on.
	layout string
	// animations keeps the UI animations on, which is the default a user has.
	animations bool
	// fish runs fish in the panes instead of bash. Its trap is spelled
	// differently and its prompt repaints differently on SIGWINCH.
	fish bool
}

// focusPanes builds two tiled panes and arms a SIGWINCH counter in the second
// one, which tiling puts on the right and leaves focused, in terminal mode.
func focusPanes(t *testing.T, f focusFixture) *tuitest.Terminal {
	t.Helper()
	base := t.TempDir()
	writeConfig(t, base, f.config)
	shell := "SHELL=/bin/bash"
	if f.fish {
		shell = "SHELL=/usr/bin/fish"
	}
	env := append([]string{shell, "HOME=" + base, "ZDOTDIR=" + base}, f.env...)
	args := []string{"new", "kbfocus"}
	if f.standalone {
		args = nil
	}
	term := startIn(t, base, startOpts{cols: 160, rows: 45, args: args, env: env, animations: f.animations})
	if !f.standalone {
		killDaemon(t, base)
	}
	waitBoot(t, term)
	for range 2 {
		newWindow(t, term)
	}
	waitWindowCount(t, term, 2, "keyboard-focus setup")
	enableTiling(t, term)
	if f.layout != "" {
		send(t, term, tuitest.Ctrl('p'))
		waitPaletteOpen(t, term, "to pick a layout")
		send(t, term, f.layout, tuitest.Enter)
		waitPaletteClosed(t, term, "after picking a layout")
		time.Sleep(time.Second)
	}
	enterTerminalMode(t, term)
	arm := `trap 'printf "WI%sCH\n" N' WINCH; clear; printf "AR%sED\n" M`
	if f.fish {
		arm = `function __w --on-signal WINCH; printf "WI%sCH\n" N; end; clear; printf "AR%sED\n" M`
	}
	runInShell(t, term, arm, "ARMED", shellTimeout)
	// The trap is armed once the marker is on screen, and the clear has left
	// the command echo behind it, so every WINCH row after this came from a
	// signal.
	time.Sleep(2 * time.Second)
	return term
}

const sharedBordersConfig = "[appearance]\nshared_borders = true\nclick_to_type = 'double'\n"

// winchRows is how many marker rows the armed pane printed, once the screen
// has settled.
func winchRows(t *testing.T, term *tuitest.Terminal, what string) int {
	t.Helper()
	time.Sleep(2 * time.Second)
	n := countRows(term.Screen(), "WINCH")
	alive(t, term, "after "+what)
	return n
}

func requireNoWinch(t *testing.T, term *tuitest.Terminal, what string) {
	t.Helper()
	n := winchRows(t, term, what)
	t.Logf("%s: %d WINCH rows\n%s", what, n, term.Snapshot())
	if n != 0 {
		t.Errorf("the pane took %d resizes across %s, and printed a line for each; "+
			"moving focus must resize nothing\n%s", n, what, term.Snapshot())
	}
}

// markerColumn is the column the marker starts at on the first row holding
// it, or -1.
func markerColumn(s tuitest.Screen, marker string) int {
	_, rows := s.Size()
	for r := range rows {
		if c := strings.Index(s.Line(r), marker); c >= 0 {
			return c
		}
	}
	return -1
}

// send types keys and gives the client a beat to act on them.
func send(t *testing.T, term *tuitest.Terminal, keys ...any) {
	t.Helper()
	if err := term.SendKeys(keys...); err != nil {
		t.Fatal(err)
	}
	time.Sleep(700 * time.Millisecond)
}

// altArrowRoundTrips moves focus to the left pane and back three times with
// the terminal-mode chords.
func altArrowRoundTrips(t *testing.T, term *tuitest.Terminal) {
	t.Helper()
	for range 3 {
		send(t, term, tuitest.Alt(tuitest.Left))
		send(t, term, tuitest.Alt(tuitest.Right))
	}
}

// TestAltArrowsMoveFocus is the control for the chords the tests below send:
// alt+left really does put the keyboard in the left pane, and alt+right brings
// it back.
func TestAltArrowsMoveFocus(t *testing.T) {
	term := focusPanes(t, focusFixture{config: sharedBordersConfig})
	send(t, term, tuitest.Alt(tuitest.Left))
	send(t, term, `printf "LM%sK\n" AR`, tuitest.Enter)
	time.Sleep(time.Second)
	if col := markerColumn(term.Screen(), "LMARK"); col < 0 || col >= 80 {
		t.Fatalf("after alt+left the marker landed at column %d, want the left pane\n%s", col, term.Snapshot())
	}
	send(t, term, tuitest.Alt(tuitest.Right))
	send(t, term, `printf "RM%sK\n" AR`, tuitest.Enter)
	time.Sleep(time.Second)
	if col := markerColumn(term.Screen(), "RMARK"); col < 80 {
		t.Fatalf("after alt+right the marker landed at column %d, want the right pane\n%s", col, term.Snapshot())
	}
}

// focusCase is one keyboard focus path in one configuration. Each is its own
// top-level test rather than a subtest: the daemon's socket lives under the
// test's temp dir, and a subtest name pushes that path past the unix socket
// limit, so the daemon cannot start.
type focusCase struct {
	fixture focusFixture
	drive   func(*testing.T, *tuitest.Terminal)
}

// tabRoundTrips walks focus with Tab in window mode.
func tabRoundTrips(t *testing.T, term *tuitest.Terminal) {
	t.Helper()
	leaveTerminalMode(t, term)
	for range 6 {
		send(t, term, tuitest.Tab)
	}
}

// runFocusCase is the regression: the keyboard focus path adds no line to the
// pane.
func runFocusCase(t *testing.T, c focusCase) {
	t.Helper()
	shells := []string{}
	if c.fixture.fish {
		shells = append(shells, "/usr/bin/fish")
	}
	for _, kv := range c.fixture.env {
		if shell, ok := strings.CutPrefix(kv, "SHELL="); ok {
			shells = append(shells, shell)
		}
	}
	for _, shell := range shells {
		if _, err := os.Stat(shell); err != nil {
			t.Skipf("%s is not installed", shell)
		}
	}
	term := focusPanes(t, c.fixture)
	c.drive(t, term)
	requireNoWinch(t, term, t.Name())
}

func TestFocusTabAddsNoLine(t *testing.T) {
	runFocusCase(t, focusCase{focusFixture{config: sharedBordersConfig}, tabRoundTrips})
}

func TestFocusAltArrowsAddNoLine(t *testing.T) {
	runFocusCase(t, focusCase{focusFixture{config: sharedBordersConfig}, altArrowRoundTrips})
}

func TestFocusAltArrowsOwnBordersAddNoLine(t *testing.T) {
	runFocusCase(t, focusCase{focusFixture{config: "[appearance]\nshared_borders = false\n"}, altArrowRoundTrips})
}

func TestFocusAltArrowsPixelHostAddNoLine(t *testing.T) {
	runFocusCase(t, focusCase{focusFixture{config: sharedBordersConfig, env: []string{"TUIOS_CELL_SIZE=9x20"}}, altArrowRoundTrips})
}

func TestFocusAltArrowsStandaloneAddNoLine(t *testing.T) {
	runFocusCase(t, focusCase{focusFixture{config: sharedBordersConfig, standalone: true}, altArrowRoundTrips})
}

func TestFocusTabStandaloneAddsNoLine(t *testing.T) {
	runFocusCase(t, focusCase{focusFixture{config: sharedBordersConfig, standalone: true}, tabRoundTrips})
}

// The auto-enter policy: 1 picks the left pane and enters terminal mode there,
// and alt+right comes back to the armed pane in terminal mode.
func TestFocusAutoEnterAddsNoLine(t *testing.T) {
	runFocusCase(t, focusCase{
		focusFixture{config: sharedBordersConfig + "auto_enter_terminal_on_focus = 'targeted'\n"},
		func(t *testing.T, term *tuitest.Terminal) {
			leaveTerminalMode(t, term)
			send(t, term, "1")
			send(t, term, tuitest.Alt(tuitest.Right))
			altArrowRoundTrips(t, term)
		}})
}

func TestFocusAltArrowsZshAddNoLine(t *testing.T) {
	runFocusCase(t, focusCase{focusFixture{config: sharedBordersConfig, env: []string{"SHELL=/usr/bin/zsh"}}, altArrowRoundTrips})
}

func TestFocusAltArrowsScrollingAddNoLine(t *testing.T) {
	runFocusCase(t, focusCase{focusFixture{config: sharedBordersConfig, layout: "scrolling"}, altArrowRoundTrips})
}

// A kitty-protocol host reports the release of a chord as well as its press.
// A release must reach neither the layout nor the shell.
func TestFocusKittyReleaseAddsNoLine(t *testing.T) {
	runFocusCase(t, focusCase{focusFixture{config: sharedBordersConfig},
		func(t *testing.T, term *tuitest.Terminal) {
			for range 3 {
				send(t, term, tuitest.Key("\x1b[1;3D"), tuitest.Key("\x1b[1;3:3D"))
				send(t, term, tuitest.Key("\x1b[1;3C"), tuitest.Key("\x1b[1;3:3C"))
			}
		}})
}

// TestSwapIntoANarrowerSlotIsTold is the positive half, in the same fixture.
// With shared borders on, a 160-column screen splits into an 80-column tile, a
// divider and a 79-column tile, so swapping the two panes (H, or ctrl+left)
// really does change each guest's width by a column and the guest has to be
// told. Without this the tests above could pass because the fixture cannot see
// a resize at all.
//
// It is also the measurement behind the report's other reading: "moving left
// and right between windows" as a swap, not a focus change, costs each shell a
// real one-column resize and the prompt repaint that follows, even though the
// two tiles look the same size.
func TestSwapIntoANarrowerSlotIsTold(t *testing.T) {
	term := focusPanes(t, focusFixture{config: sharedBordersConfig})
	leaveTerminalMode(t, term)
	send(t, term, "H")
	if n := winchRows(t, term, "one swap to the left"); n < 1 {
		t.Errorf("a swap into a slot one column narrower announced %d resizes, want at least 1; "+
			"the fixture cannot see a resize, so the TestFocus* tests prove nothing\n%s",
			n, term.Snapshot())
	}
}

func TestFocusAltArrowsFishAddNoLine(t *testing.T) {
	runFocusCase(t, focusCase{focusFixture{config: sharedBordersConfig, fish: true}, altArrowRoundTrips})
}

func TestFocusTabFishAddsNoLine(t *testing.T) {
	runFocusCase(t, focusCase{focusFixture{config: sharedBordersConfig, fish: true}, tabRoundTrips})
}

func TestFocusAltArrowsFishStandaloneAddNoLine(t *testing.T) {
	runFocusCase(t, focusCase{focusFixture{config: sharedBordersConfig, fish: true, standalone: true}, altArrowRoundTrips})
}

func TestFocusAltArrowsFishPixelHostAddNoLine(t *testing.T) {
	runFocusCase(t, focusCase{focusFixture{config: sharedBordersConfig, fish: true, env: []string{"TUIOS_CELL_SIZE=9x20"}}, altArrowRoundTrips})
}

func TestFocusAltArrowsFishOwnBordersAddNoLine(t *testing.T) {
	runFocusCase(t, focusCase{focusFixture{config: "[appearance]\nshared_borders = false\n", fish: true}, altArrowRoundTrips})
}

func TestFocusAltArrowsFishKittyHostAddNoLine(t *testing.T) {
	runFocusCase(t, focusCase{focusFixture{config: sharedBordersConfig, fish: true,
		env: []string{"TUIOS_KITTY_GRAPHICS=1", "KITTY_WINDOW_ID=1", "TERM=xterm-kitty", "TUIOS_CELL_SIZE=9x20", "KITTY_SHELL_INTEGRATION=enabled"}}, altArrowRoundTrips})
}

func TestFocusAltArrowsFishAnimatedAddNoLine(t *testing.T) {
	runFocusCase(t, focusCase{focusFixture{config: sharedBordersConfig, fish: true, animations: true,
		env: []string{"TUIOS_KITTY_GRAPHICS=1", "KITTY_WINDOW_ID=1", "TERM=xterm-kitty", "TUIOS_CELL_SIZE=9x20"}}, altArrowRoundTrips})
}

func TestFocusAltArrowsFishAnimatedOwnBordersAddNoLine(t *testing.T) {
	runFocusCase(t, focusCase{focusFixture{config: "[appearance]\nshared_borders = false\n", fish: true, animations: true,
		env: []string{"TUIOS_KITTY_GRAPHICS=1", "KITTY_WINDOW_ID=1", "TERM=xterm-kitty", "TUIOS_CELL_SIZE=9x20"}}, altArrowRoundTrips})
}

func TestFocusTabFishAnimatedOwnBordersAddsNoLine(t *testing.T) {
	runFocusCase(t, focusCase{focusFixture{config: "[appearance]\nshared_borders = false\n", fish: true, animations: true,
		env: []string{"TUIOS_CELL_SIZE=9x20"}}, tabRoundTrips})
}

// TestRenderTraceRecordsEachAnnouncement is the diagnostic the macOS report
// is asked to run: with TUIOS_RENDER_TRACE set, every size handed to a guest
// is written to the trace with the code path that handed it. A swap under
// shared borders announces one size per pane, so the trace has to show it.
func TestRenderTraceRecordsEachAnnouncement(t *testing.T) {
	trace := filepath.Join(t.TempDir(), "trace.log")
	term := focusPanes(t, focusFixture{config: sharedBordersConfig, env: []string{"TUIOS_RENDER_TRACE=" + trace}})
	leaveTerminalMode(t, term)
	send(t, term, "H")
	if n := winchRows(t, term, "one swap to the left"); n < 1 {
		t.Fatalf("the swap announced nothing, so there is nothing for the trace to record\n%s", term.Snapshot())
	}
	body, err := os.ReadFile(trace)
	if err != nil {
		t.Fatalf("the trace file was not written: %v", err)
	}
	var announced int
	for _, line := range strings.Split(string(body), "\n") {
		if strings.Contains(line, "announce id=") && strings.Contains(line, " via ") {
			announced++
		}
	}
	if announced < 2 {
		t.Fatalf("the trace holds %d announce lines after a swap of two panes, want at least 2\n%s", announced, body)
	}
	t.Logf("trace:\n%s", body)
}
