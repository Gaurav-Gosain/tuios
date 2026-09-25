package tuie2e

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// Every overlay owns the ground of its rectangle, read off the terminal the
// real binary draws on.
//
// A cell inside an overlay that the terminal shows in its own ground, or the
// desktop's, is a hole in the panel: two dark cells left of the Inbox title
// was one. The frame tuios composes can be right and the terminal still show
// the hole, because what reaches the terminal is a stream of cursor moves and
// writes and not the frame: that hole was tmux dropping the background under
// a hard tab the renderer moved with. So this reads cells the way a person
// sees them, after the whole stream, and also checks the stream itself never
// moves with HT.
//
// Each overlay is opened on a client with two panes and the rail behind it,
// at 80x24 and 120x40, in a dark and a light theme, with the backgrounds off
// and on the theme's ground. The overlay's cells are the ones that changed
// when it opened, and a hole is a cell of the ground colour with the panel's
// own colour on both sides of it, across or down. The title row of a titled
// panel has no ground cell at all.
//
// Every frame is saved, plain and styled, under artifactDir, so a failure
// has the screen it failed on and a pass has the screens it passed on.
//
// How this could pass while a hole is on screen, written down first:
//   - the overlay did not open, so nothing changed: each step waits for text
//     only the open overlay draws, and a component smaller than a panel fails;
//   - the hole sits on a panel's outermost column or row, with no panel
//     colour beyond it: the check across needs panel colour both sides, so a
//     whole missing edge column is not caught, which is not the failure seen;
//   - the terminal emulator tuitest runs is not tmux: the HT check is what
//     covers that, since a stream without HT cannot hit the tmux tab cell.
//
// The Inbox is also held to two placement rules on the way: its top row stays
// put while its height changes with the item under the cursor, and at 120
// columns it is drawn beside the rail rather than over its edge.
//
// Negative controls, run: with the Panel's title row built with bare spaces
// for its padding, every titled step fails on its title row; with the
// clients leaving hard tabs on, the stream check fails.

// artifactDir is where a test's frames are kept: $TUIOS_E2E_FRAMES when that
// is set, else tuios-e2e-artifacts under the temporary directory, in a
// directory named for the test.
func artifactDir(t *testing.T) string {
	t.Helper()
	root := os.Getenv("TUIOS_E2E_FRAMES")
	if root == "" {
		root = filepath.Join(os.TempDir(), "tuios-e2e-artifacts")
	}
	dir := filepath.Join(root, regexp.MustCompile(`[^A-Za-z0-9_.-]+`).ReplaceAllString(t.Name(), "_"))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("make the artifact directory: %v", err)
	}
	return dir
}

// saveArtifact writes the screen, plain and styled, into dir.
func saveArtifact(t *testing.T, term *tuitest.Terminal, dir, name string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name+".txt"), []byte(term.Snapshot()), 0o644); err != nil {
		t.Errorf("save %s: %v", name, err)
	}
	if err := os.WriteFile(filepath.Join(dir, name+".styled.txt"), []byte(term.SnapshotStyled()), 0o644); err != nil {
		t.Errorf("save %s: %v", name, err)
	}
}

// groundCells is a screen's cells, row by row.
type groundCells [][]tuitest.Cell

func readCells(s tuitest.Screen) groundCells {
	cols, rows := s.Size()
	out := make(groundCells, rows)
	for y := range rows {
		out[y] = make([]tuitest.Cell, cols)
		for x := range cols {
			out[y][x] = s.Cell(x, y)
		}
	}
	return out
}

// modeBg is the most common background among the cells.
func modeBg(cells [][]tuitest.Cell) tuitest.Color {
	count := map[tuitest.Color]int{}
	var best tuitest.Color
	for _, row := range cells {
		for _, c := range row {
			count[c.Bg]++
			if count[c.Bg] > count[best] {
				best = c.Bg
			}
		}
	}
	return best
}

// overlayRect is the overlay the step opened: the largest 4-connected run of
// cells that changed from before to after and are not on the ground, its
// bounding box, and the panel's own colour, the commonest in it.
type overlayRect struct {
	x0, y0, x1, y1 int
	cells          int
	panel          tuitest.Color
}

func findOverlay(before, after groundCells, ground tuitest.Color) overlayRect {
	rows := len(after)
	if rows == 0 {
		return overlayRect{}
	}
	cols := len(after[0])
	in := func(x, y int) bool {
		a, b := after[y][x], before[y][x]
		return a.Bg != ground && (a.Bg != b.Bg || a.Content != b.Content || a.Fg != b.Fg)
	}
	seen := make([][]bool, rows)
	for y := range seen {
		seen[y] = make([]bool, cols)
	}
	var best overlayRect
	for sy := range rows {
		for sx := range cols {
			if seen[sy][sx] || !in(sx, sy) {
				continue
			}
			r := overlayRect{x0: sx, y0: sy, x1: sx, y1: sy}
			var member [][2]int
			stack := [][2]int{{sx, sy}}
			seen[sy][sx] = true
			for len(stack) > 0 {
				p := stack[len(stack)-1]
				stack = stack[:len(stack)-1]
				member = append(member, p)
				r.x0, r.x1 = min(r.x0, p[0]), max(r.x1, p[0])
				r.y0, r.y1 = min(r.y0, p[1]), max(r.y1, p[1])
				for _, d := range [][2]int{{1, 0}, {-1, 0}, {0, 1}, {0, -1}} {
					x, y := p[0]+d[0], p[1]+d[1]
					if x >= 0 && y >= 0 && x < cols && y < rows && !seen[y][x] && in(x, y) {
						seen[y][x] = true
						stack = append(stack, [2]int{x, y})
					}
				}
			}
			r.cells = len(member)
			if r.cells > best.cells {
				count := map[tuitest.Color]int{}
				for _, p := range member {
					count[after[p[1]][p[0]].Bg]++
				}
				for c, n := range count {
					if n > count[r.panel] {
						r.panel = c
					}
				}
				best = r
			}
		}
	}
	return best
}

// holes lists the ground cells inside the overlay with its panel colour on
// both sides of them, across or down, and every ground cell on its title row.
func holes(after groundCells, r overlayRect, ground tuitest.Color, titleRow int) []string {
	var out []string
	panelAt := func(x, y int) bool { return after[y][x].Bg == r.panel }
	for y := r.y0; y <= r.y1; y++ {
		for x := r.x0; x <= r.x1; x++ {
			c := after[y][x]
			if c.Bg != ground || c.Width == 0 {
				continue
			}
			left, right, up, down := false, false, false, false
			for i := x - 1; i >= r.x0 && !left; i-- {
				left = panelAt(i, y)
			}
			for i := x + 1; i <= r.x1 && !right; i++ {
				right = panelAt(i, y)
			}
			for i := y - 1; i >= r.y0 && !up; i-- {
				up = panelAt(x, i)
			}
			for i := y + 1; i <= r.y1 && !down; i++ {
				down = panelAt(x, i)
			}
			if (left && right) || (up && down) || y == titleRow {
				out = append(out, fmt.Sprintf("(%d,%d) %q", x, y, c.Content))
			}
		}
	}
	return out
}

// groundStep is one overlay state: the keys that reach it from the step
// before, the text that says it is drawn, and whether it is a panel with a
// title row under its top pad.
type groundStep struct {
	name   string
	keys   []any
	want   []string
	titled bool
	// fresh means the step opens the overlay from a closed screen; the others
	// move within the one open before them and are measured against the same
	// closed screen.
	fresh bool
	// close are the keys that close it after the last step that uses it.
	close []any
	// check is anything further to assert on the step's screen.
	check func(t *testing.T, s tuitest.Screen)
	// do opens it when keys cannot, such as with the mouse.
	do func(t *testing.T, term *tuitest.Terminal)
}

// startHookIn is startHeldHook for a window of a session.
func startHookIn(t *testing.T, base, session, window, payload string) {
	t.Helper()
	cmd := exec.Command(tuiosBin, "agent-hook", "claude-code", "--session", session, "--window", window)
	cmd.Dir = workDirIn(t, base)
	cmd.Env = append(os.Environ(), "SHELL=/bin/sh")
	for _, key := range xdgKeys {
		cmd.Env = append(cmd.Env, key+"="+xdgDir(base, key))
	}
	cmd.Stdin = strings.NewReader(payload)
	if err := cmd.Start(); err != nil {
		t.Fatalf("start the hook: %v", err)
	}
	done := make(chan struct{})
	go func() { _ = cmd.Wait(); close(done) }()
	t.Cleanup(func() { _ = cmd.Process.Kill(); <-done })
}

// groundClient is a client on the shipped looks, with the theme and the
// background given, attached to a session of two panes, and an Inbox holding
// a risky approval, a plan, an error and a finished job.
func groundClient(t *testing.T, cols, rows int, theme, background string, out *syncBuffer) *tuitest.Terminal {
	t.Helper()
	base := t.TempDir()
	killDaemon(t, base)
	useShippedLooks(base)
	dir := filepath.Join(xdgDir(base, "XDG_CONFIG_HOME"), "tuios")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	cfg := "[appearance]\ntheme = \"" + theme + "\"\n"
	if background != "" {
		cfg += "background = \"" + background + "\"\n"
	}
	cfg += "\n[agents.approvals]\nenabled = [\"claude-code\"]\nhold_seconds = 300\n"
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"new", "e2e-home", "--detach"},
		{"new-window", "second", "-s", "e2e-home", "--no-focus"},
		{"new", "e2e-agent", "--detach"},
		{"new-window", "planner", "-s", "e2e-agent", "--no-focus"},
		{"new", "tests", "--detach"},
		{"set-agent-state", "-s", "tests", "errored", "--harness", "codex", "-m", "go test ./... failed: 2 packages"},
		{"new", "notes", "--detach"},
		{"set-agent-state", "-s", "notes", "done", "--harness", "gemini-cli", "-m", "CHANGELOG.md updated for 0.9"},
	} {
		if o, err := tuiosCLI(t, base, args...); err != nil {
			t.Fatalf("%v: %v\n%s", args, err, o)
		}
	}
	startHookIn(t, base, "e2e-agent", "0", `{"hook_event_name":"PermissionRequest","session_id":"e2e-risk","tool_name":"Bash","tool_input":{"command":"rm -rf build/ && git push --force origin main"}}`)
	startHookIn(t, base, "e2e-agent", "1", `{"hook_event_name":"PermissionRequest","session_id":"e2e-plan","permission_mode":"plan","tool_name":"ExitPlanMode","tool_input":{"plan":"# Make the retry limit configurable\n1. Add a MaxAttempts field.\n2. Thread it through Do.\n3. Add table tests.","planFilePath":"/tmp/plan.md"}}`)

	term := attachIn(t, base, "e2e-home", startOpts{cols: cols, rows: rows, shippedLooks: true,
		env: []string{"COLORTERM=truecolor"}, out: out})
	if err := term.WaitFor(func(s tuitest.Screen) bool { return countWindows(s) >= 2 }, bootTimeout); err != nil {
		t.Fatalf("the two panes never showed: %v\n%s", err, term.Snapshot())
	}
	// Tiled, so the panes fill the screen behind every overlay and the
	// context menu's click lands on one.
	enableTiling(t, term)
	return term
}

// groundSteps are the overlays, in the order one client opens them.
func groundSteps() []groundStep {
	noKeysRow := func(t *testing.T, s tuitest.Screen) {
		t.Helper()
		if strings.Contains(s.Text(), "[1/3]") {
			t.Errorf("an approval's row repeats the keys its footer names:\n%s", s.Text())
		}
	}
	return []groundStep{
		{name: "inbox-approval", fresh: true, titled: true, keys: []any{tuitest.Ctrl('b'), "i"},
			want: []string{"Approvals 1", "risky: approve Bash", "Plans 1", "1 allow"}, check: noKeysRow},
		{name: "inbox-plan", titled: true, keys: []any{"j"}, want: []string{"Add a MaxAttempts field", "keep planning"}},
		{name: "inbox-errored", titled: true, keys: []any{"j"}, want: []string{"go test ./... failed", "snooze"}},
		{name: "inbox-snooze-picker", titled: true, keys: []any{"z"}, want: []string{"15m", "until it changes"},
			close: []any{tuitest.Esc, tuitest.Esc}},
		{name: "help", fresh: true, titled: true, keys: []any{tuitest.Ctrl('b'), "?"}, want: []string{"Keybindings", "search"},
			close: []any{tuitest.Esc}},
		{name: "palette", fresh: true, titled: true, keys: []any{tuitest.Ctrl('p')}, want: []string{"Command Palette"},
			close: []any{tuitest.Esc}},
		{name: "settings", fresh: true, titled: true, keys: []any{","}, want: []string{"Settings"},
			close: []any{tuitest.Esc}},
		{name: "keybinds", fresh: true, titled: true, keys: []any{tuitest.Ctrl('b'), "k"}, want: []string{keybindTitle},
			close: []any{tuitest.Esc}},
		{name: "launcher", fresh: true, titled: true, keys: []any{altSpace}, want: []string{launcherTitle},
			close: []any{tuitest.Esc}},
		{name: "sessions", fresh: true, titled: true, keys: []any{tuitest.Ctrl('b'), "S"}, want: []string{"Sessions", "e2e-agent"},
			close: []any{tuitest.Esc}},
		{name: "workspaces", fresh: true, titled: true, keys: []any{tuitest.Ctrl('b'), "W"}, want: []string{"Workspaces"},
			close: []any{tuitest.Esc}},
		{name: "mail", fresh: true, titled: true, keys: []any{tuitest.Ctrl('b'), "M", "m"}, want: []string{"No mail."},
			close: []any{tuitest.Esc, tuitest.Esc}},
		{name: "which-key", fresh: true, titled: true, keys: []any{tuitest.Ctrl('b')}, want: []string{"prefix", "Focus pane in a direction"},
			close: []any{tuitest.Esc}, check: descriptionsAligned},
		{name: "context-menu", fresh: true, want: []string{"Close pane"}, close: []any{tuitest.Esc},
			do: func(t *testing.T, term *tuitest.Terminal) {
				mousePress(t, term, 20, 10, tuitest.MouseRight, 0)
				mouseRelease(t, term, 20, 10, tuitest.MouseRight, 0)
			}},
		{name: "capture-hints", fresh: true, keys: []any{tuitest.Ctrl('b'), "C"}, want: []string{"full screen"},
			close: []any{tuitest.Esc}},
	}
}

// TestEveryOverlayOwnsItsGround opens each overlay under each look and reads
// its cells.
func TestEveryOverlayOwnsItsGround(t *testing.T) {
	for _, size := range [][2]int{{80, 24}, {120, 40}} {
		for _, theme := range []string{"catppuccin_mocha", "catppuccin_latte"} {
			for _, bg := range []string{"", "theme"} {
				name := fmt.Sprintf("%dx%d-%s-bg_%s", size[0], size[1], theme, map[string]string{"": "off", "theme": "theme"}[bg])
				t.Run(name, func(t *testing.T) {
					out := &syncBuffer{}
					term := groundClient(t, size[0], size[1], theme, bg, out)
					dir := artifactDir(t)
					var closed groundCells
					inboxTop := -1
					for _, st := range groundSteps() {
						if st.fresh {
							if err := term.WaitStable(uiTimeout); err != nil {
								t.Fatalf("%s: the screen never settled before opening: %v", st.name, err)
							}
							closed = readCells(term.Screen())
							saveArtifact(t, term, dir, "closed-before-"+st.name)
						}
						sendKeys(t, term, st.keys...)
						if st.do != nil {
							st.do(t, term)
						}
						waitScreen(t, term, st.name+" never drew", st.want...)
						if err := term.WaitStable(uiTimeout); err != nil {
							t.Fatalf("%s: the screen never settled: %v", st.name, err)
						}
						s := term.Screen()
						saveArtifact(t, term, dir, st.name)
						after := readCells(s)
						ground := modeBg(closed)
						r := findOverlay(closed, after, ground)
						cols, rows := s.Size()
						if r.cells < 20 || r.x1-r.x0 < 12 {
							t.Errorf("%s: found no overlay in the cells that changed (%d cells)\n%s", st.name, r.cells, term.Snapshot())
						} else {
							title := -1
							if st.titled {
								title = r.y0 + 1
								if r.y0 == 0 && after[0][r.x0].Bg != r.panel {
									title = r.y0
								}
							}
							if h := holes(after, r, ground, title); len(h) > 0 {
								t.Errorf("%s at %dx%d: %d cells of the ground inside the overlay at (%d,%d)-(%d,%d), first %v\n%s",
									st.name, cols, rows, len(h), r.x0, r.y0, r.x1, r.y1, h[:min(len(h), 6)], term.Snapshot())
							}
						}
						// The Inbox keeps its top row while its height follows
						// the item under the cursor, and it fits beside the rail
						// at 120 columns, so it is drawn there.
						if strings.HasPrefix(st.name, "inbox-") && r.cells >= 20 {
							if inboxTop < 0 {
								inboxTop = r.y0
							} else if r.y0 != inboxTop {
								t.Errorf("%s: the Inbox moved from row %d to row %d", st.name, inboxTop, r.y0)
							}
							if rail := railHeaderColumn(s); cols >= 120 && rail > 0 && r.x1 >= rail-1 {
								t.Errorf("%s: the Inbox runs to column %d, over the rail at %d\n%s", st.name, r.x1, rail, term.Snapshot())
							}
						}
						if st.check != nil {
							st.check(t, s)
						}
						if len(st.close) > 0 {
							sendKeys(t, term, st.close...)
							waitGone(t, term, st.name, st.want[0])
						}
					}
					if i := bytes.IndexByte([]byte(out.String()), '\t'); i >= 0 {
						t.Errorf("the client moved the cursor with a hard tab at byte %d of its output; "+
							"tmux writes a tab cell without the background the blanks had", i)
					}
					t.Logf("frames in %s", dir)
				})
			}
		}
	}
}

// waitGone waits for text to leave the screen.
func waitGone(t *testing.T, term *tuitest.Terminal, what, text string) {
	t.Helper()
	if err := term.WaitFor(func(s tuitest.Screen) bool { return !strings.Contains(s.Text(), text) }, uiTimeout); err != nil {
		t.Fatalf("%s did not close: %v\n%s", what, err, term.Snapshot())
	}
	time.Sleep(insertGuard)
}

// descriptionsAligned fails unless which-key's descriptions start on one
// column. The arrows key is three bytes a cell, and a width counted in bytes
// put its description left of every other one.
func descriptionsAligned(t *testing.T, s tuitest.Screen) {
	t.Helper()
	col := func(text string) int {
		y := rowWith(s, text)
		if y < 0 {
			return -1
		}
		return len([]rune(s.Line(y)[:strings.Index(s.Line(y), text)]))
	}
	if a, b := col("Create window"), col("Focus pane in a direction"); a < 0 || a != b {
		t.Errorf("which-key descriptions start at columns %d and %d\n%s", a, b, s.Text())
	}
}
