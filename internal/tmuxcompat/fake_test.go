package tmuxcompat

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeWindow is one window of the fake daemon's session.
type fakeWindow struct {
	id, name, cwd string
	ws            int
	x, y, w, h    int
	visible       string
	recent        string
}

// call is one verb call the fake received.
type call struct {
	verb   string
	params map[string]any
}

// fakeDaemon answers the verbs the shim calls against one session, "work",
// and records every call. A call naming any other session fails, which is
// what lets a test see that nothing left the caller's session.
type fakeDaemon struct {
	t        *testing.T
	windows  []*fakeWindow
	current  int
	focused  string
	wsNames  map[int]string
	calls    []call
	nextID   int
	foreign  []string // sessions named in calls other than "work"
	failVerb string
}

func newFake(t *testing.T) *fakeDaemon {
	return &fakeDaemon{
		t:       t,
		current: 1,
		wsNames: map[int]string{},
		windows: []*fakeWindow{{id: "leader-0001", name: "claude", cwd: "/src", ws: 1, w: 120, h: 40, visible: "leader\n"}},
		focused: "leader-0001",
	}
}

func (f *fakeDaemon) Call(verb string, params any) (json.RawMessage, error) {
	raw, _ := json.Marshal(params)
	var p map[string]any
	_ = json.Unmarshal(raw, &p)
	f.calls = append(f.calls, call{verb: verb, params: p})
	if s, _ := p["session"].(string); s != "work" {
		f.foreign = append(f.foreign, s)
		return nil, fmt.Errorf("session %q not found", s)
	}
	if verb == f.failVerb {
		return nil, fmt.Errorf("%s failed", verb)
	}
	str := func(k string) string { s, _ := p[k].(string); return s }
	win := func() (*fakeWindow, error) {
		for _, w := range f.windows {
			if w.id == str("window") {
				return w, nil
			}
		}
		return nil, fmt.Errorf("window %q not found", str("window"))
	}
	var out any
	switch verb {
	case "list-windows":
		var rows []map[string]any
		for _, w := range f.windows {
			rows = append(rows, map[string]any{
				"window_id": w.id, "display_name": w.name, "workspace": w.ws, "cwd": w.cwd,
				"x": w.x, "y": w.y, "width": w.w, "height": w.h,
			})
		}
		out = map[string]any{"windows": rows, "focused_window_id": f.focused, "current_workspace": f.current}
	case "list-workspaces":
		var rows []map[string]any
		for ws := 1; ws <= 9; ws++ {
			n := 0
			for _, w := range f.windows {
				if w.ws == ws {
					n++
				}
			}
			rows = append(rows, map[string]any{"workspace": ws, "name": f.wsNames[ws], "window_count": n})
		}
		out = map[string]any{"workspaces": rows, "current_workspace": f.current}
	case "new-window":
		f.nextID++
		ws := int(p["workspace"].(float64))
		w := &fakeWindow{id: fmt.Sprintf("new-%04d", f.nextID), ws: ws, cwd: str("cwd"), w: 60, h: 40, x: 60}
		f.windows = append(f.windows, w)
		if focus, _ := p["focus"].(bool); focus {
			f.focused = w.id
			f.current = ws
		}
		out = map[string]any{"window_id": w.id, "workspace": ws}
	case "close-window":
		w, err := win()
		if err != nil {
			return nil, err
		}
		for i, x := range f.windows {
			if x == w {
				f.windows = append(f.windows[:i], f.windows[i+1:]...)
				break
			}
		}
		out = map[string]any{}
	case "set-window":
		w, err := win()
		if err != nil {
			return nil, err
		}
		w.name = str("name")
		out = map[string]any{}
	case "send-text":
		if _, err := win(); err != nil {
			return nil, err
		}
		out = map[string]any{}
	case "capture-pane":
		w, err := win()
		if err != nil {
			return nil, err
		}
		content := w.visible
		if str("source") == "recent" {
			content = w.recent
		}
		out = map[string]any{"content": content}
	case "focus-window":
		if str("window") != "" {
			w, err := win()
			if err != nil {
				return nil, err
			}
			f.focused = w.id
		}
		out = map[string]any{}
	case "select-workspace":
		f.current = int(p["workspace"].(float64))
		out = map[string]any{}
	case "set-workspace-name":
		f.wsNames[int(p["workspace"].(float64))] = str("name")
		out = map[string]any{}
	default:
		return nil, fmt.Errorf("the fake daemon has no verb %s", verb)
	}
	return json.Marshal(out)
}

// verbs lists the verbs called, in order.
func (f *fakeDaemon) verbs() []string {
	var out []string
	for _, c := range f.calls {
		out = append(out, c.verb)
	}
	return out
}

// last returns the last call of verb, failing the test when there is none.
func (f *fakeDaemon) last(verb string) map[string]any {
	f.t.Helper()
	for i := len(f.calls) - 1; i >= 0; i-- {
		if f.calls[i].verb == verb {
			return f.calls[i].params
		}
	}
	f.t.Fatalf("no %s call; calls were %v", verb, f.verbs())
	return nil
}

func (f *fakeDaemon) called(verb string) bool {
	for _, c := range f.calls {
		if c.verb == verb {
			return true
		}
	}
	return false
}

// harness is a shim wired to a fake daemon, with its output captured.
type harness struct {
	shim     *Shim
	fake     *fakeDaemon
	out, err *bytes.Buffer
	respawns []RespawnRequest
	logPath  string
}

func newHarness(t *testing.T) *harness {
	f := newFake(t)
	h := &harness{fake: f, out: &bytes.Buffer{}, err: &bytes.Buffer{}}
	h.logPath = filepath.Join(t.TempDir(), "shim.log")
	h.shim = &Shim{
		Caller:    f,
		Session:   "work",
		Window:    "leader-0001",
		TmuxPane:  PaneID("leader-0001"),
		Cwd:       "/src",
		Exe:       "/opt/tuios",
		Dir:       "/run/tuios/tmux",
		ServerPID: 42,
		Stdout:    h.out,
		Stderr:    h.err,
		Log:       &Logger{Path: h.logPath},
	}
	h.shim.respawn = func(dir, id string, req RespawnRequest) error {
		if dir != h.shim.Dir {
			t.Errorf("respawn went to %q, want the shim's dir", dir)
		}
		if id != "new-0001" && id != "leader-0001" {
			return fmt.Errorf("no holder for %s", id)
		}
		h.respawns = append(h.respawns, req)
		return nil
	}
	return h
}

// run runs one tmux argv and returns its status and stdout, clearing both
// buffers.
func (h *harness) run(args ...string) (int, string) {
	h.out.Reset()
	h.err.Reset()
	code := h.shim.Run(args)
	return code, h.out.String()
}

// logEntries reads the log.
func (h *harness) logEntries(t *testing.T) []LogEntry {
	t.Helper()
	data, err := os.ReadFile(h.logPath)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	var out []LogEntry
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if line == "" {
			continue
		}
		var e LogEntry
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			t.Fatalf("log line %q: %v", line, err)
		}
		out = append(out, e)
	}
	return out
}
