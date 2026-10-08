package tuie2e

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// The GUI bridge's wave 3b (plan 8.4): the palette and launcher events, the
// in-place session switch and the restore of a saved session, the options
// and the theme kept in the config file, the typing group, stacks, the
// scrolling strip, picture in picture, saved layouts, the scratch box and the
// window kind. Each test runs a real bridge against a real daemon, as
// tuios-gpui does; the stack and the window kind are also checked on a real
// terminal client's screen.

// wireWave3bEvent is an event of the kinds this file reads.
type wireWave3bEvent struct {
	Type    string `json:"type"`
	Palette []struct {
		Name     string `json:"name"`
		Category string `json:"category"`
		Shortcut string `json:"shortcut"`
		Action   string `json:"action"`
	} `json:"palette"`
	Options *struct {
		Options []struct {
			Path  string `json:"path"`
			Type  string `json:"type"`
			Value string `json:"value"`
		} `json:"options"`
		Path string `json:"path"`
	} `json:"options"`
	Launcher []struct {
		Name string `json:"name"`
		Path string `json:"path"`
	} `json:"launcher"`
	Layouts []struct {
		Name  string `json:"name"`
		Panes int    `json:"panes"`
	} `json:"layouts"`
	Fleet *struct {
		Saved []struct {
			Name    string `json:"name"`
			Windows int    `json:"windows"`
		} `json:"saved"`
	} `json:"fleet"`
}

// waitWave3b waits for a kept event that satisfies ok, newest first.
func (b *guiBridge) waitWave3b(ok func(*wireWave3bEvent) bool, what string) *wireWave3bEvent {
	b.t.Helper()
	deadline := time.Now().Add(shellTimeout)
	b.mu.Lock()
	defer b.mu.Unlock()
	for {
		for i := len(b.events) - 1; i >= 0; i-- {
			var ev wireWave3bEvent
			if json.Unmarshal(b.events[i], &ev) == nil && ok(&ev) {
				return &ev
			}
		}
		if b.closed {
			b.t.Fatalf("bridge closed while waiting for %s", what)
		}
		if time.Now().After(deadline) {
			b.t.Fatalf("bridge never sent %s", what)
		}
		b.waitLocked(100 * time.Millisecond)
	}
}

// eventCount is how many events the reader kept, to wait for a new one.
func (b *guiBridge) eventCount() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.events)
}

// waitWave3bAfter waits for an event that satisfies ok among those that came
// after the first n.
func (b *guiBridge) waitWave3bAfter(n int, ok func(*wireWave3bEvent) bool, what string) *wireWave3bEvent {
	b.t.Helper()
	deadline := time.Now().Add(shellTimeout)
	b.mu.Lock()
	defer b.mu.Unlock()
	for {
		for i := len(b.events) - 1; i >= n && i >= 0; i-- {
			var ev wireWave3bEvent
			if json.Unmarshal(b.events[i], &ev) == nil && ok(&ev) {
				return &ev
			}
		}
		if b.closed {
			b.t.Fatalf("bridge closed while waiting for %s", what)
		}
		if time.Now().After(deadline) {
			b.t.Fatalf("bridge never sent %s", what)
		}
		b.waitLocked(100 * time.Millisecond)
	}
}

// snapCount is how many SNAP frames came for a PTY.
func (b *guiBridge) snapCount(pty string) int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.snaps[pty]
}

// TestGUIBridgePaletteRows: the palette event carries tuios's own palette, a
// row that is an action names it, and the palette command runs a row with no
// action in the model and refuses the others.
func TestGUIBridgePaletteRows(t *testing.T) {
	base := t.TempDir()
	b := startBridge(t, base, "gb", 160, 50)
	b.tiledPanes(3)
	ev := b.waitWave3b(func(e *wireWave3bEvent) bool { return e.Type == "palette" && len(e.Palette) > 10 }, "the palette event")
	rows := map[string]string{}
	for _, r := range ev.Palette {
		rows[r.Name] = r.Action
	}
	if a, ok := rows["New window"]; !ok || a != "new_window" {
		t.Errorf(`row "New window" has action %q (present %v), want new_window`, a, ok)
	}
	if a, ok := rows["Layout: master-stack"]; !ok || a != "" {
		t.Errorf(`row "Layout: master-stack" has action %q (present %v), want a row with no action`, a, ok)
	}
	if _, ok := rows["Start the screen saver"]; ok {
		t.Errorf("the palette offers a row only a screen can show")
	}
	b.mustCall(map[string]any{"cmd": "palette", "name": "Layout: master-stack"})
	b.waitState(func(s *wireState) bool { return s.Layout == "master-stack" }, "the master-stack layout after its palette row")
	if r := b.call(map[string]any{"cmd": "palette", "name": "New window"}); r.OK || !strings.Contains(r.Error, "new_window") {
		t.Errorf("palette ran an action row: ok=%v error=%q", r.OK, r.Error)
	}
	if r := b.call(map[string]any{"cmd": "palette", "name": "No such row"}); r.OK {
		t.Errorf("palette ran a row that does not exist")
	}
}

// TestGUIBridgeSwitchSessionInPlace: switch-session moves the bridge to
// another session with no new process, and every pane of that session
// arrives as a snapshot. A missing session is refused unless create is set.
func TestGUIBridgeSwitchSessionInPlace(t *testing.T) {
	base := t.TempDir()
	b := startBridge(t, base, "first", 120, 40)
	b.tiledPanes(1)
	if out, err := tuiosCLI(t, base, "new", "--detach", "other"); err != nil {
		t.Fatalf("make the second session: %v\n%s", err, out)
	}
	if out, err := tuiosCLI(t, base, "new-window", "-s", "other", "second", "--no-focus"); err != nil {
		t.Fatalf("add a pane to the second session: %v\n%s", err, out)
	}
	b.mustCall(map[string]any{"cmd": "switch-session", "name": "other"})
	st := b.waitState(func(s *wireState) bool { return s.Session == "other" && len(s.Windows) == 2 }, "session other with its two panes")
	for _, w := range st.Windows {
		pty := w.PTY
		deadline := time.Now().Add(uiTimeout)
		for b.snapCount(pty) == 0 && time.Now().Before(deadline) {
			time.Sleep(50 * time.Millisecond)
		}
		if b.snapCount(pty) == 0 {
			t.Errorf("no snapshot of pane %s of the new session", w.ID)
		}
	}
	if r := b.call(map[string]any{"cmd": "switch-session", "name": "missing"}); r.OK {
		t.Errorf("switch-session to a missing session without create succeeded")
	}
	b.mustCall(map[string]any{"cmd": "switch-session", "name": "made", "create": true})
	b.waitState(func(s *wireState) bool { return s.Session == "made" }, "session made after create")
}

// TestGUIBridgeRestoreSavedSession: the fleet event lists a session saved on
// disk that the daemon does not hold, and switch-session with restore brings
// it back with its panes and shows it.
func TestGUIBridgeRestoreSavedSession(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)
	if out, err := tuiosCLI(t, base, "new", "--detach", "keep"); err != nil {
		t.Fatalf("make the session to save: %v\n%s", err, out)
	}
	if out, err := tuiosCLI(t, base, "new-window", "-s", "keep", "two", "--no-focus"); err != nil {
		t.Fatalf("add a pane: %v\n%s", err, out)
	}
	// A stop saves every session; a daemon started with --no-restore holds
	// none of them until one is restored.
	if out, err := tuiosCLI(t, base, "kill-server"); err != nil {
		t.Fatalf("kill-server: %v\n%s", err, out)
	}
	daemon := exec.Command(tuiosBin, "daemon", "--no-restore")
	daemon.Dir = workDirIn(t, base)
	daemon.Env = append(os.Environ(), "SHELL=/bin/sh")
	for _, key := range xdgKeys {
		daemon.Env = append(daemon.Env, key+"="+xdgDir(base, key))
	}
	if err := daemon.Start(); err != nil {
		t.Fatalf("start the daemon: %v", err)
	}
	t.Cleanup(func() {
		// Stopped by its own PID, never by name.
		_ = daemon.Process.Kill()
		_ = daemon.Wait()
	})
	deadline := time.Now().Add(shellTimeout)
	for {
		out, err := tuiosCLI(t, base, "ls")
		if err == nil && strings.Contains(out, "No sessions") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the --no-restore daemon never answered with no sessions: %v\n%s", err, out)
		}
		time.Sleep(100 * time.Millisecond)
	}
	b := startBridge(t, base, "lobby", 120, 40)
	b.waitWave3b(func(e *wireWave3bEvent) bool {
		if e.Type != "fleet" || e.Fleet == nil {
			return false
		}
		for _, s := range e.Fleet.Saved {
			if s.Name == "keep" && s.Windows == 2 {
				return true
			}
		}
		return false
	}, "the saved session keep in the fleet event")
	b.mustCall(map[string]any{"cmd": "switch-session", "name": "keep", "restore": true})
	b.waitState(func(s *wireState) bool { return s.Session == "keep" && len(s.Windows) == 2 }, "session keep restored with its two panes")
}

// TestGUIBridgeOptionsAndTheme: the options event carries each option with
// its value, and option and theme with persist write the config file.
func TestGUIBridgeOptionsAndTheme(t *testing.T) {
	base := t.TempDir()
	b := startBridge(t, base, "gb", 120, 40)
	b.mustCall(map[string]any{"cmd": "option", "key": "appearance.gap", "value": "2"})
	b.send(map[string]any{"cmd": "options"})
	ev := b.waitWave3b(func(e *wireWave3bEvent) bool { return e.Type == "options" && e.Options != nil }, "the options event")
	found := false
	for _, o := range ev.Options.Options {
		if o.Path == "appearance.gap" {
			found = true
			if o.Value != "2" {
				t.Errorf("appearance.gap = %q in the options event, want 2", o.Value)
			}
		}
	}
	if !found {
		t.Errorf("the options event has no appearance.gap")
	}
	b.mustCall(map[string]any{"cmd": "theme", "theme": "dracula", "persist": true})
	path := filepath.Join(xdgDir(base, "XDG_CONFIG_HOME"), "tuios", "config.toml")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read the config: %v", err)
	}
	if !strings.Contains(string(data), `theme = "dracula"`) {
		t.Errorf("the config does not keep the theme:\n%s", data)
	}
	if !strings.Contains(string(data), "gap = 2") {
		t.Errorf("the config does not keep the gap:\n%s", data)
	}
	if r := b.call(map[string]any{"cmd": "option", "key": "appearance.no_such_option", "value": "1"}); r.OK {
		t.Errorf("option accepted a path that does not exist")
	}
}

// TestGUIBridgeLauncher: the launcher event lists the programs on $PATH, and
// launch starts one in a new pane.
func TestGUIBridgeLauncher(t *testing.T) {
	base := t.TempDir()
	b := startBridge(t, base, "gb", 120, 40)
	b.tiledPanes(1)
	b.send(map[string]any{"cmd": "launcher"})
	ev := b.waitWave3b(func(e *wireWave3bEvent) bool { return e.Type == "launcher" && len(e.Launcher) > 0 }, "the launcher event")
	path := ""
	for _, p := range ev.Launcher {
		if p.Name == "cat" {
			path = p.Path
		}
	}
	if path == "" {
		t.Fatalf("the launcher lists no cat among %d programs", len(ev.Launcher))
	}
	b.mustCall(map[string]any{"cmd": "launch", "path": path})
	b.waitState(func(s *wireState) bool { return len(s.Windows) == 2 }, "a second pane for the launched program")
	if r := b.call(map[string]any{"cmd": "launch", "path": "/no/such/program"}); r.OK {
		t.Errorf("launch started a program the launcher does not list")
	}
}

// TestGUIBridgeGroup: group puts a pane in the typing group and takes it out,
// and the state lists the group.
func TestGUIBridgeGroup(t *testing.T) {
	base := t.TempDir()
	b := startBridge(t, base, "gb", 160, 50)
	ids := b.tiledPanes(3)
	b.mustCall(map[string]any{"cmd": "group", "id": ids[0], "on": true})
	b.mustCall(map[string]any{"cmd": "group", "id": ids[2], "on": true})
	b.waitState(func(s *wireState) bool {
		return len(s.Multifocus) == 2 && strings.Contains(strings.Join(s.Multifocus, " "), ids[0]) && strings.Contains(strings.Join(s.Multifocus, " "), ids[2])
	}, "panes A and C in the group")
	b.mustCall(map[string]any{"cmd": "group", "id": ids[0], "on": false})
	b.waitState(func(s *wireState) bool { return len(s.Multifocus) == 1 && s.Multifocus[0] == ids[2] }, "pane C alone in the group")
	if r := b.call(map[string]any{"cmd": "group", "id": "no-such-pane", "on": true}); r.OK {
		t.Errorf("group took a pane that does not exist")
	}
}

// TestGUIBridgeStack: stack puts a pane into another's tile as a title row:
// it is minimized with the stack's key, and the open pane gives a row for it.
// A terminal client on the session shows the title row. stack-open trades
// the open pane, and unstack gives the pane a tile of its own again.
func TestGUIBridgeStack(t *testing.T) {
	base := t.TempDir()
	b := startBridge(t, base, "gb", 160, 50)
	ids := b.tiledPanes(3)
	names := []string{"alpha", "bravo", "charlie"}
	for i, id := range ids {
		if out, err := tuiosCLI(t, base, "set-window", "-s", "gb", "-w", id, "--name", names[i]); err != nil {
			t.Fatalf("name pane %d: %v\n%s", i, err, out)
		}
	}
	before := b.waitState(func(s *wireState) bool { return s.window(ids[1]) != nil && s.window(ids[1]).H > 0 }, "the tree of three")
	bh := before.window(ids[1]).H
	b.mustCall(map[string]any{"cmd": "layout", "op": "stack", "a": ids[1], "b": ids[2]})
	st := b.waitState(func(s *wireState) bool {
		c, a := s.window(ids[2]), s.window(ids[1])
		return c != nil && a != nil && c.Minimized && c.Stack != "" && c.Stack == a.Stack && leaf(s.tree(), ids[2]) == nil
	}, "charlie a title row in bravo's stack")
	// The open pane gives a row for the title row below it. Its tile is the
	// leaf's, so its box is the leaf's less that row.
	n := leaf(st.tree(), ids[1])
	if n == nil {
		t.Fatalf("bravo has no leaf")
	}
	a := st.window(ids[1])
	if a.Y != n.Rect.Y || a.H != n.Rect.H-1 {
		t.Errorf("bravo's box is y=%d h=%d in a leaf of y=%d h=%d; want the leaf less one row", a.Y, a.H, n.Rect.Y, n.Rect.H)
	}
	_ = bh
	// The terminal client keeps a row for its dock, so its rows are its own;
	// the title row is in bravo's columns, under bravo's own last row.
	term := attachIn(t, base, "gb", startOpts{cols: 160, rows: 50})
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		col, row, ok := tuitest.Find(s, "▸ charlie")
		_, brow, bok := tuitest.Find(s, "bravo")
		return ok && col >= a.X && col < a.X+a.W && (!bok || row > brow)
	}, shellTimeout); err != nil {
		t.Fatalf("the terminal client does not show charlie's title row in bravo's columns: %v\n%s", err, term.Snapshot())
	}
	b.mustCall(map[string]any{"cmd": "layout", "op": "stack-open", "id": ids[2]})
	b.waitState(func(s *wireState) bool {
		c, a := s.window(ids[2]), s.window(ids[1])
		return c != nil && a != nil && !c.Minimized && a.Minimized && leaf(s.tree(), ids[2]) != nil && s.Focused == ids[2]
	}, "charlie open in the stack's tile, bravo a title row")
	b.mustCall(map[string]any{"cmd": "layout", "op": "unstack", "id": ids[1]})
	b.waitState(func(s *wireState) bool {
		c, a := s.window(ids[2]), s.window(ids[1])
		return c != nil && a != nil && !a.Minimized && a.Stack == "" && c.Stack == "" && leaf(s.tree(), ids[1]) != nil && leaf(s.tree(), ids[2]) != nil
	}, "bravo and charlie each in a tile of its own")
}

// TestGUIBridgeScrollStrip: in the scrolling layout the state carries the
// strip as it settles: the viewport, and each pane's place on the strip from
// its left end, not from the screen.
func TestGUIBridgeScrollStrip(t *testing.T) {
	base := t.TempDir()
	b := startBridge(t, base, "gb", 120, 40)
	b.tiledPanes(4)
	b.mustCall(map[string]any{"cmd": "palette", "name": "Layout: scrolling (niri-style)"})
	st := b.waitState(func(s *wireState) bool { return s.Layout == "scrolling" && s.Strip != nil && len(s.Strip.Panes) == 4 }, "the strip of four panes")
	if st.Strip.Viewport <= 0 {
		t.Errorf("the strip's viewport is %d with the newest of four columns focused; want it scrolled", st.Strip.Viewport)
	}
	if st.Strip.Panes[0].X != 0 {
		t.Errorf("the first column starts at %d on the strip, want 0", st.Strip.Panes[0].X)
	}
	for i := 1; i < len(st.Strip.Panes); i++ {
		p, q := st.Strip.Panes[i-1], st.Strip.Panes[i]
		if q.X <= p.X {
			t.Errorf("column %d at %d is not right of column %d at %d", i, q.X, i-1, p.X)
		}
	}
	if st.Strip.Width < st.Strip.Panes[3].X+st.Strip.Panes[3].W {
		t.Errorf("the strip is %d wide, narrower than its last column's end", st.Strip.Width)
	}
}

// TestGUIBridgePiPAndScratch: the pinned pane and the scratch box are in the
// state while they show.
func TestGUIBridgePiPAndScratch(t *testing.T) {
	base := t.TempDir()
	b := startBridge(t, base, "gb", 120, 40)
	ids := b.tiledPanes(2)
	b.send(map[string]any{"cmd": "focus", "window": ids[0]})
	b.waitState(func(s *wireState) bool { return s.Focused == ids[0] }, "pane A focused")
	b.mustCall(map[string]any{"cmd": "palette", "name": "Picture in picture: pin or unpin the focused pane"})
	b.waitState(func(s *wireState) bool { return s.PiP != nil && s.PiP.Window == ids[0] && s.PiP.Corner != "" }, "pane A pinned")
	b.mustCall(map[string]any{"cmd": "action", "name": "toggle_scratch"})
	st := b.waitState(func(s *wireState) bool { return s.ScratchBox != nil && s.ScratchBox.W > 0 }, "the scratch box")
	if st.ScratchOver != 1 {
		t.Errorf("the scratch box shows over workspace %d, want 1", st.ScratchOver)
	}
	b.mustCall(map[string]any{"cmd": "action", "name": "toggle_scratch"})
	b.waitState(func(s *wireState) bool { return s.ScratchBox == nil }, "the scratch box hidden again")
}

// TestGUIBridgeSavedLayouts: layout-save keeps the panes on screen as a
// layout the layouts event lists, and layout-delete takes it away.
func TestGUIBridgeSavedLayouts(t *testing.T) {
	base := t.TempDir()
	b := startBridge(t, base, "gb", 120, 40)
	b.tiledPanes(2)
	b.mustCall(map[string]any{"cmd": "layout-save", "name": "pair"})
	n := b.eventCount()
	b.send(map[string]any{"cmd": "layouts"})
	b.waitWave3bAfter(n, func(e *wireWave3bEvent) bool {
		if e.Type != "layouts" {
			return false
		}
		for _, l := range e.Layouts {
			if l.Name == "pair" && l.Panes == 2 {
				return true
			}
		}
		return false
	}, "the layout pair with two panes")
	b.mustCall(map[string]any{"cmd": "layout-load", "name": "pair"})
	b.mustCall(map[string]any{"cmd": "layout-delete", "name": "pair"})
	n = b.eventCount()
	b.send(map[string]any{"cmd": "layouts"})
	ev := b.waitWave3bAfter(n, func(e *wireWave3bEvent) bool { return e.Type == "layouts" }, "the layouts after the delete")
	for _, l := range ev.Layouts {
		if l.Name == "pair" {
			t.Errorf("the layout pair is still listed after layout-delete")
		}
	}
	if r := b.call(map[string]any{"cmd": "layout-load", "name": "missing"}); r.OK {
		t.Errorf("layout-load loaded a layout that does not exist")
	}
}

// TestGUIBridgeWindowKind: a window made with a kind and a uri keeps them in
// the state, a terminal client shows its placeholder line, and both survive
// a restart of the daemon.
func TestGUIBridgeWindowKind(t *testing.T) {
	base := t.TempDir()
	b := startBridge(t, base, "gb", 120, 40)
	b.tiledPanes(1)
	out, err := tuiosCLI(t, base, "new-window", "-s", "gb", "review", "--kind", "view", "--uri", "tuios://review/abc", "--print-id")
	if err != nil {
		t.Fatalf("make the view window: %v\n%s", err, out)
	}
	id := strings.TrimSpace(out)
	b.waitState(func(s *wireState) bool {
		w := s.window(id)
		return w != nil && w.Kind == "view" && w.URI == "tuios://review/abc"
	}, "the view window with its kind and uri")
	if r, err := tuiosCLI(t, base, "new-window", "-s", "gb", "bad", "--kind", "view"); err == nil {
		t.Errorf("a view window with no uri was made: %s", r)
	}
	term := attachIn(t, base, "gb", startOpts{cols: 120, rows: 40})
	if err := term.WaitForText("view pane  tuios://review/abc", shellTimeout); err != nil {
		t.Fatalf("the terminal client does not show the placeholder: %v\n%s", err, term.Snapshot())
	}
	// A restart restores the window as what it was.
	if out, err := tuiosCLI(t, base, "kill-server"); err != nil {
		t.Fatalf("kill-server: %v\n%s", err, out)
	}
	b2 := startBridge(t, base, "gb", 120, 40)
	b2.waitState(func(s *wireState) bool {
		for _, w := range s.Windows {
			if w.Kind == "view" && w.URI == "tuios://review/abc" {
				return true
			}
		}
		return false
	}, "the view window after a restart")
	_ = fmt.Sprint
}
