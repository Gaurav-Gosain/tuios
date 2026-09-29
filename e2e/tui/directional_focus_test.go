package tuie2e

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// Issue #231: in window mode, h and l moved focus between tiled panes and j and
// k did nothing, so a pane above or below the focused one could not be reached
// from the home row.
//
// These drive a real client over a daemon session. They lay out four panes,
// then press every direction key from every pane that has a neighbour that way
// and read the focused pane back from list-windows. Each press must land on a
// pane that touches the old one on the side pressed. The walk ends when every
// such (pane, direction) pair has been pressed, so every pane is reached too.
//
// NEGATIVE CONTROL: against a binary with no window-mode binding for j and k
// (v0.8.0 and main before the fix) the BSP case fails on the first j or k,
// saying focus never left the pane.

type focusKey struct {
	name string // direction, for messages
	keys []any  // what to press
}

var windowModeFocusKeys = []focusKey{
	{"left", []any{"h"}}, {"down", []any{"j"}}, {"up", []any{"k"}}, {"right", []any{"l"}},
}

type focusedRect struct {
	winRect
	Focused bool
}

func focusedLayout(t *testing.T, base, session string) ([]focusedRect, string) {
	t.Helper()
	out, err := tuiosCLI(t, base, "list-windows", "--json", "--session", session)
	if err != nil {
		t.Fatalf("list-windows: %v\n%s", err, out)
	}
	var payload struct {
		Windows []struct {
			ID      string `json:"window_id"`
			X       int    `json:"x"`
			Y       int    `json:"y"`
			Width   int    `json:"width"`
			Height  int    `json:"height"`
			Focused bool   `json:"focused"`
		} `json:"windows"`
	}
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatalf("decode list-windows: %v\n%s", err, out)
	}
	rects := make([]focusedRect, 0, len(payload.Windows))
	focused := ""
	for _, w := range payload.Windows {
		rects = append(rects, focusedRect{winRect{w.ID, w.X, w.Y, w.Width, w.Height}, w.Focused})
		if w.Focused {
			focused = w.ID
		}
	}
	return rects, focused
}

// touchingNeighbours is the set of panes that share an edge with from on the
// side named by dir. A tiled layout is a partition of the screen, so these are
// exactly the panes a directional focus key may land on. Two cells of slack
// cover a shared border and a gap.
func touchingNeighbours(from winRect, all []focusedRect, dir string) map[string]bool {
	overlap := func(a0, a1, b0, b1 int) int { return min(a1, b1) - max(a0, b0) }
	near := func(v, want int) bool { return v >= want-2 && v <= want+2 }
	out := map[string]bool{}
	for _, r := range all {
		w := r.winRect
		if w.ID == from.ID {
			continue
		}
		switch dir {
		case "left":
			if near(w.X+w.Width, from.X) && overlap(w.Y, w.Y+w.Height, from.Y, from.Y+from.Height) >= 2 {
				out[w.ID] = true
			}
		case "right":
			if near(w.X, from.X+from.Width) && overlap(w.Y, w.Y+w.Height, from.Y, from.Y+from.Height) >= 2 {
				out[w.ID] = true
			}
		case "up":
			if near(w.Y+w.Height, from.Y) && overlap(w.X, w.X+w.Width, from.X, from.X+from.Width) >= 2 {
				out[w.ID] = true
			}
		case "down":
			if near(w.Y, from.Y+from.Height) && overlap(w.X, w.X+w.Width, from.X, from.X+from.Width) >= 2 {
				out[w.ID] = true
			}
		}
	}
	return out
}

// waitFocusChange polls until the daemon names a focused pane other than prev.
func waitFocusChange(t *testing.T, base, session, prev string) string {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, f := focusedLayout(t, base, session); f != "" && f != prev {
			return f
		}
		time.Sleep(100 * time.Millisecond)
	}
	return prev
}

// walkEveryDirection presses each direction key from each pane that has a
// neighbour that way, and checks where focus lands.
func walkEveryDirection(t *testing.T, term *tuitest.Terminal, base, session string, keys []focusKey, panes int) {
	t.Helper()
	rects := waitForSettledGeometryIn(t, base, session, panes)
	layout, cur := focusedLayout(t, base, session)
	byID := map[string]winRect{}
	for _, r := range layout {
		byID[r.ID] = r.winRect
	}
	for _, r := range rects {
		t.Logf("pane %s: (%d,%d) %dx%d", r.ID[:8], r.X, r.Y, r.Width, r.Height)
	}
	type edge struct{ from, dir string }
	todo := map[edge]bool{}
	for _, r := range layout {
		for _, k := range keys {
			if len(touchingNeighbours(r.winRect, layout, k.name)) > 0 {
				todo[edge{r.ID, k.name}] = true
			}
		}
	}
	seen := map[string]bool{cur: true}
	// moves records where each tested press went, which is the map the walk
	// uses to get back to a pane that still has an untested direction.
	moves := map[string]map[string]string{}
	pressed := map[string]int{}

	for step := 0; len(todo) > 0; step++ {
		if step > 80 {
			t.Fatalf("the walk did not finish; untested: %v", todo)
		}
		var pick *focusKey
		for i, k := range keys {
			if todo[edge{cur, k.name}] {
				pick = &keys[i]
				break
			}
		}
		if pick == nil {
			// Head for a pane with work left along a known press.
			pick = routeTo(cur, moves, keys, func(id string) bool {
				for _, k := range keys {
					if todo[edge{id, k.name}] {
						return true
					}
				}
				return false
			})
			if pick == nil {
				t.Fatalf("no known route from %s to a pane with an untested direction; untested: %v", cur[:8], todo)
			}
		}
		want := touchingNeighbours(byID[cur], layout, pick.name)
		send(t, term, pick.keys...)
		got := waitFocusChange(t, base, session, cur)
		if !want[got] {
			t.Fatalf("pressed %s (%v) on pane %s at %+v: focus went to %s, want one of %v\n%s",
				pick.name, pick.keys, cur[:8], byID[cur], got[:8], keysOf(want), term.Snapshot())
		}
		delete(todo, edge{cur, pick.name})
		if moves[cur] == nil {
			moves[cur] = map[string]string{}
		}
		moves[cur][pick.name] = got
		pressed[pick.name]++
		cur = got
		seen[cur] = true
	}
	if len(seen) != panes {
		t.Fatalf("the keys reached %d of %d panes", len(seen), panes)
	}
	t.Logf("presses per direction: %v", pressed)
}

// routeTo is the first key of the shortest known path from cur to a pane
// that wants(), over presses already made.
func routeTo(cur string, moves map[string]map[string]string, keys []focusKey, wants func(string) bool) *focusKey {
	type hop struct {
		id    string
		first int
	}
	visited := map[string]bool{cur: true}
	queue := []hop{}
	for i, k := range keys {
		if to, ok := moves[cur][k.name]; ok && !visited[to] {
			visited[to] = true
			queue = append(queue, hop{to, i})
		}
	}
	for len(queue) > 0 {
		h := queue[0]
		queue = queue[1:]
		if wants(h.id) {
			return &keys[h.first]
		}
		for _, k := range keys {
			if to, ok := moves[h.id][k.name]; ok && !visited[to] {
				visited[to] = true
				queue = append(queue, hop{to, h.first})
			}
		}
	}
	return nil
}

func keysOf(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k[:8])
	}
	return out
}

// tiledPanes starts a daemon session with n tiled panes in window mode.
// layout is a palette search that picks a tiling scheme, or empty for BSP.
func tiledPanes(t *testing.T, session, layout string, n int) (*tuitest.Terminal, string) {
	t.Helper()
	term, base := start(t, startOpts{cols: 160, rows: 48, args: []string{"new", session}})
	killDaemon(t, base)
	waitBoot(t, term)
	for range n {
		newWindow(t, term)
	}
	waitWindowCount(t, term, n, "directional focus setup")
	enableTiling(t, term)
	if layout != "" {
		send(t, term, tuitest.Ctrl('p'))
		waitPaletteOpen(t, term, "to pick a layout")
		send(t, term, layout, tuitest.Enter)
		waitPaletteClosed(t, term, "after picking a layout")
		time.Sleep(time.Second)
	}
	return term, base
}

func TestDirectionalFocusBSP(t *testing.T) {
	term, base := tiledPanes(t, "dirbsp", "", 4)
	walkEveryDirection(t, term, base, "dirbsp", windowModeFocusKeys, 4)
}

// Three panes is the master on one side and two stacked on the other. Four or
// more is a grid, which the BSP spiral already covers in kind.
func TestDirectionalFocusMasterStack(t *testing.T) {
	term, base := tiledPanes(t, "dirmaster", "Layout: master-stack", 3)
	walkEveryDirection(t, term, base, "dirmaster", windowModeFocusKeys, 3)
}

func TestDirectionalFocusMasterStackGrid(t *testing.T) {
	term, base := tiledPanes(t, "dirgrid", "Layout: master-stack", 4)
	walkEveryDirection(t, term, base, "dirgrid", windowModeFocusKeys, 4)
}

// The prefix arrows reach the same action from any mode.
func TestDirectionalFocusPrefixArrowsBSP(t *testing.T) {
	term, base := tiledPanes(t, "dirprefix", "", 4)
	arrows := []focusKey{
		{"left", []any{tuitest.Ctrl('b'), tuitest.Left}},
		{"down", []any{tuitest.Ctrl('b'), tuitest.Down}},
		{"up", []any{tuitest.Ctrl('b'), tuitest.Up}},
		{"right", []any{tuitest.Ctrl('b'), tuitest.Right}},
	}
	walkEveryDirection(t, term, base, "dirprefix", arrows, 4)
}
