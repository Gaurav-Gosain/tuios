package tuie2e

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

func artifactDir(t *testing.T) string {
	t.Helper()
	name := regexp.MustCompile(`[^A-Za-z0-9_.-]+`).ReplaceAllString(t.Name(), "_")
	root := os.Getenv("TUIOS_E2E_FRAMES")
	if root == "" {
		return scratchArtifactDir(t, name)
	}
	dir := filepath.Join(root, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("make the artifact directory: %v", err)
	}
	return dir
}

var (
	scratchArtifactMu   sync.Mutex
	scratchArtifactDirs = map[string]string{}
)

func scratchArtifactDir(t *testing.T, name string) string {
	scratchArtifactMu.Lock()
	defer scratchArtifactMu.Unlock()
	if dir, ok := scratchArtifactDirs[name]; ok {
		return dir
	}
	dir := t.TempDir()
	scratchArtifactDirs[name] = dir
	t.Cleanup(func() {
		scratchArtifactMu.Lock()
		delete(scratchArtifactDirs, name)
		scratchArtifactMu.Unlock()
		if t.Failed() {
			keepFailedArtifacts(t, dir, name)
		}
	})
	return dir
}

const failedArtifactRoot = "/tmp/tuios-e2e-artifacts"

func keepFailedArtifacts(t *testing.T, dir, name string) {
	if entries, err := os.ReadDir(dir); err != nil || len(entries) == 0 {
		return
	}
	dst := filepath.Join(failedArtifactRoot, name)
	if err := os.RemoveAll(dst); err != nil {
		t.Logf("keep the frames of %s: %v", name, err)
		return
	}
	if err := os.CopyFS(dst, os.DirFS(dir)); err != nil {
		t.Logf("keep the frames of %s: %v", name, err)
		return
	}
	t.Logf("frames of the failed test kept in %s", dst)
}

func saveArtifact(t *testing.T, term *tuitest.Terminal, dir, name string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name+".txt"), []byte(term.Snapshot()), 0o644); err != nil {
		t.Errorf("save %s: %v", name, err)
	}
	if err := os.WriteFile(filepath.Join(dir, name+".styled.txt"), []byte(term.SnapshotStyled()), 0o644); err != nil {
		t.Errorf("save %s: %v", name, err)
	}
}

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

type groundStep struct {
	name   string
	keys   []any
	want   []string
	titled bool
	fresh  bool
	close  []any
	check  func(t *testing.T, s tuitest.Screen)
	do     func(t *testing.T, term *tuitest.Terminal)
}

func startHookIn(t *testing.T, base, session, window, payload string) {
	t.Helper()
	want := heldApprovals(t, base, session) + 1
	for attempt := 1; ; attempt++ {
		cmd := exec.Command(tuiosBin, "agent-hook", "claude-code", "--session", session, "--window", window, "--explain")
		cmd.Dir = workDirIn(t, base)
		cmd.Env = append(os.Environ(), "SHELL=/bin/sh")
		for _, key := range xdgKeys {
			cmd.Env = append(cmd.Env, key+"="+xdgDir(base, key))
		}
		cmd.Stdin = strings.NewReader(payload)
		var stderr syncBuffer
		cmd.Stderr = &stderr
		if err := cmd.Start(); err != nil {
			t.Fatalf("start the hook: %v", err)
		}
		done := make(chan struct{})
		go func() { _ = cmd.Wait(); close(done) }()
		t.Cleanup(func() { _ = cmd.Process.Kill(); <-done })
		deadline := time.Now().Add(bootTimeout)
		for heldApprovals(t, base, session) < want {
			select {
			case <-done:
				if attempt == 3 {
					t.Fatalf("the hook exited %d times without holding its prompt; last run said: %s", attempt, stderr.String())
				}
				t.Logf("the hook exited without holding its prompt, so it runs again: %s", stderr.String())
			default:
				if time.Now().After(deadline) {
					t.Fatalf("the daemon never held the hook's prompt; the hook said: %s", stderr.String())
				}
				time.Sleep(50 * time.Millisecond)
				continue
			}
			break
		}
		if heldApprovals(t, base, session) >= want {
			return
		}
	}
}

func heldApprovals(t *testing.T, base, session string) int {
	t.Helper()
	out, err := tuiosCLI(t, base, "list-attention", "--session", session, "--json")
	if err != nil {
		t.Fatalf("list-attention: %v\n%s", err, out)
	}
	var res struct {
		Items []struct {
			RequestID string `json:"request_id"`
		} `json:"items"`
	}
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("list-attention json: %v\n%s", err, out)
	}
	n := 0
	for _, it := range res.Items {
		if it.RequestID != "" {
			n++
		}
	}
	return n
}

func groundClient(t *testing.T, cols, rows int, theme, background string, out *syncBuffer) *tuitest.Terminal {
	t.Helper()
	base := t.TempDir()
	killDaemon(t, base)
	useShippedLooks(base)
	dir := filepath.Join(xdgDir(base, "XDG_CONFIG_HOME"), "tuios")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	cfg := "[appearance]\ntheme = \"" + theme + "\"\nmodal_dim = 0\n"
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
	enableTiling(t, term)
	return term
}

func groundSteps() []groundStep {
	noKeysRow := func(t *testing.T, s tuitest.Screen) {
		t.Helper()
		if strings.Contains(s.Text(), "[1/3]") {
			t.Errorf("an approval's row repeats the keys its footer names:\n%s", s.Text())
		}
	}
	return []groundStep{
		{name: "inbox-approval", fresh: true, titled: true, keys: []any{tuitest.Ctrl('b'), "i"},
			want: []string{"3 needs you", "Allow Bash", "risky", "a allow"}, check: noKeysRow},
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
		{name: "sessions", fresh: true, titled: true, keys: []any{tuitest.Ctrl('b'), "S"}, want: []string{"Go to", "e2e-agent"},
			close: []any{tuitest.Esc}},
		{name: "workspaces", fresh: true, titled: true, keys: []any{tuitest.Ctrl('b'), "W"}, want: []string{"Go to", "workspace"},
			close: []any{tuitest.Esc}},
		{name: "mail", fresh: true, titled: true, keys: []any{tuitest.Ctrl('b'), "M", "m"}, want: []string{"No mail."},
			close: []any{tuitest.Esc, tuitest.Esc}},
		{name: "which-key", fresh: true, titled: true, keys: []any{tuitest.Ctrl('b')}, want: []string{"prefix", "Focus pane"},
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

func waitGone(t *testing.T, term *tuitest.Terminal, what, text string) {
	t.Helper()
	if err := term.WaitFor(func(s tuitest.Screen) bool { return !strings.Contains(s.Text(), text) }, uiTimeout); err != nil {
		t.Fatalf("%s did not close: %v\n%s", what, err, term.Snapshot())
	}
}

func descriptionsAligned(t *testing.T, s tuitest.Screen) {
	t.Helper()
	col := func(text string) int {
		y := rowWith(s, text)
		if y < 0 {
			return -1
		}
		return len([]rune(s.Line(y)[:strings.Index(s.Line(y), text)]))
	}
	if a, b := col("Create"), col("Focus pane"); a < 0 || a != b {
		t.Errorf("which-key descriptions start at columns %d and %d\n%s", a, b, s.Text())
	}
}
