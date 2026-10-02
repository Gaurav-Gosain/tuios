package tuie2e

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// The rail's custom section, driven on a real daemon and a real client. The
// section is a command's output drawn as rows, so every test here writes a
// shell command into the config, attaches, and reads the rows off the rail's
// columns.

// railCustomConfig is a rail on the left, 28 columns wide, with the custom
// section placed by sections and configured by custom, the body of the
// [appearance.sidebar.custom] table.
func railCustomConfig(sections, custom string) string {
	return "[appearance.sidebar]\nenabled = true\nposition = \"left\"\nwidth = 28\nsections = \"" +
		sections + "\"\n\n[appearance.sidebar.custom]\n" + custom + "\n" +
		"\n[notifications.agent]\nsuppress_focused = false\nsettle_seconds = 0\n"
}

// railCustomClient attaches a client to a session named e2e with the custom
// section configured.
func railCustomClient(t *testing.T, sections, custom string) (*tuitest.Terminal, string) {
	t.Helper()
	return railClient(t, "e2e", railCustomConfig(sections, custom), startOpts{cols: 120, rows: 40})
}

// waitRailShows waits until the rail's columns carry needle.
func waitRailShows(t *testing.T, term *tuitest.Terminal, needle, why string) {
	t.Helper()
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return railRowOf(s, needle) >= 0
	}, uiTimeout); err != nil {
		t.Fatalf("%s: the rail never showed %q: %v\n%s", why, needle, err, term.Snapshot())
	}
}

// waitRailLacks waits until the rail's columns no longer carry needle.
func waitRailLacks(t *testing.T, term *tuitest.Terminal, needle, why string) {
	t.Helper()
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return railRowOf(s, needle) < 0
	}, uiTimeout); err != nil {
		t.Fatalf("%s: the rail still shows %q: %v\n%s", why, needle, err, term.Snapshot())
	}
}

// railStillShows fails unless the rail shows needle now and still does after
// hold. It is the positive half of a "did not re-run" assertion.
func railStillShows(t *testing.T, term *tuitest.Terminal, needle string, hold time.Duration, why string) {
	t.Helper()
	waitRailShows(t, term, needle, why)
	time.Sleep(hold)
	if railRowOf(term.Screen(), needle) < 0 {
		t.Fatalf("%s: the rail lost %q within %s\n%s", why, needle, hold, term.Snapshot())
	}
}

// countFile is a file the command appends one line to per run, so a test can
// count runs without trusting the screen alone.
func countFile(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "runs")
}

// countingCommand appends to runs and prints RUN-N, where N is the run count.
func countingCommand(runs string) string {
	return "echo x >> " + runs + "; echo RUN-$(wc -l < " + runs + " | tr -d ' ')"
}

// runCount is how many lines runs holds.
func runCount(t *testing.T, runs string) int {
	t.Helper()
	data, err := os.ReadFile(runs)
	if err != nil {
		return 0
	}
	return strings.Count(string(data), "\n")
}

// railRunNumber is the N of the RUN-N row on the rail, or 0 when there is
// none. Tests that count runs read the number the rail shows rather than
// assuming how many runs the attach and the pane setup cost.
func railRunNumber(s tuitest.Screen) int {
	row := railRowOf(s, "RUN-")
	if row < 0 {
		return 0
	}
	m := runNumberRe.FindStringSubmatch(railLine(s, row))
	if m == nil {
		return 0
	}
	n, _ := strconv.Atoi(m[1])
	return n
}

var runNumberRe = regexp.MustCompile(`RUN-(\d+)`)

// railHeightRe reads the H=N row the environment test prints.
var railHeightRe = regexp.MustCompile(`H=(\d+)`)

// plusCountRe matches the "+N" of an overflow row.
var plusCountRe = regexp.MustCompile(`\+\d+`)

// waitRailRun waits until the rail shows RUN-n exactly.
func waitRailRun(t *testing.T, term *tuitest.Terminal, n int, why string) {
	t.Helper()
	waitRailShows(t, term, "RUN-"+strconv.Itoa(n), why)
}

// shortID is the first twelve characters of a window id, which is what a
// 26 column row can show after "ID=".
func shortID(id string) string {
	return id[:min(len(id), 12)]
}

// railComponent is the rail's row in list-dock-components --json.
type railComponent struct {
	Name     string `json:"name"`
	Side     string `json:"side"`
	Source   string `json:"source"`
	Refresh  string `json:"refresh"`
	Text     string `json:"text"`
	Visible  bool   `json:"visible"`
	LastExit int    `json:"last_exit"`
	LastErr  string `json:"last_error"`
	Stopped  bool   `json:"stopped"`
}

// listRailComponent reads the rail's row from list-dock-components.
func listRailComponent(t *testing.T, base string) railComponent {
	t.Helper()
	out, err := tuiosCLI(t, base, "list-dock-components", "--json", "-s", "e2e")
	if err != nil {
		t.Fatalf("list-dock-components: %v\n%s", err, out)
	}
	var payload struct {
		Components []railComponent `json:"components"`
	}
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatalf("list-dock-components output: %v\n%s", err, out)
	}
	for _, c := range payload.Components {
		if c.Name == "rail/custom" {
			return c
		}
	}
	t.Fatalf("list-dock-components has no rail/custom row\n%s", out)
	return railComponent{}
}

// waitRailComponent polls the listing until want is satisfied.
func waitRailComponent(t *testing.T, base, why string, want func(railComponent) bool) railComponent {
	t.Helper()
	deadline := time.Now().Add(uiTimeout)
	var last railComponent
	for time.Now().Before(deadline) {
		last = listRailComponent(t, base)
		if want(last) {
			return last
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("%s: list-dock-components never reported it; last row %+v", why, last)
	return last
}

// refreshRail re-runs the section through refresh-dock.
func refreshRail(t *testing.T, base string) {
	t.Helper()
	if out, err := tuiosCLI(t, base, "refresh-dock", "rail/custom", "-s", "e2e"); err != nil {
		t.Fatalf("refresh-dock rail/custom: %v\n%s", err, out)
	}
}

// focusPane focuses the window named name through the CLI, which fires
// window-focused in the attached client.
func focusPane(t *testing.T, base, name string) {
	t.Helper()
	if out, err := tuiosCLI(t, base, "focus-window", "-s", "e2e", windowID(t, base, "e2e", name)); err != nil {
		t.Fatalf("focus-window %s: %v\n%s", name, err, out)
	}
}

// configFile is the attached client's config file.
func configFile(base string) string {
	return filepath.Join(xdgDir(base, "XDG_CONFIG_HOME"), "tuios", "config.toml")
}

// TestRailCustomSectionPlacementAndEditorRoundTrip: a layout naming custom
// draws the title and the command's rows where the layout puts them, and the
// rail editor takes the section off and puts it back, writing the layout to
// the file both times.
//
// Negative control: drop "custom" from the sidebarSectionByName map in
// internal/app/sidebar_layout.go. The layout parser keeps the name, the
// renderer never draws it, and the first wait fails on the title.
func TestRailCustomSectionPlacementAndEditorRoundTrip(t *testing.T) {
	term, base := railCustomClient(t, "sessions,terminals,custom:40",
		"title = \"Brief\"\ncommand = \"printf 'ALPHA\\\\nBETA\\\\n'\"\nrefresh = \"once\"")
	waitRailShows(t, term, "Brief", "at attach")
	waitRailShows(t, term, "ALPHA", "at attach")
	waitRailShows(t, term, "BETA", "at attach")
	s := term.Screen()
	if railRowOf(s, "terminals") > railRowOf(s, "Brief") {
		t.Fatalf("the custom section is not below terminals, where the layout put it\n%s", term.Snapshot())
	}

	openSettings(t, term)
	if err := term.SendKeys("/", "sections"); err != nil {
		t.Fatalf("search: %v", err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return selectedSettingsRow(s, "Sections") != ""
	}, uiTimeout); err != nil {
		t.Fatalf("the search did not land on the Sections row: %v\n%s", err, term.Snapshot())
	}
	if err := term.SendKeys(tuitest.Enter); err != nil {
		t.Fatalf("open the editor: %v", err)
	}
	if err := term.WaitForText("Rail sections", uiTimeout); err != nil {
		t.Fatalf("the rail editor never opened: %v\n%s", err, term.Snapshot())
	}
	// The editor opens on the first placed row, sessions. Two down is custom.
	if err := term.SendKeys(tuitest.Down, tuitest.Down, tuitest.Enter); err != nil {
		t.Fatalf("take custom off: %v", err)
	}
	waitRailLacks(t, term, "ALPHA", "after taking the section off")
	waitRailLacks(t, term, "Brief", "after taking the section off")
	// The selection followed the row into the "Not on the rail" list, so
	// Enter again puts it back, at the end of the layout.
	if err := term.SendKeys(tuitest.Enter); err != nil {
		t.Fatalf("put custom back: %v", err)
	}
	waitRailShows(t, term, "Brief", "after putting the section back")
	waitRailShows(t, term, "ALPHA", "after putting the section back")
	if err := term.SendKeys(tuitest.Esc, tuitest.Esc); err != nil {
		t.Fatalf("close: %v", err)
	}

	data, err := os.ReadFile(configFile(base))
	if err != nil {
		t.Fatalf("read the config: %v", err)
	}
	if !editedLayoutRe.Match(data) {
		t.Fatalf("the editor did not write custom back into the layout:\n%s", data)
	}
}

// editedLayoutRe is the layout line the editor round trip leaves in the file.
// The writer picks the TOML quote style, so either one is the same value.
var editedLayoutRe = regexp.MustCompile(`(?m)^\s*sections = ['"]sessions,terminals,custom['"]\s*$`)

// TestRailCustomSectionRefreshModes pins each refresh mode on the real
// binary: once runs at attach and then only on refresh-dock, an interval
// polls, and an event re-runs on the event and not otherwise.
//
// Negative control: drop the rail component from InitDockComponents
// (internal/app/dock_runtime.go). Nothing runs, and every subtest fails on
// its first wait.
func TestRailCustomSectionRefreshModes(t *testing.T) {
	t.Run("once", func(t *testing.T) {
		runs := countFile(t)
		term, base := railCustomClient(t, "sessions,terminals,custom:40",
			"command = \""+countingCommand(runs)+"\"\nrefresh = \"once\"")
		railStillShows(t, term, "RUN-1", 3*time.Second, "once")
		if n := runCount(t, runs); n != 1 {
			t.Fatalf("a once section ran %d times in its first seconds, want 1", n)
		}
		refreshRail(t, base)
		waitRailShows(t, term, "RUN-2", "after refresh-dock")
	})
	t.Run("interval", func(t *testing.T) {
		runs := countFile(t)
		term, _ := railCustomClient(t, "sessions,terminals,custom:40",
			"command = \""+countingCommand(runs)+"\"\nrefresh = \"1s\"")
		waitRailShows(t, term, "RUN-3", "an interval of one second")
	})
	t.Run("event", func(t *testing.T) {
		runs := countFile(t)
		term, base := railCustomClient(t, "sessions,terminals,custom:40",
			"command = \""+countingCommand(runs)+"\"\nrefresh = \"event:window-focused\"")
		renameWindow(t, term, "FIRST")
		if out, err := tuiosCLI(t, base, "new-window", "SECOND", "-s", "e2e", "--no-focus"); err != nil {
			t.Fatalf("new-window: %v\n%s", err, out)
		}
		waitWindowCount(t, term, 2, "opening the second pane")
		// Whatever the attach and the pane setup cost, the count settles.
		// Read it, hold it for two seconds, and then one focus change is one
		// more run and nothing else is.
		waitRailShows(t, term, "RUN-", "the run at attach")
		time.Sleep(2 * time.Second)
		n := railRunNumber(term.Screen())
		railStillShows(t, term, "RUN-"+strconv.Itoa(n), 2*time.Second, "before any focus change")
		focusPane(t, base, "SECOND")
		waitRailRun(t, term, n+1, "after a focus change")
		railStillShows(t, term, "RUN-"+strconv.Itoa(n+1), 2*time.Second, "with nothing happening")
	})
}

// TestRailCustomSectionCutsRowsToSize: forty rows of sixty columns are cut to
// the section's lines and the rail's width, and a last line with no newline
// is still a row.
//
// Negative controls: in drawCustom (internal/app/render_sidebar.go) draw
// len(customRows) rows instead of count[sidebarSectionCustom]: ROW40 lands
// on screen and the +N row is gone. Drop the cut to the rail's width: the
// digit tails land in the pane region.
func TestRailCustomSectionCutsRowsToSize(t *testing.T) {
	t.Run("long", func(t *testing.T) {
		term, _ := railCustomClient(t, "sessions,terminals,custom:40",
			"command = \"i=1; while [ $i -le 40 ]; do printf 'ROW%02d-%s\\\\n' $i 0123456789012345678901234567890123456789012345678901234567890123456789; i=$((i+1)); done\"")
		waitRailShows(t, term, "ROW01", "forty rows")
		s := term.Screen()
		if railRowOf(s, "ROW40") >= 0 {
			t.Fatalf("the section drew all forty rows on a rail with 40%% of the lines\n%s", term.Snapshot())
		}
		// The overflow row is "+N" below the custom title. The sessions and
		// terminals headers draw a "+" too, so look only under the title.
		title := railRowOf(s, "Custom")
		_, rows := s.Size()
		found := false
		for r := title + 1; title >= 0 && r < rows; r++ {
			if plusCountRe.MatchString(railLine(s, r)) {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("the section hides rows and does not own up to them with +N below its title\n%s", term.Snapshot())
		}
		// Each row is ROWnn- and seventy digits, so anything past the rail's
		// width that is a long run of digits is a row that was not cut.
		for r := range rows {
			line := []rune(s.Line(r))
			if len(line) > railWidth && strings.Contains(string(line[railWidth:]), "0123456789") {
				t.Fatalf("row %d ran past the rail's width into the panes: %q\n%s", r, string(line), term.Snapshot())
			}
		}
	})
	t.Run("no-trailing-newline", func(t *testing.T) {
		term, _ := railCustomClient(t, "sessions,terminals,custom:40",
			"command = \"printf 'ONLYROW'\"")
		waitRailShows(t, term, "ONLYROW", "a last line with no newline")
	})
}

// TestRailCustomSectionStripsControlSequences: SGR survives and every other
// control sequence goes. The red of RED is the positive half, read off the
// cell; the OSC title and the erase are the negative half.
//
// Negative control: in dockLines (internal/app/dock_engine.go) append the
// raw line instead of dockSanitize(line). The OSC payload lands on the rail
// as text and the wait for the absence of TITLE fails.
func TestRailCustomSectionStripsControlSequences(t *testing.T) {
	term, _ := railCustomClient(t, "sessions,terminals,custom:40",
		"command = \"printf '\\\\033[31mRED\\\\033[0m \\\\033[2J\\\\033]0;TITLE\\\\007PLAIN\\\\n'\"")
	waitRailShows(t, term, "RED PLAIN", "a coloured row")
	s := term.Screen()
	if railRowOf(s, "TITLE") >= 0 || railRowOf(s, "0;") >= 0 {
		t.Fatalf("an OSC payload reached the rail as text\n%s", term.Snapshot())
	}
	row := railRowOf(s, "RED PLAIN")
	col := strings.Index(railLine(s, row), "RED")
	red := s.Cell(col, row).Fg
	if red.Kind != tuitest.ColorIndexed || red.Index != 1 {
		t.Fatalf("the SGR red did not survive: RED is drawn in %+v\n%s", red, term.Snapshot())
	}
	plain := s.Cell(col+4, row).Fg
	if plain.Kind == tuitest.ColorIndexed && plain.Index == 1 {
		t.Fatalf("the SGR reset was lost: PLAIN is drawn red too\n%s", term.Snapshot())
	}
	if railRowOf(s, "sessions") < 0 {
		t.Fatalf("the erase sequence reached the screen: the rail's own header is gone\n%s", term.Snapshot())
	}
}

// TestRailCustomSectionEmptyOnFailure: a command that exits non-zero, prints
// nothing or times out leaves the title over an empty section, never the
// previous run's rows, and list-dock-components says why. A refresh after
// the fix brings the rows back, which is the positive half.
//
// Negative control: in applyUpdate (internal/app/dock_engine.go) keep text
// when u.Err is set for a MultiLine component. GOOD stays on the rail after
// the failure.
func TestRailCustomSectionEmptyOnFailure(t *testing.T) {
	t.Run("exit", func(t *testing.T) {
		flag := filepath.Join(t.TempDir(), "fail")
		term, base := railCustomClient(t, "sessions,terminals,custom:40",
			"command = \"if [ -f "+flag+" ]; then exit 3; fi; echo GOOD\"\nrefresh = \"once\"")
		waitRailShows(t, term, "GOOD", "before the failure")
		if err := os.WriteFile(flag, nil, 0o600); err != nil {
			t.Fatalf("set the flag: %v", err)
		}
		refreshRail(t, base)
		waitRailLacks(t, term, "GOOD", "after the command failed")
		waitRailShows(t, term, "Custom", "after the command failed")
		c := waitRailComponent(t, base, "a failed run", func(c railComponent) bool { return c.LastExit == 3 })
		if c.Text != "" || c.Side != "rail" {
			t.Fatalf("the listing kept text for a failed run: %+v", c)
		}
		if err := os.Remove(flag); err != nil {
			t.Fatalf("clear the flag: %v", err)
		}
		refreshRail(t, base)
		waitRailShows(t, term, "GOOD", "after the fix")
	})
	t.Run("nothing", func(t *testing.T) {
		runs := countFile(t)
		term, base := railCustomClient(t, "sessions,terminals,custom:40",
			"command = \"echo x >> "+runs+"\"")
		waitRailShows(t, term, "Custom", "a command that prints nothing")
		// The zero values of the listing look like a silent run, so wait
		// for proof that the command ran at all.
		deadline := time.Now().Add(uiTimeout)
		for runCount(t, runs) < 1 && time.Now().Before(deadline) {
			time.Sleep(100 * time.Millisecond)
		}
		if runCount(t, runs) < 1 {
			t.Fatalf("the command never ran\n%s", term.Snapshot())
		}
		c := waitRailComponent(t, base, "a silent run", func(c railComponent) bool { return c.LastExit == 0 })
		if c.Text != "" || c.Visible {
			t.Fatalf("a silent command was reported as drawing: %+v", c)
		}
	})
	t.Run("timeout", func(t *testing.T) {
		term, base := railCustomClient(t, "sessions,terminals,custom:40",
			"command = \"echo PARTIAL; sleep 30\"")
		waitRailShows(t, term, "Custom", "a command that hangs")
		c := waitRailComponent(t, base, "a timed out run", func(c railComponent) bool {
			return strings.Contains(c.LastErr, "timed out")
		})
		if c.Text != "" || railRowOf(term.Screen(), "PARTIAL") >= 0 {
			t.Fatalf("output written before the hang was shown: %+v\n%s", c, term.Snapshot())
		}
	})
}

// TestRailCustomSectionEnvFollowsFocus: each run sees the focused pane's id
// and folder, the section's name, and the rail's width and the section's
// height, read fresh for that run.
//
// Negative control: drop the m.dockEngine.SetRailContext calls from
// syncRailContext (internal/app/sidebar_custom.go) and InitDockComponents
// (internal/app/dock_runtime.go). Every run sees an empty pane id and the id
// assertion fails.
func TestRailCustomSectionEnvFollowsFocus(t *testing.T) {
	term, base := railCustomClient(t, "sessions,terminals,custom:50",
		"command = \"echo ID=$TUIOS_ACTIVE_PANE_ID; echo CWD=${TUIOS_ACTIVE_PANE_CWD##*/}; echo SEC=$TUIOS_RAIL_SECTION; echo W=$TUIOS_RAIL_WIDTH; echo H=$TUIOS_RAIL_HEIGHT\"\nrefresh = \"event:window-focused\"")
	renameWindow(t, term, "FIRST")
	dir := t.TempDir()
	if out, err := tuiosCLI(t, base, "new-window", "SECOND", "-s", "e2e", "--no-focus", "--cwd", dir); err != nil {
		t.Fatalf("new-window: %v\n%s", err, out)
	}
	waitWindowCount(t, term, 2, "opening the second pane")
	focusPane(t, base, "SECOND")
	waitRailShows(t, term, "ID="+shortID(windowID(t, base, "e2e", "SECOND")), "after focusing SECOND")
	s := term.Screen()
	if railRowOf(s, "CWD="+filepath.Base(dir)) < 0 {
		t.Fatalf("the run did not see the focused pane's folder %s\n%s", dir, term.Snapshot())
	}
	if railRowOf(s, "SEC=custom") < 0 {
		t.Fatalf("the run did not see TUIOS_RAIL_SECTION=custom\n%s", term.Snapshot())
	}
	if railRowOf(s, "W=26") < 0 {
		t.Fatalf("a 28 column rail did not give the run 26 columns\n%s", term.Snapshot())
	}
	hRow := railRowOf(s, "H=")
	if hRow < 0 {
		t.Fatalf("the run did not see TUIOS_RAIL_HEIGHT\n%s", term.Snapshot())
	}
	// The rail's line ends in its edge rule, so the number is read by pattern
	// rather than by trimming.
	h := 0
	if m := railHeightRe.FindStringSubmatch(railLine(s, hRow)); m != nil {
		h, _ = strconv.Atoi(m[1])
	}
	if h <= 0 || h >= 40 {
		t.Fatalf("TUIOS_RAIL_HEIGHT is %q, want a count between 1 and the screen's rows\n%s",
			railLine(s, hRow), term.Snapshot())
	}
	focusPane(t, base, "FIRST")
	waitRailShows(t, term, "ID="+shortID(windowID(t, base, "e2e", "FIRST")), "after focusing FIRST again")
	if railRowOf(term.Screen(), "CWD="+filepath.Base(dir)) >= 0 {
		t.Fatalf("FIRST is focused and the run still reports SECOND's folder\n%s", term.Snapshot())
	}
}

// TestRailCustomCommandIsNotSettable: set-config refuses the command, and
// the positive half in the same fixture: set-config places the section, so
// membership is settable and only the command is not.
//
// Negative control: delete the "appearance.sidebar.custom" entry from
// optionWalkSkips and add a registry entry for the command. The set succeeds
// and PWNED reaches the rail.
func TestRailCustomCommandIsNotSettable(t *testing.T) {
	term, base := railCustomClient(t, "sessions,terminals", "command = \"echo FROMFILE\"")
	out, err := tuiosCLI(t, base, "set-config", "appearance.sidebar.custom.command", "echo PWNED", "-s", "e2e")
	if err == nil {
		t.Fatalf("set-config set the rail command:\n%s", out)
	}
	if !strings.Contains(out, "no such option") {
		t.Fatalf("set-config refused for another reason: %v\n%s", err, out)
	}
	if out, err := tuiosCLI(t, base, "set-config", "appearance.sidebar.sections", "sessions,terminals,custom", "-s", "e2e"); err != nil {
		t.Fatalf("set-config appearance.sidebar.sections: %v\n%s", err, out)
	}
	waitRailShows(t, term, "FROMFILE", "after placing the section with set-config")
	if railRowOf(term.Screen(), "PWNED") >= 0 {
		t.Fatalf("the refused command ran\n%s", term.Snapshot())
	}
}

// TestRailCustomSectionKeepsOnePendingRerun: an event that lands while the
// command is running is not dropped. One re-run follows, and a burst of two
// such events costs one run, not two.
//
// Negative control: in fire (internal/app/dock_engine.go) keep the plain
// drop-if-in-flight for Coalesce components. The rail stays on TWO.
func TestRailCustomSectionKeepsOnePendingRerun(t *testing.T) {
	runs := countFile(t)
	mark := filepath.Join(filepath.Dir(runs), "mark")
	if err := os.WriteFile(mark, []byte("ONE\n"), 0o600); err != nil {
		t.Fatalf("write mark: %v", err)
	}
	term, base := railCustomClient(t, "sessions,terminals,custom:40",
		"command = \"echo x >> "+runs+"; cat "+mark+"; sleep 2\"\nrefresh = \"event:window-focused\"")
	renameWindow(t, term, "FIRST")
	if out, err := tuiosCLI(t, base, "new-window", "SECOND", "-s", "e2e", "--no-focus"); err != nil {
		t.Fatalf("new-window: %v\n%s", err, out)
	}
	waitWindowCount(t, term, 2, "opening the second pane")
	waitRailShows(t, term, "ONE", "the run at attach")
	// Let whatever the setup started finish, then count from here: the two
	// focus events below must cost exactly two runs, the event's own and
	// the one pending re-run.
	time.Sleep(3 * time.Second)
	before := runCount(t, runs)

	if err := os.WriteFile(mark, []byte("TWO\n"), 0o600); err != nil {
		t.Fatalf("write mark: %v", err)
	}
	focusPane(t, base, "SECOND")
	// The debounce is 200 ms and the run sleeps two seconds, so at 700 ms the
	// second run is in flight. Two events land on it; one re-run must follow.
	time.Sleep(700 * time.Millisecond)
	if err := os.WriteFile(mark, []byte("THREE\n"), 0o600); err != nil {
		t.Fatalf("write mark: %v", err)
	}
	focusPane(t, base, "FIRST")
	focusPane(t, base, "SECOND")
	waitRailShows(t, term, "THREE", "after an event landed mid-run")
	time.Sleep(3 * time.Second)
	if n := runCount(t, runs) - before; n != 2 {
		t.Fatalf("three focus events, two of them mid-run, cost %d runs, want 2: the event's own and one pending re-run", n)
	}
}
