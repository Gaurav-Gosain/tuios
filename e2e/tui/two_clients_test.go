package tuie2e

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

type mcClient struct {
	term       *tuitest.Terminal
	cols, rows int
	attached   bool
}

type mcRun struct {
	t        *testing.T
	base     string
	name     string
	policy   string
	panes    int
	clients  map[string]*mcClient
	shares   []winRect
	border   map[string][2]int
	railCols int
	dir      string
}

func (r *mcRun) want() (w, h int) {
	first := true
	for _, c := range r.clients {
		if !c.attached {
			continue
		}
		if first {
			w, h, first = c.cols, c.rows, false
			continue
		}
		if r.policy == "largest" {
			w, h = max(w, c.cols), max(h, c.rows)
		} else {
			w, h = min(w, c.cols), min(h, c.rows)
		}
	}
	return w, h
}

func (r *mcRun) check(step string) {
	t := r.t
	t.Helper()
	w, h := r.want()
	waitWSSizeIn(t, r.base, r.name, w, h, step)
	rects := waitForShape(t, r.base, r.name, r.panes, step+": the daemon's panes", func(rects []winRect) error {
		x, _, bw, _ := paneBoxOf(rects)
		if x+bw != w {
			return fmt.Errorf("the panes end at column %d, want the session's %d", x+bw, w)
		}
		if r.railCols < 0 && x == 0 || r.railCols >= 0 && x != r.railCols {
			return fmt.Errorf("the panes start at column %d, want %d (-1 is the rail's edge)", x, r.railCols)
		}
		if r.shares != nil {
			return sameShares(r.shares, rects, 2)
		}
		return nil
	})
	deadline := time.Now().Add(uiTimeout)
	for _, rc := range rects {
		for {
			cols, rows := sbGridSize(t, r.base, r.name, rc.ID)
			b, known := r.border[rc.ID]
			if !known {
				r.border[rc.ID] = [2]int{rc.Width - cols, rc.Height - rows}
				break
			}
			if cols == rc.Width-b[0] && rows == rc.Height-b[1] {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("%s: pane %s is drawn %dx%d and its shell is %dx%d, want %dx%d",
					step, rc.ID, rc.Width, rc.Height, cols, rows, rc.Width-b[0], rc.Height-b[1])
			}
			time.Sleep(150 * time.Millisecond)
		}
	}
	for name, c := range r.clients {
		if !c.attached {
			continue
		}
		if c.cols >= w {
			waitSpanRight(t, c.term, w, step+": client "+name)
		}
		saveArtifact(t, c.term, r.dir, strings.ReplaceAll(step, " ", "-")+"-"+name)
	}
}

func waitWSSizeIn(t *testing.T, base, name string, w, h int, what string) {
	t.Helper()
	deadline := time.Now().Add(uiTimeout)
	var gw, gh int
	var policy string
	for time.Now().Before(deadline) {
		if gw, gh, policy = sessionPolicySize(t, base, name); gw == w && gh == h {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("%s: the session is %dx%d under %s, want %dx%d", what, gw, gh, policy, w, h)
}

func (r *mcRun) atOnce(step string) {
	r.t.Helper()
	w, h := r.want()
	start := time.Now()
	waitWSSizeIn(r.t, r.base, r.name, w, h, step)
	if d := time.Since(start); d > time.Second {
		r.t.Fatalf("%s: the session took %v to become %dx%d", step, d, w, h)
	}
}

func (r *mcRun) resize(name string, cols, rows int) {
	r.t.Helper()
	c := r.clients[name]
	if err := c.term.Resize(cols, rows); err != nil {
		r.t.Fatalf("resize %s: %v", name, err)
	}
	c.cols, c.rows = cols, rows
}

// TestTwoClientsKeepOneLayout is the general pass over two clients. It is a
// guard: it passes on main (524e2f46) as well. It records what holds today,
// so a change that breaks one of the four agreements, or the client count
// the dock shows, fails here. The positive half of its count check is in the
// test itself: a third client joins and leaves, and the dock counts 3 and 2.
func TestTwoClientsKeepOneLayout(t *testing.T) {
	for _, policy := range []string{"smallest", "largest"} {
		for _, lay := range []string{"master-stack", "bsp"} {
			t.Run(policy+"/"+lay, func(t *testing.T) {
				r := &mcRun{t: t, base: t.TempDir(), name: "mc", policy: policy, panes: 3,
					clients: map[string]*mcClient{}, border: map[string][2]int{}, dir: artifactDir(t)}
				writeConfig(t, r.base, fmt.Sprintf("[daemon]\nwindow_size = %q\n"+
					"[startup]\nopen_default_window = true\ntiled = true\nlayout = %q\n"+
					"[appearance.sidebar]\nenabled = false\nposition = \"left\"\n", policy, lay))
				killDaemon(t, r.base)
				if out, err := tuiosCLI(t, r.base, "new", "-d", r.name); err != nil {
					t.Fatalf("create the session: %v\n%s", err, out)
				}
				out := &syncBuffer{}
				a := attachIn(t, r.base, r.name, startOpts{cols: 185, rows: 43, out: out})
				r.clients["a"] = &mcClient{term: a, cols: 185, rows: 43, attached: true}
				waitForSettledGeometryIn(t, r.base, r.name, 1)
				for i := 2; i <= r.panes; i++ {
					if out, err := tuiosCLI(t, r.base, "run-command", "-s", r.name, "NewWindow"); err != nil {
						t.Fatalf("open window %d: %v\n%s", i, err, out)
					}
					waitForSettledGeometryIn(t, r.base, r.name, i)
				}
				equal := waitForSettledGeometryIn(t, r.base, r.name, r.panes)
				sendKeys(t, a, ">", ">", ">", "}", "}", "}")
				r.shares = waitForShape(t, r.base, r.name, r.panes, "the keys", func(rects []winRect) error {
					if sameShares(equal, rects, 1) == nil {
						return fmt.Errorf("the keys resized nothing")
					}
					return nil
				})
				r.check("one client")

				b := attachIn(t, r.base, r.name, startOpts{cols: 160, rows: 40})
				r.clients["b"] = &mcClient{term: b, cols: 160, rows: 40, attached: true}
				r.check("b attached")

				r.resize("b", 120, 35)
				r.check("b shrank")
				r.resize("b", 200, 50)
				r.check("b grew past a")

				toggleRail(t, b)
				if err := a.WaitFor(railShown, uiTimeout); err != nil {
					t.Fatalf("the rail never showed on a\n%s", a.Snapshot())
				}
				r.railCols = -1
				r.check("rail shown")
				toggleRail(t, b)
				if err := a.WaitFor(func(s tuitest.Screen) bool { return !railShown(s) }, uiTimeout); err != nil {
					t.Fatalf("the rail never hid on a\n%s", a.Snapshot())
				}
				r.railCols = 0
				r.check("rail hidden")

				for i := range 12 {
					r.resize("b", 140+i*5, 36+i%5)
					time.Sleep(25 * time.Millisecond)
				}
				r.check("b dragged")
				go func() { _ = a.Resize(170, 42) }()
				r.resize("b", 190, 45)
				r.clients["a"].cols, r.clients["a"].rows = 170, 42
				r.check("both resized at once")
				r.resize("a", 185, 43)
				r.resize("b", 200, 50)
				r.check("b at 200x50")

				sendKeys(t, b, tuitest.Ctrl('b'), "d")
				r.clients["b"].attached = false
				r.atOnce("b detached")
				r.check("b detached")

				b2 := attachIn(t, r.base, r.name, startOpts{cols: 160, rows: 40})
				r.clients["b"] = &mcClient{term: b2, cols: 160, rows: 40, attached: true}
				r.check("b attached again at 160x40")
				if policy == "smallest" {
					c := attachSmall(t, r.base, r.name, startOpts{cols: 200, rows: 50})
					waitStream(t, out, "Client joined (3 connected)", "a, when a third client attached")
					if err := c.Close(); err != nil {
						t.Fatalf("close c: %v", err)
					}
					waitStream(t, out, "Client left (2 connected)", "a, when the third client left")
				}
				if err := b2.Close(); err != nil {
					t.Fatalf("close b: %v", err)
				}
				r.clients["b"].attached = false
				r.atOnce("b exited")
				r.check("b exited")
			})
		}
	}
}

func waitStream(t *testing.T, out *syncBuffer, text, what string) {
	t.Helper()
	deadline := time.Now().Add(uiTimeout)
	for !strings.Contains(out.String(), text) {
		if time.Now().After(deadline) {
			t.Fatalf("%s: %q never reached the screen", what, text)
		}
		time.Sleep(100 * time.Millisecond)
	}
}
