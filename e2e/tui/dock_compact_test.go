package tuie2e

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// appearance.dock_compact draws the dock as one row: the pills, without the
// rule between them and the panes. These drive it on the real binary, at the
// top and at the bottom: the pane gets the row, a click on a pill still lands
// on it, and a config save switches it on a running client.
//
// Negative controls are in NEGATIVE_CONTROLS.md under "Compact dock".

const compactCols, compactRows = 100, 40

// compactConfig is a config that puts the dock at pos, compact or full.
func compactConfig(pos string, compact bool) string {
	return fmt.Sprintf("[appearance]\ndockbar_position = %q\ndock_compact = %t\n", pos, compact)
}

// dockRowAt is the row the dock's pills are on: the first row with the dock at
// the top, the last with it at the bottom. Compact or full, the pills keep the
// screen edge.
func dockRowAt(pos string) int {
	if pos == "top" {
		return 0
	}
	return compactRows - 1
}

// dockStatusRe is the dock's "<workspace>:<count>" status field.
var dockStatusRe = regexp.MustCompile(`([0-9]+):([0-9]+)`)

// dockStatusOn reads the workspace and the window count off one row, or -1, -1
// when the row carries no status. countWindows reads only the rows where a
// full dock sits, so it cannot see a compact dock at the top.
func dockStatusOn(s tuitest.Screen, row int) (ws, n int) {
	m := dockStatusRe.FindStringSubmatch(s.Line(row))
	if m == nil {
		return -1, -1
	}
	ws, _ = strconv.Atoi(m[1])
	n, _ = strconv.Atoi(m[2])
	return ws, n
}

// waitDockStatus waits for the dock row to read ws:n.
func waitDockStatus(t *testing.T, term *tuitest.Terminal, pos string, ws, n int, what string) {
	t.Helper()
	row := dockRowAt(pos)
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		w, c := dockStatusOn(s, row)
		return w == ws && c == n
	}, uiTimeout); err != nil {
		t.Fatalf("%s: the dock row never read %d:%d: %v\n%s", what, ws, n, err, term.Snapshot())
	}
}

// paneTopRow is the first row that carries a pane's top-left corner, or -1.
func paneTopRow(s tuitest.Screen) int {
	_, rows := s.Size()
	for r := range rows {
		for _, ch := range s.Line(r) {
			if isPaneCorner(ch) {
				return r
			}
		}
	}
	return -1
}

// paneBottomEdge is the last row that carries a pane's bottom-left corner, or -1.
func paneBottomEdge(s tuitest.Screen) int {
	_, rows := s.Size()
	last := -1
	for r := range rows {
		for _, ch := range s.Line(r) {
			if ch == '╰' || ch == '└' || ch == '┗' || ch == '╚' {
				last = r
				break
			}
		}
	}
	return last
}

// startCompactFixture starts a standalone tuios with the dock at pos, opens one
// tiled pane, and waits until the pane is drawn where the dock leaves room.
func startCompactFixture(t *testing.T, base, pos string, compact bool) *tuitest.Terminal {
	t.Helper()
	writeConfig(t, base, compactConfig(pos, compact)+
		"[screenshot]\ndirectory = \""+shotDir(t, base)+"\"\nformat = \"png\"\n")
	term := startIn(t, base, startOpts{cols: compactCols, rows: compactRows})
	waitBoot(t, term)
	if err := term.SendKeys("n"); err != nil {
		t.Fatalf("new window: %v", err)
	}
	waitDockStatus(t, term, pos, 1, 1, "after the first window")
	enableTiling(t, term)
	return term
}

// wantPaneEdge is the row the pane's border meets the dock on: its top row with
// the dock at the top, its bottom row with the dock at the bottom.
func wantPaneEdge(pos string, compact bool) int {
	rule := 1
	if compact {
		rule = 0
	}
	if pos == "top" {
		return 1 + rule
	}
	return compactRows - 2 - rule
}

// paneEdge reads the row wantPaneEdge predicts.
func paneEdge(s tuitest.Screen, pos string) int {
	if pos == "top" {
		return paneTopRow(s)
	}
	return paneBottomEdge(s)
}

// waitPaneEdge waits for the pane to meet the dock at the row a dock of that
// height leaves.
func waitPaneEdge(t *testing.T, term *tuitest.Terminal, pos string, compact bool, what string) {
	t.Helper()
	want := wantPaneEdge(pos, compact)
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return paneEdge(s, pos) == want
	}, configWatchTimeout); err != nil {
		t.Fatalf("%s: the pane meets the dock at row %d, want %d: %v\n%s",
			what, paneEdge(term.Screen(), pos), want, err, term.Snapshot())
	}
}

// TestDockCompactGivesThePaneARow: with dock_compact on, the pane's own shell
// is one row taller than with it off, at the top and at the bottom, and the
// pane's border sits on the row the rule used to take. The rows come from the
// shell's stty, so this is the size the program in the pane draws into.
func TestDockCompactGivesThePaneARow(t *testing.T) {
	for _, pos := range []string{"top", "bottom"} {
		t.Run(pos, func(t *testing.T) {
			rowsOf := map[bool]int{}
			for _, compact := range []bool{false, true} {
				base := t.TempDir()
				term := startCompactFixture(t, base, pos, compact)
				waitPaneEdge(t, term, pos, compact, fmt.Sprintf("compact=%t", compact))
				// The frame as tuios captures it, for a person to look at.
				name := fmt.Sprintf("%s-compact-%t", pos, compact)
				saveArtifact(t, term, artifactDir(t), name)
				captureScreenTo(t, term, shotDir(t, base), artifactDir(t), name)
				enterTerminalMode(t, term)
				tag := "A"
				if compact {
					tag = "B"
				}
				rows, _ := reportedPaneSize(t, term, tag)
				rowsOf[compact] = rows
				_ = term.Close()
			}
			if rowsOf[true] != rowsOf[false]+1 {
				t.Fatalf("dock at the %s: the pane has %d rows with a compact dock and %d with a full one, want one more",
					pos, rowsOf[true], rowsOf[false])
			}
		})
	}
}

// pillCol is the screen column of the first cell of a workspace pill labelled
// label on the dock row, found by the label with a space on each side. -1 when
// it is not there.
func pillCol(line, label string) int {
	return pillColumn(line, " "+label+" ")
}

// TestDockCompactPillClickSwitchesWorkspace: with a compact dock a click on a
// workspace pill switches to that workspace, and the pane's edge row next to
// the dock is still the pane's, at the top and at the bottom.
//
// The second half is the hit test. A dock band still two rows tall claims the
// pane's edge row, and a right-click there opens the dock's menu instead of
// the pane's.
func TestDockCompactPillClickSwitchesWorkspace(t *testing.T) {
	for _, pos := range []string{"top", "bottom"} {
		t.Run(pos, func(t *testing.T) {
			base := t.TempDir()
			term := startCompactFixture(t, base, pos, true)
			waitPaneEdge(t, term, pos, true, "compact")

			// A window on workspace 2, so its pill is on the strip, then back to 1.
			if err := term.SendKeys(tuitest.Ctrl('b'), "w", "2"); err != nil {
				t.Fatalf("switch to workspace 2: %v", err)
			}
			waitDockStatus(t, term, pos, 2, 0, "on workspace 2")
			if err := term.SendKeys("n"); err != nil {
				t.Fatalf("new window: %v", err)
			}
			waitDockStatus(t, term, pos, 2, 1, "a window on workspace 2")
			if err := term.SendKeys(tuitest.Ctrl('b'), "w", "1"); err != nil {
				t.Fatalf("switch to workspace 1: %v", err)
			}
			waitDockStatus(t, term, pos, 1, 1, "back on workspace 1")
			time.Sleep(insertGuard)

			row := dockRowAt(pos)
			line := term.Screen().Line(row)
			col := pillCol(line, "2")
			if col < 0 {
				t.Fatalf("no pill for workspace 2 on the dock row %q\n%s", line, term.Snapshot())
			}
			leftClick(t, term, col+1, row)
			waitDockStatus(t, term, pos, 2, 1, "after a click on the pill for workspace 2")

			// The pane's edge row next to the dock opens the pane's menu.
			edge := wantPaneEdge(pos, true)
			shiftRightClick(t, term, 30, edge)
			waitMenu(t, term, "the pane row next to a compact dock", "Split right", "Rename")
			if strings.Contains(term.Screen().Text(), "Restore all") {
				t.Fatalf("row %d is the pane's edge but it opened the dock's menu\n%s", edge, term.Snapshot())
			}
			alive(t, term, "after a click on a compact dock")
		})
	}
}

// TestDockCompactSwitchesOnConfigSave: a running client picks up dock_compact
// from a config saved on disk, and the pane gets the row, then gives it back
// when the option is turned off again. The shell's stty says the PTY moved, so
// this is a retile and not only a redraw.
func TestDockCompactSwitchesOnConfigSave(t *testing.T) {
	for _, pos := range []string{"top", "bottom"} {
		t.Run(pos, func(t *testing.T) {
			base := t.TempDir()
			term := startCompactFixture(t, base, pos, false)
			waitPaneEdge(t, term, pos, false, "full dock at start")
			enterTerminalMode(t, term)
			full, _ := reportedPaneSize(t, term, "A")
			if err := term.SendKeys("clear", tuitest.Enter); err != nil {
				t.Fatalf("clear: %v", err)
			}

			// The whole file is replaced, so it carries the pinned looks too:
			// without them the rail and the title bars move to their shipped
			// places and the rows would not be comparable.
			pinned := "window_title_position = \"bottom\"\nzoom_size = 100\nclick_to_type = \"single\"\nmodal_dim = 0\n" +
				"[appearance.sidebar]\nenabled = false\n[appearance.scrollbar]\nstyle = \"thin\"\n"
			saveConfigLikeAnEditor(t, base, compactConfig(pos, true)+pinned)
			waitPaneEdge(t, term, pos, true, "after dock_compact = true was saved")
			saveArtifact(t, term, artifactDir(t), pos+"-after-save")
			compact, _ := reportedPaneSize(t, term, "B")
			if compact != full+1 {
				t.Fatalf("after the save the pane has %d rows, want %d", compact, full+1)
			}

			saveConfigLikeAnEditor(t, base, compactConfig(pos, false)+pinned)
			waitPaneEdge(t, term, pos, false, "after dock_compact = false was saved")
		})
	}
}

// TestDockCompactClientSharesASessionWithAFullOne: two clients on one session,
// one with a full dock and one with a compact dock, each from its own config.
// The panes are shared, so they keep the larger reserve while both are there:
// the compact client leaves its spare row blank, and the full client's panes
// do not move. When the full client leaves, the compact client's reserve is
// the only one, and its panes take the row.
//
// This is the reserve the client tells the daemon. A client that announced the
// full dock's two rows whatever its setting would keep the panes off the row
// after the other client left.
func TestDockCompactClientSharesASessionWithAFullOne(t *testing.T) {
	full, base := twoClientSession(t, "dock", bigCols, bigRows)
	enableTiling(t, full)
	fullEdge := bigRows - 3
	waitBottomEdge(t, full, fullEdge, "the full client on its own")

	home := t.TempDir()
	writeConfigIn(t, home, compactConfig("bottom", true))
	compact := attachIn(t, base, "dock", startOpts{
		cols: bigCols, rows: bigRows,
		env: []string{"XDG_CONFIG_HOME=" + home},
	})
	waitBottomEdge(t, compact, fullEdge, "the compact client beside a full one")
	if line := strings.TrimSpace(compact.Screen().Line(bigRows - 2)); line != "" {
		t.Fatalf("the compact client draws %q on the row it keeps blank\n%s", line, compact.Snapshot())
	}
	time.Sleep(time.Second)
	if got := paneBottomEdge(full.Screen()); got != fullEdge {
		t.Fatalf("a compact client attached and moved the full client's panes to row %d, want %d\n%s",
			got, fullEdge, full.Snapshot())
	}
	saveArtifact(t, compact, artifactDir(t), "compact-beside-full")

	if err := full.Close(); err != nil {
		t.Fatalf("close the full client: %v", err)
	}
	waitBottomEdge(t, compact, bigRows-2, "the compact client on its own")
	saveArtifact(t, compact, artifactDir(t), "compact-alone")
}

// waitBottomEdge waits for the lowest pane border to be on row want.
func waitBottomEdge(t *testing.T, term *tuitest.Terminal, want int, what string) {
	t.Helper()
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return paneBottomEdge(s) == want
	}, 20*time.Second); err != nil {
		t.Fatalf("%s: the panes end on row %d, want %d: %v\n%s",
			what, paneBottomEdge(term.Screen()), want, err, term.Snapshot())
	}
}

// TestDockCompactFromTheSettingsPanel: the Compact dock row on the settings
// page switches the dock on a running client, and the pane gets the row.
func TestDockCompactFromTheSettingsPanel(t *testing.T) {
	for _, pos := range []string{"top", "bottom"} {
		t.Run(pos, func(t *testing.T) {
			base := t.TempDir()
			term := startCompactFixture(t, base, pos, false)
			waitPaneEdge(t, term, pos, false, "full dock at start")

			openSettings(t, term)
			if err := term.SendKeys("/", "compact dock"); err != nil {
				t.Fatalf("search: %v", err)
			}
			if err := term.WaitFor(func(s tuitest.Screen) bool {
				row := selectedSettingsRow(s, "Compact dock")
				return row != "" && strings.Contains(row, "off")
			}, uiTimeout); err != nil {
				t.Fatalf("the search did not put Compact dock, off, under the cursor: %v\n%s", err, term.Snapshot())
			}
			if err := term.SendKeys(tuitest.Enter); err != nil {
				t.Fatalf("enter: %v", err)
			}
			if err := term.WaitFor(func(s tuitest.Screen) bool {
				return strings.Contains(selectedSettingsRow(s, "Compact dock"), "on ]")
			}, uiTimeout); err != nil {
				t.Fatalf("enter did not turn Compact dock on: %v\n%s", err, term.Snapshot())
			}
			closeSettingsSearch(t, term)
			waitPaneEdge(t, term, pos, true, "after Compact dock was turned on in the settings panel")
		})
	}
}
