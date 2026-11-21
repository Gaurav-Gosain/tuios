package tmuxcompat

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
)

type fakeWindow struct {
	id, name, cwd string
	ws            int
	x, y, w, h    int
	visible       string
	recent        string
	cmd           string
	history       int
	scratch       bool
}

type call struct {
	verb   string
	params map[string]any
}

type fakeDaemon struct {
	mu           sync.Mutex
	name, prefix string
	t            *testing.T
	windows      []*fakeWindow
	current      int
	focused      string
	wsNames      map[int]string
	calls        []call
	nextID       int
	foreign      []string
	failVerb     string
	refuseText   string
	noAdmin      bool
	grants       map[string]any
	grantsErr    error
}

type codedErr struct{ code string }

func (e codedErr) Error() string     { return "verb failed (" + e.code + ")" }
func (e codedErr) ErrorCode() string { return e.code }

func newFake(t *testing.T) *fakeDaemon {
	return &fakeDaemon{
		name:    "work",
		t:       t,
		current: 1,
		wsNames: map[int]string{},
		windows: []*fakeWindow{{id: "leader-0001", name: "claude", cwd: "/src", ws: 1, w: 120, h: 40, visible: "leader\n"}},
		focused: "leader-0001",
	}
}

func (f *fakeDaemon) with(fn func()) {
	f.mu.Lock()
	defer f.mu.Unlock()
	fn()
}

func (f *fakeDaemon) Call(verb string, params any) (json.RawMessage, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	raw, _ := json.Marshal(params)
	var p map[string]any
	_ = json.Unmarshal(raw, &p)
	f.calls = append(f.calls, call{verb: verb, params: p})
	if verb == "pane-grants" {
		if f.grantsErr != nil {
			return nil, f.grantsErr
		}
		if f.grants == nil {
			return json.Marshal(map[string]any{"pane": false})
		}
		return json.Marshal(f.grants)
	}
	switch verb {
	case "list-buffers", "show-buffer", "set-buffer", "delete-buffer":
		return nil, codedErr{code: "unknown_verb"}
	}
	if verb == "list-sessions" {
		if f.noAdmin {
			return nil, codedErr{code: "forbidden"}
		}
		return json.Marshal(map[string]any{"sessions": []map[string]any{f.listing()}})
	}
	if verb == "get-option" {
		return json.Marshal(map[string]any{"type": "option", "key": p["key"], "value": 5000})
	}
	if s, _ := p["session"].(string); s != f.name {
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
				"foreground_cmd": w.cmd, "history_rows": w.history, "scratch": w.scratch,
			})
		}
		out = map[string]any{"windows": rows, "focused_window_id": f.focused, "current_workspace": f.current}
	case "session-info":
		out = map[string]any{"session_name": f.name, "session_id": "sess-" + f.name, "width": 120, "height": 40, "tui_attached": true}
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
		for _, w := range f.windows {
			if w.scratch {
				rows = append(rows, map[string]any{"workspace": w.ws, "name": "scratch", "window_count": 1})
			}
		}
		out = map[string]any{"workspaces": rows, "current_workspace": f.current}
	case "new-window":
		f.nextID++
		ws := int(p["workspace"].(float64))
		w := &fakeWindow{id: fmt.Sprintf("%snew-%04d", f.prefix, f.nextID), ws: ws, cwd: str("cwd"), w: 60, h: 40, x: 60}
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
	case "send-text", "send-keys":
		if _, err := win(); err != nil {
			return nil, err
		}
		if f.refuseText != "" {
			return nil, codedErr{code: f.refuseText}
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

func (f *fakeDaemon) listing() map[string]any {
	return map[string]any{
		"name": f.name, "id": "sess-" + f.name, "created": 1700000000, "last_active": 1700000500,
		"attached": f.name == "work", "width": 120, "height": 40,
	}
}

type multiFake struct {
	mu       sync.Mutex
	t        *testing.T
	sessions []*fakeDaemon
	calls    []call
}

func newMultiFake(t *testing.T, names ...string) *multiFake {
	m := &multiFake{t: t}
	for i, n := range names {
		f := newFake(t)
		f.name = n
		if i > 0 {
			f.prefix = n + "-"
			f.windows = []*fakeWindow{{id: n + "-main-0001", name: n + "-sh", cwd: "/home/" + n, ws: 1, w: 80, h: 24, visible: n + "\n"}}
			f.focused = n + "-main-0001"
		}
		m.sessions = append(m.sessions, f)
	}
	return m
}

func (m *multiFake) session(name string) *fakeDaemon {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, f := range m.sessions {
		if f.name == name {
			return f
		}
	}
	return nil
}

func (m *multiFake) Call(verb string, params any) (json.RawMessage, error) {
	raw, _ := json.Marshal(params)
	var p map[string]any
	_ = json.Unmarshal(raw, &p)
	m.mu.Lock()
	m.calls = append(m.calls, call{verb: verb, params: p})
	m.mu.Unlock()
	switch verb {
	case "list-sessions":
		m.mu.Lock()
		defer m.mu.Unlock()
		var rows []map[string]any
		for _, f := range m.sessions {
			f.mu.Lock()
			rows = append(rows, f.listing())
			f.mu.Unlock()
		}
		return json.Marshal(map[string]any{"sessions": rows})
	case "new-session":
		name, _ := p["name"].(string)
		if m.session(name) != nil {
			return nil, codedErr{code: "session_exists"}
		}
		f := newFake(m.t)
		f.name, f.prefix = name, name+"-"
		cwd, _ := p["cwd"].(string)
		f.windows = []*fakeWindow{{id: name + "-first-0001", name: "sh", cwd: cwd, ws: 1, w: 80, h: 24}}
		f.focused = f.windows[0].id
		m.mu.Lock()
		m.sessions = append(m.sessions, f)
		m.mu.Unlock()
		return json.Marshal(map[string]any{"session": name, "window_id": f.windows[0].id})
	}
	name, _ := p["session"].(string)
	f := m.session(name)
	if f == nil {
		return nil, codedErr{code: "session_not_found"}
	}
	return f.Call(verb, params)
}

func (f *fakeDaemon) verbs() []string {
	var out []string
	for _, c := range f.calls {
		out = append(out, c.verb)
	}
	return out
}

func (f *fakeDaemon) last(verb string) map[string]any {
	f.t.Helper()
	for _, v := range slices.Backward(f.calls) {
		if v.verb == verb {
			return v.params
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

func (h *harness) run(args ...string) (int, string) {
	h.out.Reset()
	h.err.Reset()
	code := h.shim.Run(args)
	return code, h.out.String()
}

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
	for line := range strings.SplitSeq(strings.TrimSpace(string(data)), "\n") {
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
