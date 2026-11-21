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

// TestDaemonCreatedWindowsTile drives the exact scenario the owner reported:
// with a client attached and tiling on, windows are created through the daemon
// path (tuios run-command NewWindow, the same path 'tuios new'/CLI callers use)
// rather than interactively. Every such window must join the tiled layout, so
// the six windows partition the screen with no overlap.
func TestDaemonCreatedWindowsTile(t *testing.T) {
	term, base := start(t, startOpts{cols: 120, rows: 40, args: []string{"new", "e2e"}})
	killDaemon(t, base)

	waitBoot(t, term)

	newWindow(t, term)
	waitWindowCount(t, term, 1, "first window")
	enableTiling(t, term)

	for i := 2; i <= 6; i++ {
		out, err := tuiosCLI(t, base, "run-command", "NewWindow")
		if err != nil {
			t.Fatalf("run-command NewWindow #%d failed: %v\n%s", i, err, out)
		}
		waitWindowCount(t, term, i, fmt.Sprintf("after daemon NewWindow #%d", i))
	}

	rects := waitForSettledGeometry(t, base, 6)

	for _, r := range rects {
		if r.Width >= 120 {
			t.Errorf("window %s spans the full width (%d): it was never tiled", r.ID, r.Width)
		}
	}
	for a := range rects {
		for b := a + 1; b < len(rects); b++ {
			if geomOverlap(rects[a], rects[b]) {
				t.Errorf("windows overlap: %s (%d,%d %dx%d) and %s (%d,%d %dx%d)",
					rects[a].ID, rects[a].X, rects[a].Y, rects[a].Width, rects[a].Height,
					rects[b].ID, rects[b].X, rects[b].Y, rects[b].Width, rects[b].Height)
			}
		}
	}

	for _, r := range rects {
		t.Logf("window %s: (%d,%d) %dx%d", r.ID, r.X, r.Y, r.Width, r.Height)
	}
}

type winRect struct {
	ID                  string
	X, Y, Width, Height int
}

func geomOverlap(a, b winRect) bool {
	return a.X < b.X+b.Width && b.X < a.X+a.Width && a.Y < b.Y+b.Height && b.Y < a.Y+a.Height
}

func waitForSettledGeometry(t *testing.T, base string, n int) []winRect {
	t.Helper()
	return waitForSettledGeometryIn(t, base, "", n)
}

func waitForSettledGeometryIn(t *testing.T, base, session string, n int) []winRect {
	t.Helper()
	args := []string{"list-windows", "--json"}
	if session != "" {
		args = append(args, "--session", session)
	}
	var prev []winRect
	deadline := time.Now().Add(shellTimeout)
	stableSince := 0
	for time.Now().Before(deadline) {
		out, err := tuiosCLI(t, base, args...)
		if err != nil {
			time.Sleep(200 * time.Millisecond)
			continue
		}
		rects, ok := parseWindows(out)
		if !ok || len(rects) != n {
			time.Sleep(200 * time.Millisecond)
			continue
		}
		if sameGeometry(prev, rects) {
			stableSince++
			if stableSince >= 3 {
				return rects
			}
		} else {
			stableSince = 0
		}
		prev = rects
		time.Sleep(200 * time.Millisecond)
	}
	if prev != nil {
		return prev
	}
	t.Fatalf("daemon never reported %d windows with settled geometry", n)
	return nil
}

func sameGeometry(a, b []winRect) bool {
	if len(a) != len(b) {
		return false
	}
	byID := make(map[string]winRect, len(a))
	for _, r := range a {
		byID[r.ID] = r
	}
	for _, r := range b {
		p, ok := byID[r.ID]
		if !ok || p != r {
			return false
		}
	}
	return true
}

func parseWindows(jsonOut string) ([]winRect, bool) {
	var payload struct {
		Windows []struct {
			ID     string `json:"window_id"`
			X      int    `json:"x"`
			Y      int    `json:"y"`
			Width  int    `json:"width"`
			Height int    `json:"height"`
		} `json:"windows"`
	}
	if err := json.Unmarshal([]byte(jsonOut), &payload); err != nil {
		return nil, false
	}
	rects := make([]winRect, 0, len(payload.Windows))
	for _, w := range payload.Windows {
		rects = append(rects, winRect{ID: w.ID, X: w.X, Y: w.Y, Width: w.Width, Height: w.Height})
	}
	return rects, true
}

// TestNewPaneShellStartsAtItsSize opens panes into a tiled layout and checks
// that each new pane's shell starts at the size its tile gives it and takes no
// SIGWINCH while it starts (#1205).
//
// The shell was started at the session's size, or at the floating placement
// box, and resized once the client tiled the pane. zsh started in that window
// took the SIGWINCH during startup and drew its prompt again under PROMPT_SP's
// "%" mark, with empty rows above it. The rc file follows the reporter's: it
// logs the size at start, every SIGWINCH and every prompt, and sleeps 0.1 s so
// the resize lands while the shell is still starting.
//
// The shell is bash, not the reporter's zsh. The CI runners have no zsh, and a
// pane whose shell does not exist is never made, so the test would fail at the
// first new pane. bash is also the better witness: it runs its WINCH trap for
// a resize that lands during the rc file's sleep, where zsh's TRAPWINCH misses
// it because the signal goes to the sleep's process group.
//
// The test waits on conditions, not on time. A pane is judged once its shell
// has drawn a prompt and the frame on screen holds the size the shell last
// logged. The next pane is made only then.
//
// A pane that is split gets a SIGWINCH, which is right: its tile shrank. Only
// the new pane is held to none. A deliberate terminal resize must reach that
// same trap afterwards, so a dead trap cannot make the assertion pass. The
// shell's first observed kernel size is compared with the real frame too;
// a resize before its rc file runs is outside that observation. Call-site
// negative controls pin the sized requests. Logs and frames are saved as
// artifacts.
func TestNewPaneShellStartsAtItsSize(t *testing.T) {
	const shell = "/bin/bash"
	if _, err := os.Stat(shell); err != nil {
		t.Skipf("%s is not installed", shell)
	}
	cases := []struct {
		name, layout string
		args         []string
		animations   bool
		cols, rows   int
		resized      bool
	}{
		{"daemon", "bsp", []string{"new", "winch"}, false, 160, 45, false},
		{"daemon-animated", "bsp", []string{"new", "winch"}, true, 160, 45, false},
		{"standalone", "bsp", nil, false, 160, 45, false},
		{"standalone-animated", "bsp", nil, true, 160, 45, false},
		{"daemon-scrolling", "scrolling", []string{"new", "winch"}, false, 160, 45, false},
		{"standalone-scrolling", "scrolling", nil, false, 160, 45, false},
		{"daemon-master-stack", "master-stack", []string{"new", "winch"}, false, 160, 45, false},
		{"standalone-master-stack", "master-stack", nil, false, 160, 45, false},
		{"daemon-master-narrow", "master-stack", []string{"new", "winch"}, false, 100, 30, false},
		{"daemon-bsp-resized", "bsp", []string{"new", "winch"}, false, 160, 45, true},
		{"daemon-master-resized", "master-stack", []string{"new", "winch"}, false, 160, 45, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			base := t.TempDir()
			writeConfig(t, base, fmt.Sprintf("[startup]\ntiled = true\nlayout = %q\n[workspaces]\nnew_window_when_empty = true\n[keybindings.global]\nscroll_focus_right = ['f6']\n", tc.layout))
			logPath := filepath.Join(base, "size.log")
			rc := "_log() { echo \"$$ $1 $(stty size) $EPOCHREALTIME\" >> " + logPath + "; }\n" +
				"trap '_log WINCH' WINCH\n_log start\nPROMPT_COMMAND='_log precmd'\nsleep 0.1\nPS1='p$$ $ '\n"
			if err := os.WriteFile(filepath.Join(xdgDir(base, "HOME"), ".bashrc"), []byte(rc), 0o600); err != nil {
				t.Fatal(err)
			}
			term := startIn(t, base, startOpts{cols: tc.cols, rows: tc.rows, args: tc.args, animations: tc.animations,
				env: []string{"SHELL=" + shell, "BASH_SILENCE_DEPRECATION_WARNING=1"}})
			if tc.args != nil {
				killDaemon(t, base)
			}
			events := func() [][]string {
				data, _ := os.ReadFile(logPath)
				var out [][]string
				for line := range strings.Lines(string(data)) {
					if f := strings.Fields(line); len(f) >= 4 {
						out = append(out, f)
					}
				}
				return out
			}
			seen := map[string]bool{}
			awaitNewShell := func(pane string) string {
				var pid string
				waitForFileText(t, logPath, func(string) bool {
					pid = ""
					for _, f := range events() {
						switch {
						case f[1] == "start" && pid == "" && !seen[f[0]]:
							pid = f[0]
						case f[1] == "precmd" && f[0] == pid && pid != "":
							return true
						}
					}
					return false
				}, pane+": its shell never started and drew a prompt")
				seen[pid] = true
				return pid
			}
			awaitTileSize := func(pane, pid string) string {
				if tc.layout == "scrolling" {
					sendKeys(t, term, tuitest.F6)
				}
				var size string
				err := term.WaitForDesc(pane+"'s frame holds the size its shell logged", func(s tuitest.Screen) bool {
					cols, rows, ok := frameAround(s, "p"+pid+" $")
					if !ok {
						return false
					}
					last := ""
					for _, f := range events() {
						if f[0] == pid {
							last = f[2] + "x" + f[3]
						}
					}
					size = fmt.Sprintf("%dx%d", rows, cols)
					return last == size
				}, uiTimeout)
				if err != nil {
					t.Fatalf("%s: %v\nlog:\n%s", pane, err, func() string {
						data, _ := os.ReadFile(logPath)
						return string(data)
					}())
				}
				return size
			}
			waitBoot(t, term)
			newWindow(t, term)
			enableTiling(t, term)
			if tc.args != nil {
				out, err := tuiosCLI(t, base, "run-command", "--json", "GetSessionInfo")
				if err != nil {
					t.Fatalf("read layout: %v\n%s", err, out)
				}
				var info struct {
					Layout string `json:"layout_mode"`
				}
				if err := json.Unmarshal([]byte(out), &info); err != nil || info.Layout != tc.layout {
					t.Fatalf("measured layout is %q, want %q: %v\n%s", info.Layout, tc.layout, err, out)
				}
			}
			awaitNewShell("the first pane")
			if tc.resized {
				newWindow(t, term)
				pid := awaitNewShell("resize setup")
				awaitTileSize("resize setup", pid)
				before := waitForSettledGeometry(t, base, 2)
				sendKeys(t, term, ">", ">")
				waitForShape(t, base, "winch", 2, "the resized divider", func(after []winRect) error {
					if sameGeometry(before, after) {
						return fmt.Errorf("the width keys moved no divider")
					}
					return nil
				})
				awaitTileSize("after the divider resize", pid)
			}
			type pane struct {
				name, pid, size string
				from            int
			}
			var panes []pane
			for i := range 3 {
				from := len(events())
				newWindow(t, term)
				name := fmt.Sprintf("new pane %d", i+1)
				pid := awaitNewShell(name)
				size := awaitTileSize(name, pid)
				panes = append(panes, pane{name, pid, size, from})
			}
			saveArtifact(t, term, artifactDir(t), "multiple-panes")
			savePNG(t, term.Screen(), hostPalette(t, ""), artifactDir(t), "multiple-panes")
			if tc.args != nil {
				from := len(events())
				sendKeys(t, term, tuitest.Alt("2"))
				exactlyPanesOn(t, term, base, "winch", 2, 1, "automatic empty-workspace pane")
				pid := awaitNewShell("empty workspace")
				size := awaitTileSize("empty workspace", pid)
				panes = append(panes, pane{"empty workspace", pid, size, from})
			}
			all := events()
			var text strings.Builder
			for _, f := range all {
				text.WriteString(strings.Join(f, " "))
				text.WriteByte('\n')
			}
			if err := os.WriteFile(filepath.Join(artifactDir(t), "size.log"), []byte(text.String()), 0o644); err != nil {
				t.Logf("could not save the log: %v", err)
			}
			for i, p := range panes {
				to := len(all)
				if i+1 < len(panes) {
					to = panes[i+1].from
				}
				start, winch := "", 0
				for _, f := range all[p.from:to] {
					switch {
					case f[0] != p.pid:
					case f[1] == "start":
						start = f[2] + "x" + f[3]
					case f[1] == "WINCH":
						winch++
					}
				}
				if start != p.size || winch != 0 {
					t.Errorf("%s (pid %s) started at %s, frame %s, and took %d SIGWINCH; a new pane's "+
						"shell must start at its tile's size\n%s", p.name, p.pid, start, p.size, winch, text.String())
				}
			}
			last := panes[len(panes)-1]
			from := len(events())
			if err := term.Resize(tc.cols-8, tc.rows-3); err != nil {
				t.Fatal(err)
			}
			waitForFileText(t, logPath, func(string) bool {
				for _, f := range events()[from:] {
					if f[0] == last.pid && f[1] == "WINCH" {
						return true
					}
				}
				return false
			}, "the new shell did not report a deliberate terminal resize")
			awaitTileSize("after terminal resize", last.pid)
			saveArtifact(t, term, artifactDir(t), "resized")
			savePNG(t, term.Screen(), hostPalette(t, ""), artifactDir(t), "resized")
			data, err := os.ReadFile(logPath)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(artifactDir(t), "size.log"), data, 0o644); err != nil {
				t.Fatal(err)
			}
			alive(t, term, "after opening panes")
		})
	}
}

func frameAround(s tuitest.Screen, marker string) (cols, rows int, ok bool) {
	vertical := func(r rune) bool { return strings.ContainsRune("│┃║╎┆┊", r) }
	w, h := s.Size()
	want := []rune(marker)
	for y := 1; y < h; y++ {
		for x := 1; x+len(want) <= w; x++ {
			hit := true
			for i, r := range want {
				if s.Cell(x+i, y).Rune != r {
					hit = false
					break
				}
			}
			if !hit {
				continue
			}
			left := x - 1
			if !vertical(s.Cell(left, y).Rune) || vertical(s.Cell(left, y-1).Rune) {
				return 0, 0, false
			}
			right := x
			for right < w && !vertical(s.Cell(right, y).Rune) {
				right++
			}
			bottom := y
			for bottom < h && vertical(s.Cell(left, bottom).Rune) {
				bottom++
			}
			if right >= w || bottom >= h {
				return 0, 0, false
			}
			return right - x, bottom - y, true
		}
	}
	return 0, 0, false
}
