package tuie2e

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
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

	// One window interactively, then turn tiling on. From here the attached
	// client is in tiling mode and has synced that to the daemon.
	newWindow(t, term)
	waitWindowCount(t, term, 1, "first window")
	enableTiling(t, term)

	// Create five more windows through the daemon verb path.
	for i := 2; i <= 6; i++ {
		out, err := tuiosCLI(t, base, "run-command", "NewWindow")
		if err != nil {
			t.Fatalf("run-command NewWindow #%d failed: %v\n%s", i, err, out)
		}
		waitWindowCount(t, term, i, fmt.Sprintf("after daemon NewWindow #%d", i))
	}

	// Give the client a beat to place and sync the final window back.
	rects := waitForSettledGeometry(t, base, 6)

	// The signature of the bug: a window still carrying the daemon's full-size
	// Unplaced box, or two windows on top of each other.
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

// waitForSettledGeometry polls list-windows --json until the daemon reports n
// windows whose geometry has stopped changing, so the assertion runs against the
// placed layout rather than a mid-flight frame.
func waitForSettledGeometry(t *testing.T, base string, n int) []winRect {
	t.Helper()
	return waitForSettledGeometryIn(t, base, "", n)
}

// waitForSettledGeometryIn is waitForSettledGeometry against a named session,
// which is what a test with more than one session in the daemon has to ask for:
// the default target is whichever session was active last.
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

// TestNewPaneShellStartsAtItsSize opens panes into a tiled layout with zsh
// as the shell and checks that each new pane's shell starts at the size its
// tile gives it and takes no SIGWINCH while it starts (#1205).
//
// The shell was started at the session's size, or at the floating placement
// box, and resized once the client tiled the pane. zsh started in that window
// took the SIGWINCH during startup and drew its prompt again under PROMPT_SP's
// "%" mark, with empty rows above it. The rc file is the reporter's: it logs
// the size at start, every SIGWINCH and every prompt, and sleeps 0.1 s so the
// resize lands while the shell is still starting.
//
// A pane that is split gets a SIGWINCH, which is right: its tile shrank. Only
// the new pane is held to none. The log is saved as an artifact.
func TestNewPaneShellStartsAtItsSize(t *testing.T) {
	cases := []struct {
		name       string
		args       []string
		animations bool
	}{
		{"daemon", []string{"new", "winch"}, false},
		{"daemon-animated", []string{"new", "winch"}, true},
		{"standalone", nil, false},
		{"standalone-animated", nil, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			base := t.TempDir()
			logPath := filepath.Join(base, "zsize.log")
			rc := "zmodload zsh/datetime\n" +
				"_log() { print -r -- \"$EPOCHREALTIME $$ $1 $(stty size)\" >> " + logPath + " }\n" +
				"_log start\nTRAPWINCH() { _log WINCH }\nprecmd() { _log precmd }\nsleep 0.1\nPS1='%% '\n"
			if err := os.WriteFile(filepath.Join(base, ".zshrc"), []byte(rc), 0o600); err != nil {
				t.Fatal(err)
			}
			term := startIn(t, base, startOpts{cols: 160, rows: 45, args: tc.args, animations: tc.animations,
				env: []string{"SHELL=/usr/bin/zsh", "ZDOTDIR=" + base}})
			if tc.args != nil {
				killDaemon(t, base)
			}
			waitBoot(t, term)
			newWindow(t, term)
			enableTiling(t, term)
			// Panes made before this point were not made into a tiled layout.
			time.Sleep(1500 * time.Millisecond)
			readLog := func() []string {
				data, _ := os.ReadFile(logPath)
				if s := strings.TrimSpace(string(data)); s != "" {
					return strings.Split(s, "\n")
				}
				return nil
			}
			// Each pane is judged on the log lines from its own keypress to the
			// next one. The SIGWINCH of the pane it split is the right answer
			// to the split, and comes from another pid.
			var segments [][]string
			for range 3 {
				mark := len(readLog())
				newWindow(t, term)
				time.Sleep(1500 * time.Millisecond)
				segments = append(segments, readLog()[mark:])
			}
			all := strings.Join(readLog(), "\n")
			if err := os.WriteFile(filepath.Join(artifactDir(t), "zsize.log"), []byte(all+"\n"), 0o644); err != nil {
				t.Logf("could not save the log: %v", err)
			}
			for i, seg := range segments {
				pid, start, winch := "", "", 0
				for _, line := range seg {
					f := strings.Fields(line)
					if len(f) < 5 {
						continue
					}
					switch {
					case f[2] == "start" && pid == "":
						pid, start = f[1], f[3]+"x"+f[4]
					case f[2] == "WINCH" && f[1] == pid:
						winch++
					}
				}
				if pid == "" {
					t.Fatalf("pane %d: its shell logged no start\n%s", i+1, all)
				}
				if winch != 0 {
					t.Errorf("new pane %d (pid %s) started at %s and took %d SIGWINCH; a new pane's "+
						"shell must start at its tile's size\n%s", i+1, pid, start, winch, all)
				}
			}
			alive(t, term, "after opening panes")
		})
	}
}
