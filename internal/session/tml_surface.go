package session

import (
	"bytes"
	"cmp"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"tuios.dev/tml"
)

// TML surfaces: a document a program, a script or an agent sends, which every
// attached client draws with tuios.dev/loom. See docs/protocol.md, "TML
// surfaces", and the design in the notes (TML-DESIGN.md, section 5).
//
// The daemon holds the surface: the tree, the state store, a version and the
// window it is drawn in. That is what lets it survive a detach, and what lets
// two clients and a phone see the same values. Each client keeps its own view
// state (focus, the cursor row, the open tab, sort and scroll), as each keeps
// its own zoom box.
//
// The window is an ordinary window of kind tml, the native-renderer contract
// the view, web and app kinds already have (WindowState.Kind): it still has a
// PTY, whose program prints one line for a terminal client that does not draw
// TML. Opening, closing, focus, popups, tiling and listing all go through the
// paths every window takes. The placeholder turns on the alternate screen and
// mouse reporting, so a client sends the wheel and clicks to the pane, where
// its TML code takes them, instead of scrolling the pane's history.
//
// A surface does not survive a daemon restart: resurrection drops its window,
// as it drops a popup.

// WindowKindTML is the kind of a window that draws a TML surface.
const WindowKindTML = "tml"

// TMLURIPrefix starts the URI of a TML window. The surface id follows it.
const TMLURIPrefix = "tuios://tml/"

// The error codes of the TML verbs.
const (
	// ErrVerbInvalidTML is a document that does not parse or validate. The
	// error carries the problem list.
	ErrVerbInvalidTML = "invalid_tml"
	// ErrVerbNoSurface is a surface id that names no open surface.
	ErrVerbNoSurface = "no_surface"
	// ErrVerbTooManySurfaces is a tml-open over the caller's cap of open
	// surfaces.
	ErrVerbTooManySurfaces = "too_many_surfaces"
)

// The reasons a surface closed, in tml_result.
const (
	// TMLClosedByPerson is a close() action the person ran: the value is
	// the person's answer.
	TMLClosedByPerson = "closed"
	// TMLDismissed is the window closed without a value: esc, close-window,
	// or the session ended.
	TMLDismissed = "dismissed"
	// TMLClosedByProgram is a tml-close: a program gave the value, not the
	// person.
	TMLClosedByProgram = "program"
)

// TML targets tml-open takes.
const (
	TMLTargetPopup = "popup"
	TMLTargetPane  = "pane"
)

var tmlTargets = []string{TMLTargetPopup, TMLTargetPane}

// TMLSurfacePayload is the body of MsgTMLSurface: one surface as the daemon
// holds it, pushed to every attached client that draws TML when it opens,
// when it changes and when the client attaches. Doc is the tree as canonical
// TML JSON, and State the state store as JSON; a client puts the state into
// the tree's root before it draws. Closed tells the client to forget the
// surface.
type TMLSurfacePayload struct {
	Surface  string
	WindowID string
	Title    string
	// Owner names who opened the surface, for the frame.
	Owner string
	// Version counts every change, DocVersion only the changes to the tree.
	Version    uint64
	DocVersion uint64
	Doc        []byte
	State      []byte
	Closed     bool
}

// TMLSurfaceID returns the surface a TML window's URI names, or "".
func TMLSurfaceID(uri string) string {
	id, ok := strings.CutPrefix(uri, TMLURIPrefix)
	if !ok {
		return ""
	}
	return id
}

// tmlPlaceholderCommand is the program of a TML window. It prints one line
// for a terminal client that does not draw TML, and holds the alternate
// screen and button-event mouse reporting (SGR) while it waits, so a client
// that does draw TML sends the wheel and clicks to the pane.
func tmlPlaceholderCommand(uri string) []string {
	return []string{"/bin/sh", "-c",
		`stty -echo 2>/dev/null; printf '\033[?1049h\033[?1002h\033[?1006h\033[2m%s\033[0m\n%s\n' "$1" "$2"; exec sleep 2147483647`,
		"tuios-tml", "This pane shows a TML surface. A newer tuios client draws it.", uri}
}

// tmlSurface is one open surface.
type tmlSurface struct {
	id        string
	sessionID string
	windowID  string
	title     string
	owner     string
	// caller keys the surface cap: the opening pane, or "" for a caller in
	// no pane.
	caller string

	doc     *tml.Node
	docJSON []byte
	store   *tml.Store
	limits  tml.Limits

	version    uint64
	docVersion uint64

	closed   bool
	value    any
	hasValue bool
	reason   string
	done     chan struct{}
}

// tmlHost holds the open surfaces of the daemon. The zero value is ready.
type tmlHost struct {
	mu       sync.Mutex
	surfaces map[string]*tmlSurface
}

// payload builds the push for s. The caller holds the host's lock.
func (s *tmlSurface) payload() *TMLSurfacePayload {
	p := &TMLSurfacePayload{Surface: s.id, WindowID: s.windowID, Title: s.title, Owner: s.owner,
		Version: s.version, DocVersion: s.docVersion, Closed: s.closed}
	if s.closed {
		return p
	}
	p.Doc = s.docJSON
	p.State, _ = json.Marshal(s.store.State())
	return p
}

// newTMLSurfaceID returns a short random id, such as tml-4f1a9c02.
func newTMLSurfaceID() string {
	var b [4]byte
	_, _ = rand.Read(b[:])
	return "tml-" + hex.EncodeToString(b[:])
}

// tmlDocSource reads the doc parameter: a JSON string holds TML text or TML
// JSON, and a JSON object is TML JSON.
func tmlDocSource(raw json.RawMessage) ([]byte, *verbError) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || string(raw) == "null" {
		return nil, invalidParam("doc", "doc is required: pass the TML document as text or as TML JSON")
	}
	if raw[0] == '"' {
		var s string
		if err := json.Unmarshal(raw, &s); err != nil {
			return nil, invalidParam("doc", "doc is not a valid JSON string: "+err.Error())
		}
		return []byte(s), nil
	}
	if raw[0] != '{' {
		return nil, invalidParam("doc", "doc is a string of TML text or JSON, or a TML JSON object")
	}
	return raw, nil
}

// tmlInvalid is the invalid_tml error for a document's problems.
func tmlInvalid(problems []tml.Problem, err error) *verbError {
	msg := err.Error()
	var terr *tml.Error
	if errors.As(err, &terr) {
		problems = terr.Problems
	}
	e := hintedVerbError(ErrVerbInvalidTML, msg, &VerbHint{
		Command: "tuios tml validate <file>",
		Detail:  "Each problem names the node with a JSON Pointer and the fix. Correct the document and send it again.",
	})
	if len(problems) > 0 {
		e.Problems = problems
	}
	return e
}

// tmlLimitsFor is what a document from cs may hold: the person's limits for
// a caller in no pane, and the default limits for any pane, whatever it
// holds. An agent runs in a pane.
func (d *Daemon) tmlLimitsFor(cs *connState) tml.Limits {
	if d.paneAuthority(cs) == nil {
		return tml.PersonLimits()
	}
	return tml.DefaultLimits()
}

// Open surfaces a caller may hold at once: a pane, and every caller outside
// the panes together, per session. A surface is a window, a PTY and a
// process, so a loop that opens them must stop.
const (
	tmlSurfacesPerPane    = 8
	tmlSurfacesPerSession = 32
)

// tmlCallerKey names the caller on cs for the surface cap: its pane, or ""
// for a caller in no pane.
func (d *Daemon) tmlCallerKey(cs *connState) string {
	if ref := d.callerPaneRef(cs); ref != nil {
		return ref.Window
	}
	return ""
}

// tmlOwnerLabel names the caller on cs for the frame: its pane, or the CLI.
func (d *Daemon) tmlOwnerLabel(cs *connState) string {
	if ref := d.callerPaneRef(cs); ref != nil {
		if sess := d.sessionHoldingWindow(ref.Window); sess != nil {
			for _, w := range sess.GetState().Windows {
				if w.ID == ref.Window {
					return "pane " + cmp.Or(w.CustomName, w.Title, shortID(w.ID))
				}
			}
		}
		return "pane " + shortID(ref.Window)
	}
	return "tuios tml show"
}

// verbTMLOpen opens a surface as a popup or a pane.
func (d *Daemon) verbTMLOpen(cs *connState, params json.RawMessage) (any, *verbError) {
	var p struct {
		Session   string          `json:"session"`
		Doc       json.RawMessage `json:"doc"`
		Target    string          `json:"target"`
		Name      string          `json:"name"`
		Width     string          `json:"width"`
		Height    string          `json:"height"`
		Workspace int             `json:"workspace"`
		Wait      bool            `json:"wait"`
		Timeout   int             `json:"timeout"`
	}
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	src, verr := tmlDocSource(p.Doc)
	if verr != nil {
		return nil, verr
	}
	target := cmp.Or(p.Target, TMLTargetPopup)
	if target != TMLTargetPopup && target != TMLTargetPane {
		return nil, invalidParam("target", "unknown target "+echoName(p.Target), tmlTargets...)
	}
	if p.Timeout < 0 {
		return nil, invalidParam("timeout", "timeout is milliseconds and cannot be negative")
	}
	if p.Workspace < 0 {
		return nil, invalidParam("workspace", "workspace is a workspace number, e.g. 2. Omit it for the current one")
	}
	if target == TMLTargetPane && (p.Width != "" || p.Height != "") {
		return nil, invalidParam("width", "width and height size a popup. A pane takes its tile")
	}
	if err := ValidatePopupSize(p.Width); err != nil {
		return nil, invalidParam("width", err.Error())
	}
	if err := ValidatePopupSize(p.Height); err != nil {
		return nil, invalidParam("height", err.Error())
	}
	limits := d.tmlLimitsFor(cs)
	doc, problems, err := tml.Check(src, &tml.ValidateOptions{Limits: &limits})
	if err != nil {
		return nil, tmlInvalid(problems, err)
	}
	store, err := tml.StoreFor(doc, &limits)
	if err != nil {
		return nil, tmlInvalid(nil, err)
	}
	sess, verr := d.resolveVerbSession(p.Session)
	if verr != nil {
		return nil, verr
	}
	if target == TMLTargetPopup && !d.hasTUIClient(sess) {
		return nil, hintedVerbError(ErrVerbNeedsClient,
			"a popup is drawn on a screen, so it needs an attached client",
			&VerbHint{
				Command: "tuios attach " + sess.Name(),
				Detail:  "Attach a client and retry, or open the surface as a pane with target pane.",
			})
	}

	// The name is shown on the frame and listed, so it is held to what a
	// document's text is: no control characters.
	title := cmp.Or(tml.Sanitize(p.Name), doc.AttrString("title"), "TML")
	s := &tmlSurface{
		id:        newTMLSurfaceID(),
		sessionID: sess.ID,
		title:     title,
		owner:     d.tmlOwnerLabel(cs),
		doc:       doc,
		docJSON:   tml.FormatJSON(doc, false),
		store:     store,
		limits:    limits,
		version:   1, docVersion: 1,
		done: make(chan struct{}),
	}
	uri := TMLURIPrefix + s.id
	s.caller = d.tmlCallerKey(cs)
	// The surface is registered before the window exists, so the push that
	// follows the window's creation finds it; a client that sees the window
	// first draws it empty until the push lands.
	d.tml.mu.Lock()
	if d.tml.surfaces == nil {
		d.tml.surfaces = map[string]*tmlSurface{}
	}
	limit := tmlSurfacesPerSession
	if s.caller != "" {
		limit = tmlSurfacesPerPane
	}
	open := 0
	for _, o := range d.tml.surfaces {
		if o.caller == s.caller && (s.caller != "" || o.sessionID == s.sessionID) {
			open++
		}
	}
	if open >= limit {
		d.tml.mu.Unlock()
		return nil, hintedVerbError(ErrVerbTooManySurfaces, fmt.Sprintf("%d surfaces are open already, the most one caller may hold", limit), &VerbHint{
			Command: "tuios tml close <surface>",
			Detail:  fmt.Sprintf("A pane holds at most %d open surfaces, and the callers outside the panes %d per session. Close one first.", tmlSurfacesPerPane, tmlSurfacesPerSession),
		})
	}
	d.tml.surfaces[s.id] = s
	d.tml.mu.Unlock()

	onExit := func(ptyID string) {
		d.notifyPTYClosed(sess.ID, ptyID)
		d.tmlWindowGone(s.id)
		d.closeWindowOfPTY(sess, ptyID, false)
	}
	opts := NewWindowOptions{
		Title:       title,
		Name:        title,
		Workspace:   p.Workspace,
		Focus:       true,
		Command:     tmlPlaceholderCommand(uri),
		Popup:       target == TMLTargetPopup,
		PopupWidth:  p.Width,
		PopupHeight: p.Height,
		Kind:        WindowKindTML,
		URI:         uri,
	}
	win, bverr, err := d.addWindowFor(cs, "tml-open", sess, opts, onExit)
	if bverr == nil && err != nil {
		bverr = newWindowErr(err, sess, p.Workspace)
	}
	if bverr != nil {
		d.tml.mu.Lock()
		delete(d.tml.surfaces, s.id)
		d.tml.mu.Unlock()
		return nil, bverr
	}
	d.tml.mu.Lock()
	s.windowID = win.ID
	payload := s.payload()
	d.tml.mu.Unlock()
	d.pushTMLSurface(sess.ID, payload)

	if !p.Wait {
		return map[string]any{
			"type":      "tml_opened",
			"surface":   s.id,
			"window_id": win.ID,
			"target":    target,
			"workspace": win.Workspace,
		}, nil
	}
	var timeout <-chan time.Time
	if p.Timeout > 0 {
		t := time.NewTimer(time.Duration(p.Timeout) * time.Millisecond)
		defer t.Stop()
		timeout = t.C
	}
	select {
	case <-s.done:
	case <-timeout:
		return nil, hintedVerbError(ErrVerbTimeout, "the surface was still open when the wait ended", &VerbHint{
			Command: "tuios tml close " + s.id,
			Detail:  "The surface is still on the screen. Close it with the command shown, or raise timeout.",
		})
	}
	d.tml.mu.Lock()
	res := map[string]any{"type": "tml_result", "surface": s.id, "window_id": win.ID, "reason": s.reason, "value": s.value}
	d.tml.mu.Unlock()
	return res, nil
}

// verbTMLClose closes a surface with a value.
func (d *Daemon) verbTMLClose(_ *connState, params json.RawMessage) (any, *verbError) {
	var p struct {
		Surface string          `json:"surface"`
		Value   json.RawMessage `json:"value"`
	}
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	var value any
	has := len(bytes.TrimSpace(p.Value)) > 0
	if has {
		if err := json.Unmarshal(p.Value, &value); err != nil {
			return nil, invalidParam("value", "value is not JSON: "+err.Error())
		}
		value = tml.SanitizeValue(value)
	}
	if verr := d.tmlClose(p.Surface, value, has, TMLClosedByProgram); verr != nil {
		return nil, verr
	}
	return map[string]any{"type": "tml_closed", "surface": p.Surface}, nil
}

// verbTMLInput is what the person did on a surface, from an attached client:
// the state the surface's local actions changed, and a close with its value.
// It needs the client's attach nonce, since it acts as the person.
func (d *Daemon) verbTMLInput(cs *connState, params json.RawMessage) (any, *verbError) {
	var p struct {
		Surface    string          `json:"surface"`
		HumanNonce string          `json:"human_nonce"`
		Ops        json.RawMessage `json:"ops"`
		Close      bool            `json:"close"`
		Value      json.RawMessage `json:"value"`
	}
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	d.tml.mu.Lock()
	s := d.tml.surfaces[p.Surface]
	var sessionID string
	if s != nil {
		sessionID = s.sessionID
	}
	d.tml.mu.Unlock()
	if s == nil {
		return nil, noSurface(p.Surface)
	}
	if _, ok := d.humanNonceFor(p.HumanNonce, sessionID, cs); !ok {
		return nil, hintedVerbError(ErrVerbNotHuman, "tml-input acts as the person, so it needs the attach nonce of a client", &VerbHint{
			Param:  "human_nonce",
			Detail: "A program changes a surface with tml-patch and closes it with tml-close.",
		})
	}
	if ops := bytes.TrimSpace(p.Ops); len(ops) > 0 && string(ops) != "null" {
		d.tml.mu.Lock()
		parsed, err := tml.DecodePatch(ops, &s.limits)
		if err == nil && !s.closed {
			_, err = s.store.Apply(parsed)
			if err == nil {
				s.version++
			}
		}
		payload := s.payload()
		d.tml.mu.Unlock()
		if err != nil {
			return nil, invalidParam("ops", err.Error())
		}
		d.pushTMLSurface(sessionID, payload)
	}
	if p.Close {
		var value any
		has := len(bytes.TrimSpace(p.Value)) > 0
		if has {
			if err := json.Unmarshal(p.Value, &value); err != nil {
				return nil, invalidParam("value", "value is not JSON: "+err.Error())
			}
			value = tml.SanitizeValue(value)
		}
		reason := TMLClosedByPerson
		if !has {
			reason = TMLDismissed
		}
		if verr := d.tmlClose(p.Surface, value, has, reason); verr != nil {
			return nil, verr
		}
	}
	d.tml.mu.Lock()
	v := s.version
	d.tml.mu.Unlock()
	return map[string]any{"type": "tml_input", "surface": p.Surface, "version": v}, nil
}

func noSurface(id string) *verbError {
	return hintedVerbError(ErrVerbNoSurface, "no open surface "+echoName(id), &VerbHint{
		Param:  "surface",
		Detail: "The surface closed, or the id is wrong. tml-open answers with the id of the surface it opened.",
	})
}

// tmlClose closes a surface: it records the value, tells the clients, ends a
// waiting tml-open and closes the window.
func (d *Daemon) tmlClose(id string, value any, has bool, reason string) *verbError {
	d.tml.mu.Lock()
	s := d.tml.surfaces[id]
	if s == nil || s.closed {
		d.tml.mu.Unlock()
		return noSurface(id)
	}
	s.closed, s.value, s.hasValue, s.reason = true, value, has, reason
	delete(d.tml.surfaces, id)
	payload := s.payload()
	close(s.done)
	sessionID, windowID := s.sessionID, s.windowID
	d.tml.mu.Unlock()
	d.pushTMLSurface(sessionID, payload)
	if sess := d.manager.GetSessionByID(sessionID); sess != nil && windowID != "" {
		d.goTracked(func() { _, _ = sess.CloseDaemonWindow(windowID) })
	}
	return nil
}

// tmlWindowGone ends the surface of a window that closed some other way: by
// hand, with close-window, or because its session ended. A waiting tml-open
// answers with a null value and reason "dismissed".
func (d *Daemon) tmlWindowGone(id string) {
	d.tml.mu.Lock()
	s := d.tml.surfaces[id]
	if s == nil || s.closed {
		d.tml.mu.Unlock()
		return
	}
	s.closed, s.reason = true, TMLDismissed
	delete(d.tml.surfaces, id)
	payload := s.payload()
	close(s.done)
	sessionID := s.sessionID
	d.tml.mu.Unlock()
	d.pushTMLSurface(sessionID, payload)
}

// tmlReader reports whether cs is told about the surfaces of a session: an
// attached TUI client of it whose hello says it draws TML. A passive client,
// such as a session shown in the rail, draws no surface.
func tmlReader(cs *connState, sessionID string) bool {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	return cs.isTUIClient && cs.attached && cs.tmlSurfaces && !cs.passive && cs.sessionID == sessionID
}

// pushTMLSurface sends one surface to every client of its session that draws
// TML.
func (d *Daemon) pushTMLSurface(sessionID string, payload *TMLSurfacePayload) {
	var msg *Message
	d.clientsMu.RLock()
	defer d.clientsMu.RUnlock()
	for _, cs := range d.clients {
		if !tmlReader(cs, sessionID) {
			continue
		}
		if msg == nil {
			var err error
			if msg, err = NewMessage(MsgTMLSurface, payload); err != nil {
				return
			}
		}
		d.queueBroadcast(cs, msg, "pushTMLSurface")
	}
}

// sendTMLSurfaces sends a client that just attached every open surface of
// its session, behind the attach reply.
func (d *Daemon) sendTMLSurfaces(cs *connState, sessionID string) {
	if !tmlReader(cs, sessionID) {
		return
	}
	d.tml.mu.Lock()
	var payloads []*TMLSurfacePayload
	for _, s := range d.tml.surfaces {
		if s.sessionID == sessionID && !s.closed {
			payloads = append(payloads, s.payload())
		}
	}
	d.tml.mu.Unlock()
	for _, p := range payloads {
		if msg, err := NewMessage(MsgTMLSurface, p); err == nil {
			d.queueBroadcast(cs, msg, "sendTMLSurfaces")
		}
	}
}
