package tuie2e

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// The GUI bridge (`tuios gui-bridge`) end to end: a real bridge process
// against a real daemon, spoken to over its own frame protocol the way
// tuios-gpui speaks to it. The wire types below are written from the
// protocol document, not imported, so a change to the JSON breaks these
// tests even when the Go side still agrees with itself.
//
// Every layout test checks the edit three ways: the bridge's own state, a
// second client that attaches afterwards and reads the tree back from the
// daemon, and for swap and move a real terminal client, whose screen shows
// each pane's marker inside the rectangle the bridge says the pane has.

// wireState is the bridge's "state" event.
type wireState struct {
	Session     string               `json:"session"`
	Cols        int                  `json:"cols"`
	Rows        int                  `json:"rows"`
	Workspace   int                  `json:"workspace"`
	Focused     string               `json:"focused"`
	Tiling      bool                 `json:"tiling"`
	Layout      string               `json:"layout"`
	Zoomed      string               `json:"zoomed"`
	Windows     []wireWindow         `json:"windows"`
	Trees       map[string]*wireTree `json:"trees"`
	SessionSize wireSize             `json:"session_size"`
	ClientSize  wireSize             `json:"client_size"`
	Viewport    *struct{ X, Y int }  `json:"viewport"`
	AgentSeen   bool                 `json:"agent_seen"`
	// Wave 3b (gui_bridge_wave3b_test.go).
	Multifocus []string `json:"multifocus"`
	PiP        *struct {
		Window string `json:"window"`
		Corner string `json:"corner"`
	} `json:"pip"`
	ScratchBox  *wireRect `json:"scratch_box"`
	ScratchOver int       `json:"scratch_over"`
	Strip       *struct {
		Viewport int `json:"viewport"`
		Width    int `json:"width"`
		Panes    []struct {
			ID string `json:"id"`
			X  int    `json:"x"`
			Y  int    `json:"y"`
			W  int    `json:"w"`
			H  int    `json:"h"`
		} `json:"panes"`
	} `json:"strip"`
}

type wireWindow struct {
	ID         string `json:"id"`
	PTY        string `json:"pty"`
	Kind       string `json:"kind"`
	Workspace  int    `json:"workspace"`
	X          int    `json:"x"`
	Y          int    `json:"y"`
	W          int    `json:"w"`
	H          int    `json:"h"`
	Floating   bool   `json:"floating"`
	Popup      bool   `json:"popup"`
	Repo       string `json:"repo"`
	Branch     string `json:"branch"`
	Title      string `json:"title"`
	Minimized  bool   `json:"minimized"`
	Scratch    bool   `json:"scratch"`
	Stack      string `json:"stack"`
	StackIndex int    `json:"stack_index"`
	URI        string `json:"uri"`
}

type wireSize struct {
	Cols int `json:"cols"`
	Rows int `json:"rows"`
}

type wireRect struct {
	X int `json:"x"`
	Y int `json:"y"`
	W int `json:"w"`
	H int `json:"h"`
}

type wireTree struct {
	Bounds wireRect  `json:"bounds"`
	Gap    int       `json:"gap"`
	Scheme string    `json:"scheme"`
	Root   *wireNode `json:"root"`
}

type wireNode struct {
	ID     uint64    `json:"id"`
	Window string    `json:"window"`
	Axis   string    `json:"axis"`
	Ratio  float64   `json:"ratio"`
	Rect   wireRect  `json:"rect"`
	A      *wireNode `json:"a"`
	B      *wireNode `json:"b"`
}

type wireResult struct {
	Req   int64   `json:"req"`
	Cmd   string  `json:"cmd"`
	Op    string  `json:"op"`
	Name  string  `json:"name"`
	OK    bool    `json:"ok"`
	Error string  `json:"error"`
	Ratio float64 `json:"ratio"`
}

type wireKeybinds struct {
	Leader   string `json:"leader"`
	RepeatMS int    `json:"repeat_ms"`
	Scopes   []struct {
		ID    string `json:"id"`
		Chord string `json:"chord"`
	} `json:"scopes"`
	Bindings []struct {
		Scope  string `json:"scope"`
		Action string `json:"action"`
		Key    string `json:"key"`
		Press  string `json:"press"`
	} `json:"bindings"`
	Prefixes map[string]string `json:"prefixes"`
	Menus    map[string][]struct {
		Title string `json:"title"`
		Rows  []struct {
			Key    string `json:"key"`
			Action string `json:"action"`
		} `json:"rows"`
	} `json:"menus"`
	Rows []struct {
		Scope  string   `json:"scope"`
		Action string   `json:"action"`
		Keys   []string `json:"keys"`
	} `json:"rows"`
	GUIActions map[string]string `json:"gui_actions"`
}

type wireEvent struct {
	Type     string        `json:"type"`
	Message  string        `json:"message"`
	State    *wireState    `json:"state"`
	Result   *wireResult   `json:"result"`
	Keybinds *wireKeybinds `json:"keybinds"`
}

// guiBridge is one running `tuios gui-bridge`.
type guiBridge struct {
	t    *testing.T
	in   io.WriteCloser
	wmu  sync.Mutex
	mu   sync.Mutex
	cond *sync.Cond
	// state is the newest state, keybinds the newest keybinds event, and
	// keybindsSeen how many keybinds events came.
	state        *wireState
	keybinds     *wireKeybinds
	keybindsSeen int
	results      map[int64]wireResult
	// events holds every event of another type, in the order it came, for
	// the Inbox tests (gui_bridge_inbox_test.go).
	events []json.RawMessage
	// snaps counts the SNAP frames of each PTY, for an in-place session
	// switch, which sends a snapshot of every pane of the new session.
	snaps map[string]int
	// outputs holds each PTY's OUTPUT bytes, for the kept emulators of wave
	// 4 (gui_bridge_wave4_test.go).
	outputs map[string][]byte
	closed  bool
	req     int64
	// log is every command sent and every result, kept as the test's
	// artifact.
	log []string
	// pid is the bridge process, to stop it as a crash does.
	pid int
}

// startBridge runs a bridge on session under the isolation root base, sized
// cols by rows, and waits for its first state.
func startBridge(t *testing.T, base, session string, cols, rows int) *guiBridge {
	t.Helper()
	return startBridgeWith(t, base, bridgeOpts{args: []string{"--session", session}, cols: cols, rows: rows, name: session})
}

// bridgeOpts are the flags and environment of one bridge. keepDaemon leaves
// a daemon an earlier bridge started running, for a bridge that comes back.
type bridgeOpts struct {
	args       []string
	env        []string
	cols, rows int
	keepDaemon bool
	// name labels the bridge's artifact.
	name string
	// noWait returns before the first state, for a bridge that is expected
	// to stop.
	noWait bool
}

// startBridgeWith runs a bridge with opts under the isolation root base.
func startBridgeWith(t *testing.T, base string, opts bridgeOpts) *guiBridge {
	t.Helper()
	if !opts.keepDaemon {
		killDaemon(t, base)
	}
	pinPreV080Looks(t, base)
	session := opts.name
	args := append([]string{"gui-bridge", "--cols", fmt.Sprint(opts.cols), "--rows", fmt.Sprint(opts.rows)}, opts.args...)
	cmd := exec.Command(tuiosBin, args...)
	cmd.Dir = workDirIn(t, base)
	cmd.Env = append(os.Environ(), "SHELL=/bin/sh", "ENV=", "PS1=$ ")
	cmd.Env = append(cmd.Env, opts.env...)
	for _, key := range xdgKeys {
		cmd.Env = append(cmd.Env, key+"="+xdgDir(base, key))
	}
	logPath := filepath.Join(t.TempDir(), "bridge.log")
	logFile, err := os.Create(logPath)
	if err != nil {
		t.Fatalf("bridge log: %v", err)
	}
	cmd.Stderr = logFile
	in, err := cmd.StdinPipe()
	if err != nil {
		t.Fatalf("bridge stdin: %v", err)
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("bridge stdout: %v", err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start bridge: %v", err)
	}
	b := &guiBridge{t: t, in: in, results: map[int64]wireResult{}, pid: cmd.Process.Pid}
	bridgeCount++
	logName := fmt.Sprintf("bridge-%d-%s", bridgeCount, session)
	b.cond = sync.NewCond(&b.mu)
	done := make(chan struct{})
	go func() {
		defer close(done)
		b.read(out)
	}()
	t.Cleanup(func() {
		_ = in.Close()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			// Stopped by its own PID, never by name.
			_ = cmd.Process.Kill()
			<-done
		}
		_ = cmd.Wait()
		_ = logFile.Close()
		b.saveLog(logName)
		if t.Failed() {
			data, _ := os.ReadFile(logPath)
			if len(data) > 4000 {
				data = data[len(data)-4000:]
			}
			t.Logf("bridge stderr tail:\n%s", data)
		}
	})
	if !opts.noWait {
		b.waitState(func(*wireState) bool { return true }, "the first state")
	}
	return b
}

// bridgeCount numbers the bridges a run starts, for the artifact names.
var bridgeCount int

// saveLog writes the commands, the results and the last state to
// $TUIOS_E2E_FRAMES/<test>/<name>.jsonl when that directory is set.
func (b *guiBridge) saveLog(name string) {
	if os.Getenv("TUIOS_E2E_FRAMES") == "" {
		return
	}
	b.mu.Lock()
	lines := append([]string{}, b.log...)
	if st, err := json.Marshal(map[string]any{"last_state": b.state}); err == nil {
		lines = append(lines, string(st))
	}
	b.mu.Unlock()
	path := filepath.Join(artifactDir(b.t), name+".jsonl")
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		b.t.Logf("save %s: %v", path, err)
	}
}

// read decodes the bridge's frames until its output closes.
func (b *guiBridge) read(r io.Reader) {
	br := bufio.NewReaderSize(r, 256<<10)
	var hdr [5]byte
	for {
		if _, err := io.ReadFull(br, hdr[:]); err != nil {
			break
		}
		n := binary.BigEndian.Uint32(hdr[:4])
		body := make([]byte, n-1)
		if _, err := io.ReadFull(br, body); err != nil {
			break
		}
		if hdr[4] == 3 && len(body) > 0 && len(body) >= 1+int(body[0]) {
			b.mu.Lock()
			if b.snaps == nil {
				b.snaps = map[string]int{}
			}
			b.snaps[string(body[1:1+int(body[0])])]++
			b.cond.Broadcast()
			b.mu.Unlock()
			continue
		}
		if hdr[4] == 2 && len(body) > 0 && len(body) >= 1+int(body[0]) {
			pty := string(body[1 : 1+int(body[0])])
			b.mu.Lock()
			if b.outputs == nil {
				b.outputs = map[string][]byte{}
			}
			b.outputs[pty] = append(b.outputs[pty], body[1+int(body[0]):]...)
			b.cond.Broadcast()
			b.mu.Unlock()
			continue
		}
		if hdr[4] != 1 {
			continue
		}
		var ev wireEvent
		if err := json.Unmarshal(body, &ev); err != nil {
			continue
		}
		b.mu.Lock()
		switch ev.Type {
		case "state":
			b.state = ev.State
		case "keybinds":
			b.keybinds = ev.Keybinds
			b.keybindsSeen++
		case "result":
			if ev.Result != nil {
				b.results[ev.Result.Req] = *ev.Result
			}
		default:
			b.events = append(b.events, body)
		}
		b.cond.Broadcast()
		b.mu.Unlock()
	}
	b.mu.Lock()
	b.closed = true
	b.cond.Broadcast()
	b.mu.Unlock()
}

// send writes one JSON command frame.
func (b *guiBridge) send(cmd map[string]any) {
	b.t.Helper()
	body, err := json.Marshal(cmd)
	if err != nil {
		b.t.Fatalf("encode command: %v", err)
	}
	b.frame(1, body)
}

func (b *guiBridge) frame(kind byte, body []byte) {
	b.t.Helper()
	var buf bytes.Buffer
	_ = binary.Write(&buf, binary.BigEndian, uint32(len(body)+1))
	buf.WriteByte(kind)
	buf.Write(body)
	b.wmu.Lock()
	defer b.wmu.Unlock()
	if _, err := b.in.Write(buf.Bytes()); err != nil {
		b.t.Fatalf("write to bridge: %v", err)
	}
}

// input types text into a pane's PTY through the bridge.
func (b *guiBridge) input(pty, text string) {
	b.t.Helper()
	body := append([]byte{byte(len(pty))}, pty...)
	b.frame(2, append(body, text...))
}

// call sends a command with a fresh req and returns its result.
func (b *guiBridge) call(cmd map[string]any) wireResult {
	b.t.Helper()
	b.mu.Lock()
	b.req++
	req := b.req
	b.mu.Unlock()
	cmd["req"] = req
	if sent, err := json.Marshal(map[string]any{"sent": cmd}); err == nil {
		b.mu.Lock()
		b.log = append(b.log, string(sent))
		b.mu.Unlock()
	}
	b.send(cmd)
	deadline := time.Now().Add(uiTimeout)
	b.mu.Lock()
	defer b.mu.Unlock()
	for {
		if r, ok := b.results[req]; ok {
			if got, err := json.Marshal(map[string]any{"result": r, "tree": shape(b.state.tree(), nil)}); err == nil {
				b.log = append(b.log, string(got))
			}
			return r
		}
		if b.closed {
			b.t.Fatalf("bridge closed before it answered %v", cmd)
		}
		if time.Now().After(deadline) {
			b.t.Fatalf("no result for %v within %v", cmd, uiTimeout)
		}
		b.waitLocked(100 * time.Millisecond)
	}
}

// mustCall is call for a command that has to succeed.
func (b *guiBridge) mustCall(cmd map[string]any) wireResult {
	b.t.Helper()
	r := b.call(cmd)
	if !r.OK {
		b.t.Fatalf("%v failed: %s", cmd, r.Error)
	}
	return r
}

// waitLocked waits on the condition for at most d. Called with mu held.
func (b *guiBridge) waitLocked(d time.Duration) {
	timer := time.AfterFunc(d, func() {
		b.mu.Lock()
		b.cond.Broadcast()
		b.mu.Unlock()
	})
	b.cond.Wait()
	timer.Stop()
}

// waitState waits until the newest state satisfies ok and returns it.
func (b *guiBridge) waitState(ok func(*wireState) bool, what string) *wireState {
	b.t.Helper()
	deadline := time.Now().Add(shellTimeout)
	b.mu.Lock()
	defer b.mu.Unlock()
	for {
		if b.state != nil && ok(b.state) {
			return b.state
		}
		if b.closed {
			b.t.Fatalf("bridge closed while waiting for %s", what)
		}
		if time.Now().After(deadline) {
			got, _ := json.Marshal(b.state)
			b.t.Fatalf("bridge never showed %s; last state: %s", what, got)
		}
		b.waitLocked(100 * time.Millisecond)
	}
}

// tiledPanes gets the session to n tiled panes on the split tree, and returns
// their window ids in the order they were made.
func (b *guiBridge) tiledPanes(n int) []string {
	b.t.Helper()
	st := b.waitState(func(*wireState) bool { return true }, "a state")
	for i := len(st.Windows); i < n; i++ {
		b.mustCall(map[string]any{"cmd": "action", "name": "new_window"})
		want := i + 1
		b.waitState(func(s *wireState) bool { return len(s.Windows) == want }, fmt.Sprintf("%d panes", want))
	}
	// Tiling is turned on once there are panes: a session with none ignores
	// the toggle.
	if st = b.waitState(func(*wireState) bool { return true }, "a state"); !st.Tiling {
		b.mustCall(map[string]any{"cmd": "action", "name": "toggle_tiling"})
		b.waitState(func(s *wireState) bool { return s.Tiling }, "tiling on")
	}
	st = b.waitState(func(s *wireState) bool {
		return s.Layout == "bsp" && leafCount(s.tree()) == n
	}, fmt.Sprintf("a tree of %d panes", n))
	var ids []string
	for _, w := range st.Windows {
		ids = append(ids, w.ID)
	}
	return ids
}

// tree is the split tree of the workspace on screen, or nil.
func (s *wireState) tree() *wireNode {
	if s == nil || s.Trees == nil {
		return nil
	}
	t := s.Trees[fmt.Sprint(s.Workspace)]
	if t == nil {
		return nil
	}
	return t.Root
}

func (s *wireState) window(id string) *wireWindow {
	for i := range s.Windows {
		if s.Windows[i].ID == id {
			return &s.Windows[i]
		}
	}
	return nil
}

func leafCount(n *wireNode) int {
	if n == nil {
		return 0
	}
	if n.A == nil && n.B == nil {
		return 1
	}
	return leafCount(n.A) + leafCount(n.B)
}

// leaf finds the leaf of a window.
func leaf(n *wireNode, id string) *wireNode {
	if n == nil {
		return nil
	}
	if n.Window == id {
		return n
	}
	if f := leaf(n.A, id); f != nil {
		return f
	}
	return leaf(n.B, id)
}

// parentOf finds the split whose child is c.
func parentOf(n, c *wireNode) *wireNode {
	if n == nil || (n.A == nil && n.B == nil) {
		return nil
	}
	if n.A == c || n.B == c {
		return n
	}
	if p := parentOf(n.A, c); p != nil {
		return p
	}
	return parentOf(n.B, c)
}

// shape writes a tree with names in place of window ids and with no node ids,
// so two clients' trees compare equal when they hold the same layout.
func shape(n *wireNode, names map[string]string) string {
	if n == nil {
		return "-"
	}
	if n.A == nil && n.B == nil {
		if name, ok := names[n.Window]; ok {
			return name
		}
		return "?" + n.Window
	}
	return fmt.Sprintf("%s%.3f(%s %s)", n.Axis, n.Ratio, shape(n.A, names), shape(n.B, names))
}

// paneNames names panes A, B, C... in the order given.
func paneNames(ids []string) map[string]string {
	names := map[string]string{}
	for i, id := range ids {
		names[id] = string(rune('A' + i))
	}
	return names
}

// confirmByObserver attaches a second bridge to the session and checks that
// the tree it reads back from the daemon has the same shape. The second
// bridge runs the terminal client's model, so this is what any client that
// attaches now is given.
func confirmByObserver(t *testing.T, base, session string, want string, names map[string]string) {
	t.Helper()
	obs := startBridge(t, base, session, 120, 40)
	got := ""
	obs.waitState(func(s *wireState) bool {
		got = shape(s.tree(), names)
		return got == want
	}, "the same tree in a second client: want "+want+", last "+got)
}

// terminalShowsLayout attaches a terminal client and checks that each pane's
// marker shows inside the rectangle the bridge gives the pane. The session has
// one size, so the terminal client and the bridge lay the tree out alike.
func terminalShowsLayout(t *testing.T, term *tuitest.Terminal, b *guiBridge, markers map[string]string) {
	t.Helper()
	st := b.waitState(func(*wireState) bool { return true }, "a state")
	err := term.WaitFor(func(s tuitest.Screen) bool {
		for id, mark := range markers {
			w := st.window(id)
			if w == nil {
				return false
			}
			col, row, ok := tuitest.Find(s, mark)
			if !ok || col < w.X || col >= w.X+w.W || row < w.Y || row >= w.Y+w.H {
				return false
			}
		}
		return true
	}, shellTimeout)
	if err != nil {
		var want []string
		for id, mark := range markers {
			if w := st.window(id); w != nil {
				want = append(want, fmt.Sprintf("%s in %d,%d %dx%d", mark, w.X, w.Y, w.W, w.H))
			}
		}
		sort.Strings(want)
		t.Fatalf("terminal client does not show the bridge's layout (%s): %v\n%s", strings.Join(want, "; "), err, term.Snapshot())
	}
}

// markPanes prints a marker in every pane through the bridge's input frames,
// and returns the marker of each pane.
func markPanes(t *testing.T, b *guiBridge, ids []string) map[string]string {
	t.Helper()
	st := b.waitState(func(*wireState) bool { return true }, "a state")
	marks := map[string]string{}
	for i, id := range ids {
		w := st.window(id)
		if w == nil {
			t.Fatalf("no pane %s", id)
		}
		// The marker is built by the shell, so the echoed command line does
		// not contain it.
		letter := string(rune('A' + i))
		marks[id] = "PANE" + letter + "MARK"
		b.input(w.PTY, "printf 'PANE%sMARK\\n' "+letter+"\r")
	}
	return marks
}

// TestGUIBridgeSetRatio: set-ratio moves the split it names, and holds a ratio
// that would squeeze a pane under the smallest size.
func TestGUIBridgeSetRatio(t *testing.T) {
	base := t.TempDir()
	b := startBridge(t, base, "gb", 120, 40)
	ids := b.tiledPanes(2)
	root := b.waitState(func(*wireState) bool { return true }, "a state").tree()
	if root.Axis == "" || root.Ratio != 0.5 {
		t.Fatalf("want a fresh 0.5 split at the root, got %+v", root)
	}
	r := b.mustCall(map[string]any{"cmd": "layout", "op": "set-ratio", "split": root.ID, "ratio": 0.3})
	if r.Ratio != 0.3 {
		t.Errorf("result ratio = %v, want 0.3", r.Ratio)
	}
	st := b.waitState(func(s *wireState) bool { return s.tree() != nil && s.tree().Ratio == 0.3 }, "the root at 0.3")
	// The panes moved with the split: the first pane's side is now 30 %.
	a := leaf(st.tree(), st.tree().A.Window)
	extent := st.tree().Rect.W
	if st.tree().Axis == "y" {
		extent = st.tree().Rect.H
	}
	got := a.Rect.W
	if st.tree().Axis == "y" {
		got = a.Rect.H
	}
	if want := int(float64(extent) * 0.3); got != want {
		t.Errorf("near pane is %d cells, want %d", got, want)
	}

	// The positive half above, and here the hold: 0.99 would leave the far
	// pane under the smallest pane size, so the split stops short of it.
	r = b.mustCall(map[string]any{"cmd": "layout", "op": "set-ratio", "split": root.ID, "ratio": 0.99})
	if r.Ratio >= 0.99 || r.Ratio <= 0.5 {
		t.Errorf("a 0.99 request came back as %v; want it held below 0.99", r.Ratio)
	}

	// An unknown split is refused, not guessed at.
	if r := b.call(map[string]any{"cmd": "layout", "op": "set-ratio", "split": 999999, "ratio": 0.4}); r.OK {
		t.Errorf("set-ratio on a split that does not exist succeeded")
	}
	names := paneNames(ids)
	confirmByObserver(t, base, "gb", shape(b.waitState(func(*wireState) bool { return true }, "a state").tree(), names), names)
}

// TestGUIBridgeSwap: swap trades two panes' places in the tree, and a terminal
// client on the same session draws them in their new places.
func TestGUIBridgeSwap(t *testing.T) {
	base := t.TempDir()
	b := startBridge(t, base, "gb", 120, 40)
	ids := b.tiledPanes(3)
	names := paneNames(ids)
	beforeTree := b.waitState(func(*wireState) bool { return true }, "a state").tree()

	term := attachIn(t, base, "gb", startOpts{cols: 120, rows: 40})
	marks := markPanes(t, b, ids)
	terminalShowsLayout(t, term, b, marks)
	saveFrame(t, term, "gui-bridge-swap-before")

	b.mustCall(map[string]any{"cmd": "layout", "op": "swap", "a": ids[0], "b": ids[2]})
	// The tree from before with A and C trading leaves.
	swapped := paneNames(ids)
	swapped[ids[0]], swapped[ids[2]] = "C", "A"
	want := shape(beforeTree, swapped)
	b.waitState(func(s *wireState) bool { return shape(s.tree(), names) == want }, "A and C swapped: "+want)
	terminalShowsLayout(t, term, b, marks)
	saveFrame(t, term, "gui-bridge-swap-after")
	confirmByObserver(t, base, "gb", want, names)
}

// TestGUIBridgeMove: move puts a pane beside another on the side named, in a
// new split, and the space it leaves goes to its sibling.
func TestGUIBridgeMove(t *testing.T) {
	base := t.TempDir()
	b := startBridge(t, base, "gb", 120, 40)
	ids := b.tiledPanes(3)
	names := paneNames(ids)
	st := b.waitState(func(*wireState) bool { return true }, "a state")
	if p := parentOf(st.tree(), leaf(st.tree(), ids[0])); p != nil && (p.A.Window == ids[2] || p.B.Window == ids[2]) {
		t.Fatalf("A and C start as siblings, so the move would prove nothing: %s", shape(st.tree(), names))
	}

	term := attachIn(t, base, "gb", startOpts{cols: 120, rows: 40})
	marks := markPanes(t, b, ids)
	terminalShowsLayout(t, term, b, marks)
	saveFrame(t, term, "gui-bridge-move-before")

	b.mustCall(map[string]any{"cmd": "layout", "op": "move", "id": ids[0], "target": ids[2], "side": "left"})
	st = b.waitState(func(s *wireState) bool {
		p := parentOf(s.tree(), leaf(s.tree(), ids[0]))
		return p != nil && p.Axis == "x" && p.A != nil && p.A.Window == ids[0] && p.B.Window == ids[2]
	}, "A left of C in a split of their own")
	if p := parentOf(st.tree(), leaf(st.tree(), ids[0])); p.Ratio != 0.5 {
		t.Errorf("the new split is %v, want 0.5", p.Ratio)
	}
	if st.Focused != ids[0] {
		t.Errorf("focus is on %s, want the pane that moved", names[st.Focused])
	}
	terminalShowsLayout(t, term, b, marks)
	saveFrame(t, term, "gui-bridge-move-after")
	confirmByObserver(t, base, "gb", shape(st.tree(), names), names)

	// Moving a pane beside itself changes nothing and says so.
	if r := b.call(map[string]any{"cmd": "layout", "op": "move", "id": ids[1], "target": ids[1], "side": "top"}); r.OK {
		t.Errorf("a move beside itself succeeded")
	}
}

// TestGUIBridgeMoveRoot: move-root puts a pane on one side of everything else.
func TestGUIBridgeMoveRoot(t *testing.T) {
	base := t.TempDir()
	b := startBridge(t, base, "gb", 120, 40)
	ids := b.tiledPanes(3)
	names := paneNames(ids)
	st := b.waitState(func(*wireState) bool { return true }, "a state")
	if r := st.tree(); r.Axis == "y" && r.B.Window == ids[1] {
		t.Fatalf("B already sits below everything: %s", shape(r, names))
	}
	b.mustCall(map[string]any{"cmd": "layout", "op": "move-root", "id": ids[1], "side": "bottom"})
	st = b.waitState(func(s *wireState) bool {
		r := s.tree()
		return r != nil && r.Axis == "y" && r.B != nil && r.B.Window == ids[1] && leafCount(r.A) == 2
	}, "B below the other two")
	w := st.window(ids[1])
	tr := st.Trees[fmt.Sprint(st.Workspace)]
	if w.W != tr.Bounds.W {
		t.Errorf("B is %d cells wide, want the whole width %d", w.W, tr.Bounds.W)
	}
	confirmByObserver(t, base, "gb", shape(st.tree(), names), names)

	// The only tiled pane has nothing to go beside.
	b2 := startBridge(t, t.TempDir(), "solo", 120, 40)
	solo := b2.tiledPanes(1)
	if r := b2.call(map[string]any{"cmd": "layout", "op": "move-root", "id": solo[0], "side": "left"}); r.OK {
		t.Errorf("move-root of the only pane succeeded")
	}
}

// TestGUIBridgeEqualize: equalize sets every split back to 0.5.
func TestGUIBridgeEqualize(t *testing.T) {
	base := t.TempDir()
	b := startBridge(t, base, "gb", 120, 40)
	ids := b.tiledPanes(3)
	names := paneNames(ids)
	root := b.waitState(func(*wireState) bool { return true }, "a state").tree()
	b.mustCall(map[string]any{"cmd": "layout", "op": "set-ratio", "split": root.ID, "ratio": 0.3})
	b.waitState(func(s *wireState) bool { return s.tree().Ratio == 0.3 }, "the root at 0.3")
	b.mustCall(map[string]any{"cmd": "layout", "op": "equalize"})
	st := b.waitState(func(s *wireState) bool { return allHalf(s.tree()) }, "every split at 0.5")
	confirmByObserver(t, base, "gb", shape(st.tree(), names), names)
}

func allHalf(n *wireNode) bool {
	if n == nil || (n.A == nil && n.B == nil) {
		return true
	}
	return n.Ratio == 0.5 && allHalf(n.A) && allHalf(n.B)
}

// TestGUIBridgeToggleFloat: toggle-float takes a pane out of the tree and
// back in.
func TestGUIBridgeToggleFloat(t *testing.T) {
	base := t.TempDir()
	b := startBridge(t, base, "gb", 120, 40)
	ids := b.tiledPanes(3)
	b.mustCall(map[string]any{"cmd": "layout", "op": "toggle-float", "id": ids[1]})
	st := b.waitState(func(s *wireState) bool {
		w := s.window(ids[1])
		return w != nil && w.Floating && leafCount(s.tree()) == 2 && leaf(s.tree(), ids[1]) == nil
	}, "B floating and out of the tree")
	if st.Focused != ids[1] {
		t.Errorf("focus is not on the pane that floated")
	}
	b.mustCall(map[string]any{"cmd": "layout", "op": "toggle-float", "id": ids[1]})
	b.waitState(func(s *wireState) bool {
		w := s.window(ids[1])
		return w != nil && !w.Floating && leaf(s.tree(), ids[1]) != nil
	}, "B tiled again")
}

// TestGUIBridgeFloatGeometry: float-geometry places a floating pane, and every
// other client sees it there.
func TestGUIBridgeFloatGeometry(t *testing.T) {
	base := t.TempDir()
	b := startBridge(t, base, "gb", 120, 40)
	ids := b.tiledPanes(2)
	b.mustCall(map[string]any{"cmd": "layout", "op": "toggle-float", "id": ids[1]})
	b.waitState(func(s *wireState) bool { return s.window(ids[1]).Floating }, "B floating")
	rect := map[string]int{"x": 12, "y": 6, "w": 50, "h": 15}
	b.mustCall(map[string]any{"cmd": "layout", "op": "float-geometry", "id": ids[1], "rect": rect})
	at := func(w *wireWindow) bool { return w != nil && w.X == 12 && w.Y == 6 && w.W == 50 && w.H == 15 }
	b.waitState(func(s *wireState) bool { return at(s.window(ids[1])) }, "B at 12,6 50x15")

	obs := startBridge(t, base, "gb", 120, 40)
	obs.waitState(func(s *wireState) bool { return at(s.window(ids[1])) }, "B at 12,6 50x15 in a second client")

	// A tiled pane has no floating geometry to set.
	if r := b.call(map[string]any{"cmd": "layout", "op": "float-geometry", "id": ids[0], "rect": rect}); r.OK {
		t.Errorf("float-geometry on a tiled pane succeeded")
	}
}

// TestGUIBridgeAction: action runs a registry action on the allow list, and
// refuses one that only opens something a screen would draw.
func TestGUIBridgeAction(t *testing.T) {
	base := t.TempDir()
	b := startBridge(t, base, "gb", 120, 40)
	ids := b.tiledPanes(2)
	b.mustCall(map[string]any{"cmd": "action", "name": "toggle_zoom"})
	b.waitState(func(s *wireState) bool { return s.Zoomed == s.Focused && s.Zoomed != "" }, "the focused pane zoomed")
	b.mustCall(map[string]any{"cmd": "action", "name": "toggle_zoom"})
	b.waitState(func(s *wireState) bool { return s.Zoomed == "" }, "no pane zoomed")
	b.mustCall(map[string]any{"cmd": "action", "name": "split_vertical"})
	b.waitState(func(s *wireState) bool { return len(s.Windows) == len(ids)+1 }, "a third pane from split_vertical")
	// The names the renderer's own prefix resolver ends with run too.
	b.mustCall(map[string]any{"cmd": "action", "name": "workspace_prefix_switch_2"})
	b.waitState(func(s *wireState) bool { return s.Workspace == 2 }, "workspace 2 from the workspace prefix")
	b.mustCall(map[string]any{"cmd": "action", "name": "switch_workspace_1"})
	b.waitState(func(s *wireState) bool { return s.Workspace == 1 }, "workspace 1 again")
	b.mustCall(map[string]any{"cmd": "action", "name": "prefix_split_horizontal"})
	b.waitState(func(s *wireState) bool { return len(s.Windows) == len(ids)+2 }, "a fourth pane from the leader's split")

	for _, name := range []string{"toggle_help", "command_palette", "gui_pane_picker", "no_such_action"} {
		if r := b.call(map[string]any{"cmd": "action", "name": name}); r.OK || r.Error == "" {
			t.Errorf("action %s: ok=%v error=%q, want a refusal that says why", name, r.OK, r.Error)
		}
	}
}

// TestGUIBridgeKeybinds: the keybinds event carries the user's own leader and
// keys, the gui scope, and the which-key menus, and comes again when the
// config file changes.
func TestGUIBridgeKeybinds(t *testing.T) {
	base := t.TempDir()
	pinPreV080Looks(t, base)
	cfg := filepath.Join(xdgDir(base, "XDG_CONFIG_HOME"), "tuios", "config.toml")
	data, err := os.ReadFile(cfg)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	// The user's leader is ctrl+a, and a command key of their own under it.
	// The first-run file has no keybinding tables, only comments about them.
	if strings.Contains(string(data), "\n[keybindings") {
		t.Fatalf("the first-run config already has a keybindings table; this edit assumes it has none")
	}
	edited := string(data) + "\n[keybindings]\nleader_key = \"ctrl+a\"\n\n" +
		"[[keybindings.command]]\nkey = \"prefix+y\"\ncommand = \"echo hi\"\ndescription = \"Say hi\"\n"
	writeConfigAtomically(t, cfg, []byte(edited))

	b := startBridge(t, base, "gb", 120, 40)
	kb := b.waitKeybinds(1)
	if kb.Leader != "ctrl+a" {
		t.Fatalf("leader = %q, want the user's ctrl+a", kb.Leader)
	}
	has := func(kb *wireKeybinds, scope, action, press string) bool {
		for _, x := range kb.Bindings {
			if x.Scope == scope && x.Action == action && x.Press == press {
				return true
			}
		}
		return false
	}
	if !has(kb, "prefix", "prefix_window", "ctrl+a t") {
		t.Errorf("no prefix binding spelled with the user's leader (ctrl+a t)")
	}
	if !has(kb, "gui", "new_window", "ctrl+shift+t") || !has(kb, "gui", "gui_pane_picker", "ctrl+shift+space") {
		t.Errorf("the gui scope's default chords are missing")
	}
	if kb.GUIActions["gui_pane_picker"] == "" {
		t.Errorf("gui_actions does not describe gui_pane_picker")
	}
	if kb.Prefixes["prefix_window"] != "prefix.window" {
		t.Errorf("prefixes[prefix_window] = %q", kb.Prefixes["prefix_window"])
	}
	command := ""
	for _, x := range kb.Bindings {
		if x.Scope == "prefix" && x.Press == "ctrl+a y" {
			command = x.Action
		}
	}
	if command == "" {
		t.Fatalf("the user's command key ctrl+a y is not in the bindings")
	}
	// The action the key names runs the user's command, a popup by default.
	b.mustCall(map[string]any{"cmd": "action", "name": command})
	b.waitState(func(s *wireState) bool {
		for _, w := range s.Windows {
			if w.Popup {
				return true
			}
		}
		return false
	}, "the popup the command key opens")
	menuHas := func(name, action string) bool {
		for _, g := range kb.Menus[name] {
			for _, r := range g.Rows {
				if r.Action == action {
					return true
				}
			}
		}
		return false
	}
	if !menuHas("", "prefix_window") || len(kb.Menus["window"]) == 0 {
		t.Errorf("the which-key menus are missing rows: %v", kb.Menus[""])
	}
	gui := false
	for _, r := range kb.Rows {
		if r.Scope == "gui" {
			gui = true
		}
	}
	if !gui {
		t.Errorf("the list rows have no gui scope")
	}

	// A rebind in the file reaches the renderer without a restart.
	rebound := edited + "\n[keybindings.gui]\ntoggle_zoom = [\"ctrl+shift+m\"]\n"
	writeConfigAtomically(t, cfg, []byte(rebound))
	kb = b.waitKeybinds(2)
	if !has(kb, "gui", "toggle_zoom", "ctrl+shift+m") || has(kb, "gui", "toggle_zoom", "ctrl+shift+z") {
		t.Errorf("after the reload, toggle_zoom is not on ctrl+shift+m alone")
	}
}

// waitKeybinds waits for the n-th keybinds event and returns it.
func (b *guiBridge) waitKeybinds(n int) *wireKeybinds {
	b.t.Helper()
	deadline := time.Now().Add(shellTimeout)
	b.mu.Lock()
	defer b.mu.Unlock()
	for b.keybindsSeen < n {
		if b.closed || time.Now().After(deadline) {
			b.t.Fatalf("keybinds event %d never came (saw %d)", n, b.keybindsSeen)
		}
		b.waitLocked(100 * time.Millisecond)
	}
	return b.keybinds
}

// TestGUIBridgeSessionSize: with a smaller terminal client on the session, the
// bridge says the session is that client's size and its own is larger.
func TestGUIBridgeSessionSize(t *testing.T) {
	base := t.TempDir()
	b := startBridge(t, base, "gb", 160, 50)
	b.tiledPanes(2)
	st := b.waitState(func(s *wireState) bool { return s.SessionSize.Cols == 160 }, "the session at the bridge's own width")
	if st.ClientSize != (wireSize{Cols: 160, Rows: 50}) {
		t.Errorf("client_size = %+v, want 160x50", st.ClientSize)
	}
	term := attachIn(t, base, "gb", startOpts{cols: 80, rows: 24})
	st = b.waitState(func(s *wireState) bool {
		return s.SessionSize.Cols > 0 && s.SessionSize.Cols <= 80 && s.SessionSize.Rows <= 24
	}, "the session at the terminal client's size")
	if st.ClientSize != (wireSize{Cols: 160, Rows: 50}) {
		t.Errorf("client_size = %+v with a small client attached, want 160x50", st.ClientSize)
	}
	if st.Viewport != nil {
		t.Errorf("viewport is set, but the session is smaller than the bridge")
	}
	tr := st.Trees[fmt.Sprint(st.Workspace)]
	if tr == nil || tr.Bounds.X+tr.Bounds.W > st.SessionSize.Cols {
		t.Errorf("the tree is laid out past the session's width: %+v", tr)
	}
	_ = term
}

// TestGUIBridgeSessionName: a switch made from inside the session moves the
// bridge, and its state names the session it is on now.
func TestGUIBridgeSessionName(t *testing.T) {
	base := t.TempDir()
	if out, err := tuiosCLI(t, base, "new", "--detach", "other"); err != nil {
		t.Fatalf("make the second session: %v\n%s", err, out)
	}
	b := startBridge(t, base, "first", 120, 40)
	b.tiledPanes(1)
	b.waitState(func(s *wireState) bool { return s.Session == "first" }, "session first")
	b.mustCall(map[string]any{"cmd": "action", "name": "next_session"})
	b.waitState(func(s *wireState) bool { return s.Session == "other" }, "session other after next_session")
}

// TestGUIBridgeGitBranch: a pane in a git checkout carries its repository and
// branch. The bridge reads them off the model's goroutine and sends them in
// the state that follows the read.
func TestGUIBridgeGitBranch(t *testing.T) {
	base := t.TempDir()
	dir := workDirIn(t, base)
	git := exec.Command("git", "init", "-q", "-b", "bridge-branch", dir)
	if out, err := git.CombinedOutput(); err != nil {
		t.Skipf("git init: %v\n%s", err, out)
	}
	b := startBridge(t, base, "gb", 120, 40)
	ids := b.tiledPanes(1)
	st := b.waitState(func(s *wireState) bool {
		w := s.window(ids[0])
		return w != nil && w.Branch == "bridge-branch"
	}, "the pane on branch bridge-branch")
	if w := st.window(ids[0]); w.Repo != filepath.Base(dir) {
		t.Errorf("repo = %q, want %q", w.Repo, filepath.Base(dir))
	}
}

// along is a node's extent and position along a split's axis: width and x for
// "x", height and y for "y".
func along(r wireRect, axis string) (pos, extent int) {
	if axis == "y" {
		return r.Y, r.H
	}
	return r.X, r.W
}

// splitAcross is the split action that cuts a pane across axis: a pane cut
// along x gets a new pane beside it.
func splitAcross(axis string) string {
	if axis == "y" {
		return "split_horizontal"
	}
	return "split_vertical"
}

// TestGUIBridgeSplitRefusesSmallPanes: a split that would leave either pane
// under the smallest size (20 columns by 5 rows) is refused, and the result
// says why. A split the other way, where both panes fit, still runs.
func TestGUIBridgeSplitRefusesSmallPanes(t *testing.T) {
	base := t.TempDir()
	b := startBridge(t, base, "gb", 120, 40)
	ids := b.tiledPanes(2)
	root := b.waitState(func(*wireState) bool { return true }, "a state").tree()
	_, extent := along(root.Rect, root.Axis)
	// The first pane gets 40 cells along the root's axis on x (halves of 20
	// and 19 plus the gap), or 10 rows on y (5 and 4 plus the gap): too small
	// to split again that way, by one cell.
	want := 40
	if root.Axis == "y" {
		want = 10
	}
	ratio := (float64(want) + 0.5) / float64(extent)
	b.mustCall(map[string]any{"cmd": "layout", "op": "set-ratio", "split": root.ID, "ratio": ratio})
	st := b.waitState(func(s *wireState) bool {
		_, e := along(leaf(s.tree(), ids[0]).Rect, root.Axis)
		return e == want
	}, fmt.Sprintf("the first pane at %d cells", want))
	b.send(map[string]any{"cmd": "focus", "window": ids[0]})
	b.waitState(func(s *wireState) bool { return s.Focused == ids[0] }, "focus on the first pane")

	r := b.call(map[string]any{"cmd": "action", "name": splitAcross(root.Axis)})
	if r.OK || !strings.Contains(r.Error, "too small") {
		t.Fatalf("%s on a %d-cell pane: ok=%v error=%q; want a refusal that says the pane is too small", splitAcross(root.Axis), want, r.OK, r.Error)
	}
	// Give a split that slipped through time to arrive.
	time.Sleep(500 * time.Millisecond)
	if st = b.waitState(func(*wireState) bool { return true }, "a state"); len(st.Windows) != 2 {
		t.Fatalf("the refused split still made a pane: %d panes", len(st.Windows))
	}

	// One cell more and the same split runs: both halves are at the minimum.
	b.mustCall(map[string]any{"cmd": "layout", "op": "set-ratio", "split": root.ID, "ratio": (float64(want+1) + 0.5) / float64(extent)})
	b.waitState(func(s *wireState) bool {
		_, e := along(leaf(s.tree(), ids[0]).Rect, root.Axis)
		return e == want+1
	}, fmt.Sprintf("the first pane at %d cells", want+1))
	b.mustCall(map[string]any{"cmd": "action", "name": splitAcross(root.Axis)})
	st = b.waitState(func(s *wireState) bool { return len(s.Windows) == 3 && leafCount(s.tree()) == 3 }, "a third pane")
	for _, w := range st.Windows {
		if w.W < 20 || w.H < 5 {
			t.Errorf("pane %s is %dx%d, under 20x5", w.ID, w.W, w.H)
		}
	}
}

// TestGUIBridgeSetRatioUnderMinimum: a split whose side is already under the
// smallest size (the window shrank under it) takes a ratio that moves its line
// one cell, and holds at the current line, not at the far bound. Before, the
// bound was the minimum, so the first step of a drag from there jumped the
// line to it.
func TestGUIBridgeSetRatioUnderMinimum(t *testing.T) {
	base := t.TempDir()
	b := startBridge(t, base, "gb", 120, 40)
	ids := b.tiledPanes(2)
	root := b.waitState(func(*wireState) bool { return true }, "a state").tree()
	axis := root.Axis
	b.mustCall(map[string]any{"cmd": "layout", "op": "set-ratio", "split": root.ID, "ratio": 0.3})
	b.waitState(func(s *wireState) bool { return s.tree().Ratio == 0.3 }, "the root at 0.3")

	// Shrink the grid along the axis until 0.3 of it is under the minimum.
	cols, rows, size := 50, 40, 50
	if axis == "y" {
		cols, rows, size = 120, 14, 14
	}
	b.send(map[string]any{"cmd": "resize", "cols": cols, "rows": rows})
	cur := int(float64(size) * 0.3)
	b.waitState(func(s *wireState) bool {
		tr := s.tree()
		if tr == nil {
			return false
		}
		_, e := along(tr.Rect, axis)
		_, a := along(leaf(tr, ids[0]).Rect, axis)
		return e == size && a == cur
	}, fmt.Sprintf("the root %d cells long with the first pane at %d", size, cur))

	// One cell up from where it is. The minimum is further, and the line
	// must not jump there.
	step := (float64(cur+1) + 0.5) / float64(size)
	r := b.mustCall(map[string]any{"cmd": "layout", "op": "set-ratio", "split": root.ID, "ratio": step})
	if r.Ratio != step {
		t.Errorf("set-ratio one cell up from %d came back as %v, want %v as asked", cur, r.Ratio, step)
	}
	b.waitState(func(s *wireState) bool {
		_, a := along(leaf(s.tree(), ids[0]).Rect, axis)
		return a == cur+1
	}, fmt.Sprintf("the first pane at %d cells", cur+1))

	// Smaller than it is now: held at the current line.
	r = b.mustCall(map[string]any{"cmd": "layout", "op": "set-ratio", "split": root.ID, "ratio": 0.05})
	if got := int(float64(size) * r.Ratio); got != cur+1 {
		t.Errorf("a shrink came back as %v (line %d); want it held at the current line %d", r.Ratio, got, cur+1)
	}
}

// TestGUIBridgeZoomKeepsHiddenPanes: a zoom from the bridge resizes the zoomed
// pane only, whatever zoom_size the config sets. Below 100, the terminal
// client's zoom is a camera over the whole layout that resizes every pane, and
// the bridge used to run it too. A terminal client on the same session then saw
// every hidden pane reflow. Each pane runs a program that prints a number when
// its PTY is resized: the zoomed pane prints its own, the hidden panes none.
func TestGUIBridgeZoomKeepsHiddenPanes(t *testing.T) {
	base := t.TempDir()
	pinPreV080Looks(t, base)
	// The bridge reads this file: a zoom of 95 % of the screen.
	cfg := filepath.Join(xdgDir(base, "XDG_CONFIG_HOME"), "tuios", "config.toml")
	data, err := os.ReadFile(cfg)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	if !strings.Contains(string(data), "zoom_size = 100") {
		t.Fatalf("the pinned config does not set zoom_size = 100; this edit assumes it does")
	}
	writeConfigAtomically(t, cfg, []byte(strings.Replace(string(data), "zoom_size = 100", "zoom_size = 95", 1)))
	// The terminal client reads its own, with a zoom of the whole screen, so
	// any resize of a hidden pane comes from the bridge.
	termHome := filepath.Join(base, "term-config")
	pinPreV080LooksIn(t, base, termHome)

	b := startBridge(t, base, "gb", 120, 40)
	ids := b.tiledPanes(3)
	term := attachIn(t, base, "gb", startOpts{cols: 120, rows: 40, env: []string{"XDG_CONFIG_HOME=" + termHome}})
	// The attach can resize the panes once. Wait for that to pass before
	// the programs start listening.
	time.Sleep(time.Second)
	st := b.waitState(func(*wireState) bool { return true }, "a state")
	for i, id := range ids {
		w := st.window(id)
		// Pane i prints SIG4i on a resize. The command line shows the sum
		// unworked, so only a resize puts the number on screen.
		b.input(w.PTY, fmt.Sprintf("clear; sh -c 'trap \"echo SIG\\$((40+%d))\" WINCH; while :; do sleep 0.2; done'\r", i))
	}
	time.Sleep(time.Second)
	b.send(map[string]any{"cmd": "focus", "window": ids[0]})
	b.waitState(func(s *wireState) bool { return s.Focused == ids[0] }, "focus on the first pane")
	b.mustCall(map[string]any{"cmd": "action", "name": "toggle_zoom"})
	b.waitState(func(s *wireState) bool { return s.Zoomed == ids[0] }, "the first pane zoomed")
	if err := term.WaitForText("SIG40", shellTimeout); err != nil {
		t.Fatalf("the zoomed pane never saw its resize: %v\n%s", err, term.Snapshot())
	}
	saveFrame(t, term, "gui-bridge-zoom-zoomed")
	time.Sleep(time.Second)
	b.mustCall(map[string]any{"cmd": "action", "name": "toggle_zoom"})
	b.waitState(func(s *wireState) bool { return s.Zoomed == "" }, "no pane zoomed")
	// The hidden panes' programs wake every 200 ms. A second is five of them.
	time.Sleep(1500 * time.Millisecond)
	snap := term.Snapshot()
	saveFrame(t, term, "gui-bridge-zoom-after")
	for i, name := range []string{"SIG41", "SIG42"} {
		if strings.Contains(snap, name) {
			t.Errorf("hidden pane %c was resized by the zoom (it printed %s):\n%s", 'B'+i, name, snap)
		}
	}
}
