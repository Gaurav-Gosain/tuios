package learn

import (
	"bytes"
	"cmp"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"time"

	tea "charm.land/bubbletea/v2"
	"tuios.dev/tml"

	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/Gaurav-Gosain/tuios/internal/webshell"
)

// TML surfaces in the tour. On a real machine the daemon holds a surface
// (session/tml_surface.go): the tree, the state store and the versions,
// and it answers the client's tml-input, tml-get and tml-place. The tab has
// no daemon, so surfaceHost does that part here, by the same rules: a
// surface a pane opens does not take the keys, its window is named after
// that pane, a person's close answers a waiting tuios tml show with the
// value, and esc or a closed window answers it with null. The client half
// is the app's own (app/tml.go), unchanged.
//
// The fake shell's tuios tml show asks for a surface through
// webshell.SetSurfaceHost. A surface opening, changing and closing is
// reported to the page as tml.open, tml.input, tml.place and tml.close.

// The tml events. See cmd/tuios-wasm/README.md.
const (
	EventTMLOpen  = "tml.open"
	EventTMLInput = "tml.input"
	EventTMLPlace = "tml.place"
	EventTMLClose = "tml.close"
)

// The reasons a surface closed, as session names them.
const (
	tmlClosed    = session.TMLClosedByPerson
	tmlDismissed = session.TMLDismissed
)

// surfaceAnswer is how long tuios tml show waits for the tab to open a
// surface.
const surfaceAnswer = 5 * time.Second

// tmlSurface is one open surface.
type tmlSurface struct {
	id, windowID, title, owner, caller string
	target                             string
	docJSON                            []byte
	store                              *tml.Store
	limits                             tml.Limits
	version, docVersion                uint64
	done                               chan webshell.SurfaceResult
}

// surfaceHost holds the tab's surfaces.
type surfaceHost struct {
	m        *Model
	mu       sync.Mutex
	surfaces map[string]*tmlSurface
}

// openSurfaceMsg asks the program's goroutine to open a surface.
type openSurfaceMsg struct {
	req   webshell.SurfaceRequest
	reply chan openSurfaceReply
}

type openSurfaceReply struct {
	opened webshell.SurfaceOpened
	err    error
}

// closeSurfaceWindowMsg closes the window of a surface that closed.
type closeSurfaceWindowMsg struct{ id string }

// request runs on the shell's goroutine: it hands the request to the
// program and waits for the window.
func (h *surfaceHost) request(req webshell.SurfaceRequest) (webshell.SurfaceOpened, error) {
	reply := make(chan openSurfaceReply, 1)
	h.m.send(openSurfaceMsg{req: req, reply: reply})
	select {
	case r := <-reply:
		return r.opened, r.err
	case <-time.After(surfaceAnswer):
		return webshell.SurfaceOpened{}, errors.New("tuios did not open the surface")
	}
}

// open opens a surface and its window. It runs on the program's goroutine.
func (h *surfaceHost) open(req webshell.SurfaceRequest) (webshell.SurfaceOpened, error) {
	o := h.m.OS
	// A pane holds the limits of a program, as the daemon gives a caller in
	// a pane.
	lim := tml.DefaultLimits()
	doc, _, err := tml.Check(req.Doc, &tml.ValidateOptions{Limits: &lim})
	if err != nil {
		return webshell.SurfaceOpened{}, err
	}
	store, err := tml.StoreFor(doc, &lim)
	if err != nil {
		return webshell.SurfaceOpened{}, err
	}
	s := &tmlSurface{
		id:      newSurfaceID(),
		title:   cmp.Or(tml.Sanitize(req.Name), doc.AttrString("title"), "TML"),
		owner:   h.ownerLabel(req.Window),
		caller:  req.Window,
		target:  session.TMLTargetPopup,
		docJSON: tml.FormatJSON(doc, false),
		store:   store, limits: lim,
		version: 1, docVersion: 1,
		done: make(chan webshell.SurfaceResult, 1),
	}
	if req.Pane {
		s.target = session.TMLTargetPane
	}
	h.mu.Lock()
	if h.surfaces == nil {
		h.surfaces = map[string]*tmlSurface{}
	}
	// Registered before the window opens, as the daemon does, so the
	// window never draws a surface the host does not know.
	h.surfaces[s.id] = s
	h.mu.Unlock()
	wid := o.OpenLearnTMLWindow(app.LearnTMLWindow{
		Surface: s.id,
		Name:    "from " + s.owner + " · " + labelText(s.title, 48),
		Popup:   !req.Pane,
		Width:   req.Width, Height: req.Height,
		// The person's own surface takes the keys. In the tab every
		// surface comes from a pane, which leaves them where they are.
		Focus: req.Window == "",
	})
	if wid == "" {
		h.mu.Lock()
		delete(h.surfaces, s.id)
		h.mu.Unlock()
		return webshell.SurfaceOpened{}, errors.New("the window did not open")
	}
	h.mu.Lock()
	s.windowID = wid
	p := s.payload(true, nil)
	h.mu.Unlock()
	o.LearnPushTMLSurface(p)
	h.m.emit(Event{Type: EventTMLOpen, WindowID: wid, Data: map[string]any{
		"surface": s.id, "windowId": wid, "title": s.title, "owner": s.owner, "target": s.target,
	}})
	return webshell.SurfaceOpened{Surface: s.id, WindowID: wid, Target: s.target, Workspace: o.CurrentWorkspace, Done: s.done}, nil
}

// ownerLabel names the pane that opened a surface, as the daemon does for
// a pane: its name when no other window has it, else its id.
func (h *surfaceHost) ownerLabel(window string) string {
	if window == "" {
		return "tuios tml show"
	}
	name := ""
	for _, w := range h.m.OS.Windows {
		if w != nil && w.ID == window {
			name = labelText(w.CustomName, 24)
		}
	}
	if name != "" && !strings.HasPrefix(name, "from ") {
		for _, w := range h.m.OS.Windows {
			if w != nil && w.ID != window && labelText(w.CustomName, 24) == name {
				name = ""
				break
			}
		}
	}
	if name != "" {
		return name
	}
	return "pane " + shortID(window)
}

func shortID(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

// labelText makes text safe for a window name, as session.tmlLabelText
// does: no control characters, no separator dot or brackets, at most max
// runes.
func labelText(text string, max int) string {
	text = strings.Join(strings.Fields(tml.Sanitize(text)), " ")
	text = strings.NewReplacer("·", "-", "(", "[", ")", "]").Replace(text)
	if r := []rune(text); len(r) > max {
		text = string(r[:max-1]) + "…"
	}
	return text
}

func newSurfaceID() string {
	var b [4]byte
	_, _ = rand.Read(b[:])
	return "tml-" + hex.EncodeToString(b[:])
}

// payload is the push for s, as the daemon builds it. The caller holds the
// host's lock.
func (s *tmlSurface) payload(withDoc bool, ops []byte) *session.TMLSurfacePayload {
	p := &session.TMLSurfacePayload{Surface: s.id, WindowID: s.windowID, Title: s.title, Owner: s.owner, Caller: s.caller,
		Version: s.version, DocVersion: s.docVersion}
	if withDoc {
		p.Doc = s.docJSON
	}
	if ops != nil && !withDoc {
		p.Ops = ops
		return p
	}
	p.State, _ = json.Marshal(s.store.State())
	return p
}

// call answers the client half's TML verbs in place of the daemon. It runs
// off the program's goroutine.
func (h *surfaceHost) call(verb string, params map[string]any, _ time.Duration) (json.RawMessage, error) {
	raw, err := json.Marshal(params)
	if err != nil {
		return nil, err
	}
	switch verb {
	case "tml-input":
		return h.input(raw)
	case "tml-get":
		return h.get(raw)
	case "tml-place":
		return h.place(raw)
	}
	return nil, errors.New(verb + " is not in the browser demo")
}

func (h *surfaceHost) surface(id string) (*tmlSurface, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	s := h.surfaces[id]
	if s == nil {
		return nil, errors.New("no open surface " + id)
	}
	return s, nil
}

// input is tml-input: the state the person's actions changed, and a close.
// A pane() action is refused: the tab has no panes to run it in.
func (h *surfaceHost) input(raw []byte) (json.RawMessage, error) {
	var p struct {
		Surface string          `json:"surface"`
		Ops     json.RawMessage `json:"ops"`
		Close   bool            `json:"close"`
		Value   json.RawMessage `json:"value"`
		Actions []struct {
			Verb string `json:"verb"`
		} `json:"actions"`
	}
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, err
	}
	s, err := h.surface(p.Surface)
	if err != nil {
		return nil, err
	}
	if ops := bytes.TrimSpace(p.Ops); len(ops) > 0 && string(ops) != "null" {
		h.mu.Lock()
		parsed, err := tml.DecodePatch(ops, &s.limits)
		if err == nil {
			_, err = s.store.Apply(parsed)
		}
		if err != nil {
			h.mu.Unlock()
			return nil, err
		}
		s.version++
		push := s.payload(false, ops)
		state, _ := json.Marshal(s.store.State())
		h.mu.Unlock()
		h.m.OS.LearnPushTMLSurface(push)
		h.m.emit(Event{Type: EventTMLInput, WindowID: s.windowID, Data: map[string]any{
			"surface": s.id, "state": decodeObject(state),
		}})
	}
	var results []map[string]any
	for _, a := range p.Actions {
		results = append(results, map[string]any{"verb": a.Verb, "ok": false, "error": "the browser demo has no panes to run it in"})
	}
	if p.Close {
		value := json.RawMessage("null")
		reason := tmlDismissed
		if v := bytes.TrimSpace(p.Value); len(v) > 0 {
			var decoded any
			if err := json.Unmarshal(v, &decoded); err != nil {
				return nil, err
			}
			value, _ = json.Marshal(tml.SanitizeValue(decoded))
			reason = tmlClosed
		}
		h.close(s.id, reason, value)
	}
	h.mu.Lock()
	v := s.version
	h.mu.Unlock()
	res := map[string]any{"type": "tml_input", "surface": s.id, "version": v}
	if len(results) > 0 {
		res["actions"] = results
	}
	return json.Marshal(res)
}

// get is tml-get: the surface whole, for a client that missed a version.
func (h *surfaceHost) get(raw []byte) (json.RawMessage, error) {
	var p struct {
		Surface string `json:"surface"`
	}
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, err
	}
	s, err := h.surface(p.Surface)
	if err != nil {
		return nil, err
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	state, _ := json.Marshal(s.store.State())
	return json.Marshal(map[string]any{
		"window_id": s.windowID, "title": s.title, "owner": s.owner,
		"version": s.version, "doc_version": s.docVersion,
		"doc": json.RawMessage(s.docJSON), "state": json.RawMessage(state),
	})
}

// place is tml-place: the person moved a surface between a popup and a
// pane. The client lays the window out itself.
func (h *surfaceHost) place(raw []byte) (json.RawMessage, error) {
	var p struct {
		Surface string `json:"surface"`
		Target  string `json:"target"`
	}
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, err
	}
	s, err := h.surface(p.Surface)
	if err != nil {
		return nil, err
	}
	if p.Target != session.TMLTargetPopup && p.Target != session.TMLTargetPane {
		return nil, errors.New("unknown target " + p.Target)
	}
	h.mu.Lock()
	s.target = p.Target
	h.mu.Unlock()
	h.m.emit(Event{Type: EventTMLPlace, WindowID: s.windowID, Data: map[string]any{
		"surface": s.id, "windowId": s.windowID, "target": p.Target,
	}})
	return json.Marshal(map[string]any{"type": "tml_placed", "surface": s.id, "target": p.Target})
}

// close ends a surface: it tells the client to forget it, answers a waiting
// tuios tml show, tells the page, and closes the window. A surface that
// closed already is left alone.
func (h *surfaceHost) close(id, reason string, value json.RawMessage) {
	h.mu.Lock()
	s := h.surfaces[id]
	if s == nil {
		h.mu.Unlock()
		return
	}
	delete(h.surfaces, id)
	gone := &session.TMLSurfacePayload{Surface: s.id, WindowID: s.windowID, Version: s.version, DocVersion: s.docVersion, Closed: true}
	h.mu.Unlock()
	h.m.OS.LearnPushTMLSurface(gone)
	s.done <- webshell.SurfaceResult{Reason: reason, Value: value}
	h.m.emit(Event{Type: EventTMLClose, WindowID: s.windowID, Data: map[string]any{
		"surface": s.id, "windowId": s.windowID, "title": s.title, "reason": reason, "value": decodeAny(value),
	}})
	if reason != tmlDismissed || h.windowOpen(s.windowID) {
		h.m.send(closeSurfaceWindowMsg{id: s.windowID})
	}
}

// windowOpen reports whether a window is still open, from the last
// snapshot, which is safe to read from any goroutine.
func (h *surfaceHost) windowOpen(id string) bool {
	_, ok := h.m.State().window(id)
	return ok
}

// sweep dismisses the surfaces whose window closed some other way: a close
// key, the close button, or a reset. It runs on the program's goroutine,
// after each update.
func (h *surfaceHost) sweep() {
	h.mu.Lock()
	var gone []string
	for id, s := range h.surfaces {
		if s.windowID == "" {
			continue
		}
		found := false
		for _, w := range h.m.OS.Windows {
			if w != nil && w.ID == s.windowID {
				found = true
				break
			}
		}
		if !found {
			gone = append(gone, id)
		}
	}
	h.mu.Unlock()
	for _, id := range gone {
		h.close(id, tmlDismissed, json.RawMessage("null"))
	}
}

// update handles the host's messages on the program's goroutine. ok is
// false for a message that is not the host's.
func (h *surfaceHost) update(msg tea.Msg) (tea.Cmd, bool) {
	switch msg := msg.(type) {
	case openSurfaceMsg:
		opened, err := h.open(msg.req)
		msg.reply <- openSurfaceReply{opened: opened, err: err}
		return nil, true
	case closeSurfaceWindowMsg:
		_ = h.m.OS.CloseWindow(msg.id)
		return nil, true
	}
	return nil, false
}

func decodeObject(raw []byte) map[string]any {
	var out map[string]any
	_ = json.Unmarshal(raw, &out)
	return out
}

func decodeAny(raw []byte) any {
	var out any
	_ = json.Unmarshal(raw, &out)
	return out
}
