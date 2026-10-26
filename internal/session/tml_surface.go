package session

import (
	"bytes"
	"cmp"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"slices"
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
	// Caller is the window id of the pane that opened the surface, empty
	// for the person's own. The agents view lists the surface under that
	// pane's agent, in its Artifacts.
	Caller string
	// Version counts every change, DocVersion only the changes to the tree.
	Version    uint64
	DocVersion uint64
	// Doc is set when the client may not hold this tree yet: on open, on
	// attach and after a tml-replace. Otherwise it is empty and the tree is
	// the one of DocVersion the client has.
	Doc   []byte
	State []byte
	// Ops is the JSON Patch that made Version from Version-1, when a patch
	// did. Such a push has no State: the client one version behind applies
	// the patch, so only the boxes that read the changed paths are drawn
	// again. A client that is further behind reads the surface with tml-get.
	Ops    []byte
	Closed bool
}

// TMLEvent is the body of a tml-event: what a person did on a surface that
// its owner hears.
type TMLEvent struct {
	Surface string `json:"surface"`
	// Name is the emit action's name, or close.
	Name string `json:"name"`
	// Value is the emit's value, or the close value. Absent when it is null.
	Value any `json:"value,omitempty"`
	// Reason is a close's reason: closed (the person's close action),
	// program (a tml-close) or dismissed.
	Reason string `json:"reason,omitempty"`
	// Version is the surface's version when it happened.
	Version uint64 `json:"version"`
}

// TMLSurfaceID returns the surface a TML window's URI names, or "".
func TMLSurfaceID(uri string) string {
	id, ok := strings.CutPrefix(uri, TMLURIPrefix)
	if !ok {
		return ""
	}
	return id
}

// tmlPlaceholderCommand is the program of a TML window. It prints what to do
// for a client that does not draw the surface: a client attached through
// another machine, or one that predates TML. q or esc ends it, and with it
// the window, which dismisses the surface. It holds the alternate screen and
// button-event mouse reporting (SGR), so a client that does draw TML sends
// the wheel and clicks to the pane, where its TML code takes them before
// they reach this program.
//
// An esc is told from the start of a mouse report by the byte after it: a
// report follows at once, and a lone esc has nothing after it for a tenth of
// a second. A mouse report holds no q.
func tmlPlaceholderCommand(uri string) []string {
	return []string{"/bin/sh", "-c", `stty -echo -icanon min 1 time 0 2>/dev/null
printf '\033[?1049h\033[?1002h\033[?1006h\033[2m%s\n%s\033[0m\n%s\n' "$1" "$2" "$3"
e=$(printf '\033')
while :; do
	k=$(dd bs=1 count=1 2>/dev/null) || exit 0
	case "$k" in
	q) exit 0 ;;
	"$e")
		stty min 0 time 1 2>/dev/null
		n=$(dd bs=1 count=1 2>/dev/null)
		stty min 1 time 0 2>/dev/null
		[ -z "$n" ] && exit 0 ;;
	esac
done`,
		"tuios-tml", "This pane shows a TML surface. Open this session on its own machine to use it.",
		"Press q to close it.", uri}
}

// tmlSurface is one open surface.
type tmlSurface struct {
	id        string
	sessionID string
	windowID  string
	title     string
	owner     string
	// caller keys the surface cap and owns the surface: the opening pane,
	// or "" for a caller in no pane. Only that pane, or a caller in no
	// pane, may patch, replace or close the surface, and its pane()
	// actions run with that pane's grants.
	caller string
	// patches bounds the patches and replaces a second.
	patches tmlBucket
	// ownerClient is the connection that opened the surface.
	ownerClient string

	doc     *tml.Node
	docJSON []byte
	store   *tml.Store
	limits  tml.Limits

	version    uint64
	docVersion uint64

	// view is the view state of the client that last reported one with
	// tml-input, as JSON, for capture-pane. See tml_capture.go.
	view json.RawMessage

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

// payload builds the push for s: with the tree when withDoc, and with the
// patch that made this version when ops is set. The caller holds the host's
// lock.
func (s *tmlSurface) payload(withDoc bool, ops []byte) *TMLSurfacePayload {
	p := &TMLSurfacePayload{Surface: s.id, WindowID: s.windowID, Title: s.title, Owner: s.owner, Caller: s.artifactCaller(),
		Version: s.version, DocVersion: s.docVersion, Closed: s.closed}
	if s.closed {
		return p
	}
	if withDoc {
		p.Doc = s.docJSON
	}
	// A patch travels alone: the client one version behind applies it. The
	// outbox sends the state instead when it folds two pushes into one.
	if ops != nil && !withDoc {
		p.Ops = ops
		return p
	}
	p.State, _ = json.Marshal(s.store.State())
	return p
}

// artifactCaller is the window id of the pane that opened s, or "" for a
// caller in no pane, a pane tuios cannot place and a linked machine.
func (s *tmlSurface) artifactCaller() string {
	if s.caller == unplacedWindow || strings.HasPrefix(s.caller, "link:") {
		return ""
	}
	return s.caller
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
	if terr, ok := errors.AsType[*tml.Error](err); ok {
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
	if cs.viaLink {
		return "link:" + cs.linkPeer
	}
	if pa := d.paneAuthority(cs); pa != nil {
		// A pane this daemon cannot place, or one it runs for another
		// machine, is still not the person.
		return cmp.Or(pa.window, unplacedWindow)
	}
	return ""
}

// tmlOwnerLabel names the caller on cs for the frame: its pane, a linked
// machine, or the CLI.
//
// A pane is named by the first of its labels (tmlPaneLabels) that is its
// alone (tmlLabelIsOwnersAlone): its name, then the agent in it, such as
// "claude", then the agent and the folder it started in. A label is a
// pane's alone when no other window has it among its own labels and it
// cannot pass for an id label, so no pane can copy another pane's label.
// Otherwise the pane is named by its window id, which no program chooses,
// with its first label in brackets. Never the pane's title or the name of
// the program in it, which the program sets (OSC 2, argv[0]).
func (d *Daemon) tmlOwnerLabel(cs *connState) string {
	if cs.viaLink {
		return "machine " + tmlLabelText(cmp.Or(cs.linkPeer, "unknown"), 24)
	}
	if ref := d.callerPaneRef(cs); ref != nil {
		labels := d.tmlPaneLabels(ref.Window)
		for _, l := range labels {
			if d.tmlLabelIsOwnersAlone(ref.Window, l) {
				return l
			}
		}
		if len(labels) == 0 {
			return "pane " + shortID(ref.Window)
		}
		return "pane " + shortID(ref.Window) + " (" + labels[0] + ")"
	}
	if d.paneAuthority(cs) != nil {
		return "a pane tuios cannot place"
	}
	return "tuios tml show"
}

// tmlIDLabel matches the label of a pane with no name, "pane 3f2a91c0".
var tmlIDLabel = regexp.MustCompile(`^pane [0-9a-f]{8}$`)

// tmlPaneLabels are the labels a window may be named by, best first: its
// name, the agent in it, and the agent with the folder the window started
// in. The agent is the harness the pane reported or was detected as, by the
// name its program runs as, such as "claude" for Claude Code.
func (d *Daemon) tmlPaneLabels(windowID string) []string {
	sess := d.sessionHoldingWindow(windowID)
	if sess == nil {
		return nil
	}
	for _, w := range sess.GetState().Windows {
		if w.ID == windowID {
			return d.tmlWindowLabels(sess, w)
		}
	}
	return nil
}

// tmlWindowLabels is tmlPaneLabels for one window of sess.
func (d *Daemon) tmlWindowLabels(sess *Session, w WindowState) []string {
	var out []string
	if name := tmlLabelText(w.CustomName, 24); name != "" {
		out = append(out, name)
	}
	if agent := tmlLabelText(d.tmlAgentName(w.AgentHarness), 24); agent != "" {
		out = append(out, agent)
		start := sess.windowStartDir(w.ID)
		if dir := tmlLabelText(filepath.Base(start), 24); start != "" && dir != "" && dir != "." && dir != string(filepath.Separator) {
			out = append(out, agent+" in "+dir)
		}
	}
	return out
}

// tmlAgentName is the short name of a harness: the first program name its
// manifest detects, such as "claude" for claude-code, else its id.
func (d *Daemon) tmlAgentName(harnessID string) string {
	if harnessID == "" {
		return ""
	}
	if reg := d.agentMatcher.registry; reg != nil {
		if m := reg.Lookup(harnessID); m != nil && len(m.Detect.Comm) > 0 && m.Detect.Comm[0] != "" {
			return m.Detect.Comm[0]
		}
	}
	return harnessID
}

// tmlLabelIsOwnersAlone reports whether a label may stand alone as the
// owner of a window's surfaces: no other window in this daemon has it among
// its labels, and it cannot pass for the label of a pane that has none. A
// name is not always the person's: a pane with the fan grant names the
// agents it starts (start-agent --name), and a program can report itself as
// any harness, so a label alone could copy another pane's.
func (d *Daemon) tmlLabelIsOwnersAlone(windowID, label string) bool {
	if label == "" || tmlIDLabel.MatchString(label) {
		return false
	}
	for _, sess := range d.manager.AllSessions() {
		for _, w := range sess.GetState().Windows {
			if w.ID != windowID && slices.Contains(d.tmlWindowLabels(sess, w), label) {
				return false
			}
		}
	}
	return true
}

// tmlTitleWidth bounds the document's part of a TML window's name.
const tmlTitleWidth = 48

// tmlLabelText makes text safe for a window name: no control characters, no
// separator dot or brackets, which only the owner part uses, and at most max
// runes.
func tmlLabelText(text string, max int) string {
	text = strings.Join(strings.Fields(tml.Sanitize(text)), " ")
	text = strings.NewReplacer("·", "-", "(", "[", ")", "]").Replace(text)
	if r := []rune(text); len(r) > max {
		text = string(r[:max-1]) + "…"
	}
	return text
}

// tmlWindowName is a TML window's name: the owner first, which is never cut,
// then the document's title, which the frame cuts when it must. The title
// cannot hold the separator, so it cannot draw a second owner.
func tmlWindowName(owner, title string) string {
	return "from " + owner + " · " + tmlLabelText(title, tmlTitleWidth)
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
		Events    bool            `json:"events"`
	}
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	if p.Events && p.Wait {
		return nil, invalidParam("events", "events streams what happens on the surface, and its close is the last event, so it needs no wait")
	}
	if p.Events {
		cs.mu.Lock()
		streaming := cs.streaming
		cs.mu.Unlock()
		if streaming {
			return nil, hintedVerbError(ErrVerbInvalidRequest, "connection is already subscribed", &VerbHint{
				Detail: "One event stream per connection. Open the surface with events on a connection of its own.",
			})
		}
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
	sess, verr := d.resolveVerbSession(cs, p.Session)
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
		id:          newTMLSurfaceID(),
		sessionID:   sess.ID,
		title:       title,
		owner:       d.tmlOwnerLabel(cs),
		ownerClient: cs.clientID,
		doc:         doc,
		docJSON:     tml.FormatJSON(doc, false),
		store:       store,
		limits:      limits,
		version:     1, docVersion: 1,
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
	// The window's name names the owner, and the document cannot change it,
	// so a surface cannot pass for one another program opened.
	label := tmlWindowName(s.owner, title)
	opts := NewWindowOptions{
		Title:     label,
		Name:      label,
		Workspace: p.Workspace,
		// A surface a pane opened does not take the keys: the person
		// focuses it with a click or a key, so text meant for another pane
		// cannot answer it. The person's own surface takes the focus.
		Focus:       s.caller == "",
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
	payload := s.payload(true, nil)
	d.tml.mu.Unlock()
	d.recordTMLOpened(s, target)
	d.pushTMLSurface(sess.ID, payload)

	if !p.Wait {
		res := map[string]any{
			"type":      "tml_opened",
			"surface":   s.id,
			"window_id": win.ID,
			"target":    target,
			"workspace": win.Workspace,
		}
		if p.Events {
			// The events of this surface follow the answer on this
			// connection, as a subscribe's do. Nothing is missed in between:
			// the subscription is made before the answer is written.
			sub, baseline, _ := d.events.subscribeFrom(eventFilter{types: map[string]bool{EventTML: true}, surface: s.id}, 0, nil)
			cs.mu.Lock()
			cs.eventSub, cs.pendingStream, cs.streaming = sub, sub, true
			cs.mu.Unlock()
			res["seq"], res["boot_id"] = baseline, d.events.bootIdentity()
		}
		return res, nil
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
func (d *Daemon) verbTMLClose(cs *connState, params json.RawMessage) (any, *verbError) {
	var p struct {
		Surface string          `json:"surface"`
		Value   json.RawMessage `json:"value"`
	}
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	if s, verr := d.tmlSurfaceOf(p.Surface); verr != nil {
		return nil, verr
	} else if verr := d.tmlOwnerCheck(cs, s, "tml-close"); verr != nil {
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
	if s, verr := d.tmlSurfaceOf(p.Surface); verr != nil {
		return nil, verr
	} else if verr := d.tmlReach(cs, s, "tml-close", true); verr != nil {
		return nil, verr
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
		Events     []struct {
			Name  string          `json:"name"`
			Value json.RawMessage `json:"value"`
		} `json:"events"`
		Actions []tmlPaneAction `json:"actions"`
		// View is the client's view state, present when it changed.
		View json.RawMessage `json:"view"`
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
	// The limits are checked before anything applies, so a refused input
	// changes nothing.
	if len(p.Actions) > tmlActionsPerInput {
		return nil, invalidParam("actions", fmt.Sprintf("one press can run at most %d pane actions", tmlActionsPerInput))
	}
	if len(p.Events) > tmlEventsPerInput {
		return nil, invalidParam("events", fmt.Sprintf("one tml-input carries at most %d events", tmlEventsPerInput))
	}
	for _, e := range p.Events {
		if len(e.Value) > s.limits.PatchBytes {
			return nil, invalidParam("events", fmt.Sprintf("an event value is %d bytes; the limit is %d bytes, as for a patch", len(e.Value), s.limits.PatchBytes))
		}
	}
	if view := bytes.TrimSpace(p.View); len(view) > 0 {
		if len(view) > tmlMaxView {
			return nil, invalidParam("view", fmt.Sprintf("the view is %d bytes; the limit is %d bytes", len(view), tmlMaxView))
		}
		d.tml.mu.Lock()
		s.view = append(json.RawMessage(nil), view...)
		d.tml.mu.Unlock()
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
		payload := s.payload(false, ops)
		d.tml.mu.Unlock()
		if err != nil {
			return nil, invalidParam("ops", err.Error())
		}
		d.pushTMLSurface(sessionID, payload)
	}
	for _, e := range p.Events {
		var value any
		if len(bytes.TrimSpace(e.Value)) > 0 {
			if err := json.Unmarshal(e.Value, &value); err != nil {
				return nil, invalidParam("events", "an event value is not JSON: "+err.Error())
			}
			value = tml.SanitizeValue(value)
		}
		d.publishTMLEvent(s, tml.Sanitize(e.Name), value, "")
	}
	var results []map[string]any
	for _, a := range p.Actions {
		results = append(results, d.runTMLPaneAction(s, a))
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
	res := map[string]any{"type": "tml_input", "surface": p.Surface, "version": v}
	if len(results) > 0 {
		res["actions"] = results
	}
	return res, nil
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
	d.recordTMLClosed(s)
	payload := s.payload(false, nil)
	close(s.done)
	sessionID, windowID := s.sessionID, s.windowID
	d.tml.mu.Unlock()
	d.pushTMLSurface(sessionID, payload)
	d.publishTMLEvent(s, "close", value, reason)
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
	d.recordTMLClosed(s)
	payload := s.payload(false, nil)
	close(s.done)
	sessionID := s.sessionID
	d.tml.mu.Unlock()
	d.pushTMLSurface(sessionID, payload)
	d.publishTMLEvent(s, "close", nil, TMLDismissed)
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
// TML, through each client's outbox.
func (d *Daemon) pushTMLSurface(sessionID string, payload *TMLSurfacePayload) {
	d.clientsMu.RLock()
	defer d.clientsMu.RUnlock()
	for _, cs := range d.clients {
		if tmlReader(cs, sessionID) {
			d.tmlEnqueue(cs, payload)
		}
	}
}

// tmlOutbox is one client's TML pushes not yet written: at most one per
// surface. A client that reads slowly, or not at all, makes the daemon hold
// one push per surface, never one per change. The zero value is ready.
type tmlOutbox struct {
	mu      sync.Mutex
	pending map[string]*tmlPending
	order   []string
	sending bool
}

// tmlPending is a push waiting for its client. full says two or more pushes
// were folded into it, so it goes out as the whole surface at send time;
// withDoc says one of them carried the tree.
type tmlPending struct {
	p             *TMLSurfacePayload
	full, withDoc bool
}

// tmlEnqueue puts a push in cs's outbox, folding it into one not yet written
// for the same surface, and starts the writer when none runs.
func (d *Daemon) tmlEnqueue(cs *connState, p *TMLSurfacePayload) {
	ob := &cs.tmlOut
	ob.mu.Lock()
	if ob.pending == nil {
		ob.pending = map[string]*tmlPending{}
	}
	if old := ob.pending[p.Surface]; old != nil {
		old.p = p
		old.full = !p.Closed
		old.withDoc = old.withDoc || len(p.Doc) > 0
	} else {
		ob.pending[p.Surface] = &tmlPending{p: p, withDoc: len(p.Doc) > 0}
		ob.order = append(ob.order, p.Surface)
	}
	start := !ob.sending
	ob.sending = true
	ob.mu.Unlock()
	if start && !d.goTracked(func() { d.tmlDrain(cs) }) {
		ob.mu.Lock()
		ob.sending = false
		ob.mu.Unlock()
	}
}

// tmlDrain writes cs's outbox in the order the surfaces first queued, until
// it is empty. A folded push is built again from the surface as it is now.
func (d *Daemon) tmlDrain(cs *connState) {
	ob := &cs.tmlOut
	for {
		ob.mu.Lock()
		if len(ob.order) == 0 {
			ob.sending = false
			ob.mu.Unlock()
			return
		}
		id := ob.order[0]
		ob.order = ob.order[1:]
		pend := ob.pending[id]
		delete(ob.pending, id)
		ob.mu.Unlock()
		p := pend.p
		if pend.full {
			d.tml.mu.Lock()
			if s := d.tml.surfaces[id]; s != nil {
				p = s.payload(pend.withDoc, nil)
			}
			d.tml.mu.Unlock()
		}
		msg, err := NewMessage(MsgTMLSurface, p)
		if err != nil {
			continue
		}
		if err := d.sendEncoded(cs, msg); err != nil {
			ob.mu.Lock()
			ob.pending, ob.order, ob.sending = nil, nil, false
			ob.mu.Unlock()
			return
		}
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
			payloads = append(payloads, s.payload(true, nil))
		}
	}
	d.tml.mu.Unlock()
	for _, p := range payloads {
		d.tmlEnqueue(cs, p)
	}
}

// publishTMLEvent tells the owner, and any subscriber of tml-event, what
// happened on a surface.
func (d *Daemon) publishTMLEvent(s *tmlSurface, name string, value any, reason string) {
	d.tml.mu.Lock()
	ev := &TMLEvent{Surface: s.id, Name: name, Value: value, Reason: reason, Version: s.version}
	sessionID, window := s.sessionID, s.windowID
	d.tml.mu.Unlock()
	session := ""
	if sess := d.manager.GetSessionByID(sessionID); sess != nil {
		session = sess.Name()
	}
	d.events.publish(streamEvent{Type: EventTML, Session: session, Window: window, TML: ev})
}

// tmlSurfaceOf finds an open surface, or answers no_surface.
func (d *Daemon) tmlSurfaceOf(id string) (*tmlSurface, *verbError) {
	d.tml.mu.Lock()
	s := d.tml.surfaces[id]
	d.tml.mu.Unlock()
	if s == nil {
		return nil, noSurface(id)
	}
	return s, nil
}

// verbTMLPatch applies a JSON Patch to a surface's state.
func (d *Daemon) verbTMLPatch(cs *connState, params json.RawMessage) (any, *verbError) {
	var p struct {
		Surface string          `json:"surface"`
		Ops     json.RawMessage `json:"ops"`
	}
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	s, verr := d.tmlSurfaceOf(p.Surface)
	if verr == nil {
		verr = d.tmlReach(cs, s, "tml-patch", true)
	}
	if verr != nil {
		return nil, verr
	}
	if verr := d.tmlOwnerCheck(cs, s, "tml-patch"); verr != nil {
		return nil, verr
	}
	if !s.patches.allow(time.Now()) {
		return nil, hintedVerbError(ErrVerbRateLimited, fmt.Sprintf("the surface takes at most %d patches a second", tmlPatchesPerSecond), &VerbHint{
			Detail: "Send fewer, larger patches: one patch can hold many operations. Nothing was changed.",
		})
	}
	ops := bytes.TrimSpace(p.Ops)
	if len(ops) == 0 || ops[0] != '[' {
		return nil, invalidParam("ops", "ops is a JSON Patch: an array of operations such as {\"op\":\"replace\",\"path\":\"/count\",\"value\":3}")
	}
	d.tml.mu.Lock()
	parsed, err := tml.DecodePatch(ops, &s.limits)
	if err == nil && s.closed {
		d.tml.mu.Unlock()
		return nil, noSurface(p.Surface)
	}
	var changed []string
	if err == nil {
		changed, err = s.store.Apply(parsed)
	}
	if err != nil {
		d.tml.mu.Unlock()
		return nil, tmlPatchError(err)
	}
	s.version++
	payload := s.payload(false, ops)
	version, sessionID := s.version, s.sessionID
	d.tml.mu.Unlock()
	d.pushTMLSurface(sessionID, payload)
	return map[string]any{"type": "tml_patched", "surface": p.Surface, "version": version, "changed": changed}, nil
}

// tmlPatchError is the answer to a patch that cannot apply. A failed test
// says to read the state again.
func tmlPatchError(err error) *verbError {
	var pe *tml.PatchError
	if errors.As(err, &pe) && pe.Code == tml.CodeTestFailed {
		return hintedVerbError(ErrVerbInvalidParams, err.Error(), &VerbHint{
			Param:   "ops",
			Verb:    "tml-get",
			Command: "tuios tml get <surface>",
			Detail:  "A test operation did not match: the state changed since you read it. Nothing was changed. Read the state again and send the patch again.",
		})
	}
	return invalidParam("ops", err.Error())
}

// verbTMLReplace swaps a surface's tree, and its state unless keep_state.
func (d *Daemon) verbTMLReplace(cs *connState, params json.RawMessage) (any, *verbError) {
	var p struct {
		Surface   string          `json:"surface"`
		Doc       json.RawMessage `json:"doc"`
		KeepState bool            `json:"keep_state"`
	}
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	s, verr := d.tmlSurfaceOf(p.Surface)
	if verr == nil {
		verr = d.tmlReach(cs, s, "tml-replace", true)
	}
	if verr != nil {
		return nil, verr
	}
	if verr := d.tmlOwnerCheck(cs, s, "tml-replace"); verr != nil {
		return nil, verr
	}
	if !s.patches.allow(time.Now()) {
		return nil, hintedVerbError(ErrVerbRateLimited, fmt.Sprintf("the surface takes at most %d changes a second", tmlPatchesPerSecond), &VerbHint{
			Detail: "A replace counts as a patch. Nothing was changed.",
		})
	}
	src, verr := tmlDocSource(p.Doc)
	if verr != nil {
		return nil, verr
	}
	d.tml.mu.Lock()
	limits := s.limits
	d.tml.mu.Unlock()
	doc, problems, err := tml.Check(src, &tml.ValidateOptions{Limits: &limits, NoStateCheck: p.KeepState})
	if err != nil {
		return nil, tmlInvalid(problems, err)
	}
	d.tml.mu.Lock()
	if s.closed {
		d.tml.mu.Unlock()
		return nil, noSurface(p.Surface)
	}
	var store *tml.Store
	if p.KeepState {
		// The kept state goes into the new tree, so the store checks it
		// against the new tree's limits on each, list and rows.
		raw, _ := json.Marshal(s.store.State())
		withState := doc.Clone()
		withState.SetAttr("state", tml.Value{Kind: tml.KindObject, Raw: raw})
		store, err = tml.StoreFor(withState, &limits)
	} else {
		store, err = tml.StoreFor(doc, &limits)
	}
	if err != nil {
		d.tml.mu.Unlock()
		return nil, tmlInvalid(nil, err)
	}
	s.doc, s.docJSON, s.store = doc, tml.FormatJSON(doc, false), store
	if t := doc.AttrString("title"); t != "" {
		s.title = t
	}
	s.version++
	s.docVersion++
	payload := s.payload(true, nil)
	version, sessionID := s.version, s.sessionID
	d.tml.mu.Unlock()
	d.pushTMLSurface(sessionID, payload)
	return map[string]any{"type": "tml_replaced", "surface": p.Surface, "version": version}, nil
}

// verbTMLGet reads a surface: its tree, its state and its version.
func (d *Daemon) verbTMLGet(cs *connState, params json.RawMessage) (any, *verbError) {
	var p struct {
		Surface string `json:"surface"`
	}
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	s, verr := d.tmlSurfaceOf(p.Surface)
	if verr == nil {
		verr = d.tmlReach(cs, s, "tml-get", false)
	}
	if verr != nil {
		return nil, verr
	}
	d.tml.mu.Lock()
	defer d.tml.mu.Unlock()
	state, _ := json.Marshal(s.store.State())
	return map[string]any{
		"type": "tml_surface", "surface": s.id, "window_id": s.windowID, "title": s.title, "owner": s.owner,
		"version": s.version, "doc_version": s.docVersion, "doc": json.RawMessage(s.docJSON), "state": json.RawMessage(state),
	}, nil
}

// verbTMLList lists the open surfaces of a session.
func (d *Daemon) verbTMLList(cs *connState, params json.RawMessage) (any, *verbError) {
	var p struct {
		Session string `json:"session"`
	}
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	sess, verr := d.resolveVerbSession(cs, p.Session)
	if verr != nil {
		return nil, verr
	}
	d.tml.mu.Lock()
	defer d.tml.mu.Unlock()
	list := []map[string]any{}
	for _, s := range d.tml.surfaces {
		if s.sessionID != sess.ID {
			continue
		}
		list = append(list, map[string]any{"surface": s.id, "window_id": s.windowID, "title": s.title, "owner": s.owner, "version": s.version})
	}
	slices.SortFunc(list, func(a, b map[string]any) int { return strings.Compare(a["surface"].(string), b["surface"].(string)) })
	return map[string]any{"type": "tml_list", "session": sess.Name(), "surfaces": list}, nil
}

// verbTMLValidate checks a document as tml-open would, with no session.
func (d *Daemon) verbTMLValidate(cs *connState, params json.RawMessage) (any, *verbError) {
	var p struct {
		Doc json.RawMessage `json:"doc"`
	}
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	src, verr := tmlDocSource(p.Doc)
	if verr != nil {
		return nil, verr
	}
	limits := d.tmlLimitsFor(cs)
	_, problems, err := tml.Check(src, &tml.ValidateOptions{Limits: &limits})
	if err != nil {
		return nil, tmlInvalid(problems, err)
	}
	if problems == nil {
		problems = []tml.Problem{}
	}
	return map[string]any{"type": "tml_valid", "warnings": problems}, nil
}

// tmlEventsPerInput bounds the emits one tml-input carries, and
// tmlActionsPerInput its pane actions: one key or click runs a few.
const (
	tmlEventsPerInput  = 16
	tmlActionsPerInput = 4
)

// tmlReach refuses a caller that may not reach a surface's session: a pane
// outside the session and its fan group, a pane without the read grant for
// a read, and a connection restricted to its own session. A caller in no
// pane, and a pane that holds admin, reach every surface.
func (d *Daemon) tmlReach(cs *connState, s *tmlSurface, verb string, write bool) *verbError {
	d.tml.mu.Lock()
	sessionID := s.sessionID
	d.tml.mu.Unlock()
	target := d.sessionNameByID(sessionID)
	if sc := cs.scope.Load(); sc != nil && sc.own {
		own := d.sessionNameByID(sc.sessionID)
		if own == "" || !d.sessionInScope(own, target) {
			return scopeForbidden(verb, "the surface is in session "+echoName(target)+", outside the connection's own")
		}
	}
	pa := d.paneAuthority(cs)
	if pa == nil || pa.grants.Has(GrantAdmin) {
		return nil
	}
	// The pane that opened a surface reaches it, with write alone.
	d.tml.mu.Lock()
	owner := s.caller
	d.tml.mu.Unlock()
	if pa.window != "" && pa.window == owner {
		return nil
	}
	if !d.sessionInScope(pa.session, target) {
		return grantForbidden(verb, pa, "the surface is in session "+echoName(target)+", which is not the pane's own session or in its fan group")
	}
	if !write && !pa.grants.Has(GrantRead) {
		return grantForbidden(verb, pa, verb+" needs the read grant")
	}
	return nil
}

// tmlPatchesPerSecond bounds the patches and replaces of one surface.
const tmlPatchesPerSecond = 100

// tmlBucket is a token bucket of tmlPatchesPerSecond tokens a second, full
// when it starts. The zero value is ready. It is guarded by its own lock.
type tmlBucket struct {
	mu     sync.Mutex
	tokens float64
	last   time.Time
}

// allow takes a token at now, and reports whether there was one.
func (b *tmlBucket) allow(now time.Time) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.last.IsZero() {
		b.tokens = tmlPatchesPerSecond
	} else if d := now.Sub(b.last).Seconds(); d > 0 {
		b.tokens = min(tmlPatchesPerSecond, b.tokens+d*tmlPatchesPerSecond)
	}
	b.last = now
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

// tmlOwnerCheck refuses a caller that may not change s: a pane that did not
// open it. A caller in no pane, the person's own commands and hooks, may
// change any surface.
func (d *Daemon) tmlOwnerCheck(cs *connState, s *tmlSurface, verb string) *verbError {
	caller := d.tmlCallerKey(cs)
	if caller == "" || caller == s.caller {
		return nil
	}
	d.tml.mu.Lock()
	owner := s.owner
	d.tml.mu.Unlock()
	return hintedVerbError(ErrVerbForbidden, verb+" changes a surface only for the pane that opened it, and "+owner+" opened this one", &VerbHint{
		Detail: "A pane changes only its own surfaces. Open one of your own with tml-open. Nothing was changed.",
	})
}

// tmlPaneVerbs maps the verbs of a pane() action to the verbs they run.
var tmlPaneVerbs = map[string]string{
	"focus":      "focus-window",
	"new-window": "new-window",
	"send-text":  "send-text",
}

// tmlNewWindowParams are the new-window parameters a surface may pass.
var tmlNewWindowParams = []string{"session", "name", "workspace", "cwd", "focus", "command"}

// tmlPaneAction is one pane() action the person ran on a surface.
type tmlPaneAction struct {
	Verb   string         `json:"verb"`
	Params map[string]any `json:"params"`
}

// runTMLPaneAction runs a pane() action with the grants of the surface's
// owner, never the person's: a connection placed in the opening pane, or the
// person's own when no pane opened it. The verb's own checks apply, so a
// send-text into a pane that holds more than the owner is refused as it is
// for the owner's own call.
func (d *Daemon) runTMLPaneAction(s *tmlSurface, a tmlPaneAction) map[string]any {
	verb, ok := tmlPaneVerbs[a.Verb]
	if !ok {
		return map[string]any{"verb": a.Verb, "ok": false, "error": "a pane action is focus, new-window or send-text"}
	}
	params := map[string]any{}
	for k, v := range a.Params {
		// A new window from a surface takes only these: no host, no
		// environment, no wait, no grants and no kind. The person saw the
		// command in the question before the action came here.
		if verb == "new-window" && !slices.Contains(tmlNewWindowParams, k) {
			continue
		}
		params[k] = v
	}
	d.tml.mu.Lock()
	owner, sessionID := s.caller, s.sessionID
	d.tml.mu.Unlock()
	if owner != "" && d.sessionHoldingWindow(owner) == nil {
		return map[string]any{"verb": a.Verb, "ok": false, "error": "the surface was not opened by a pane of this machine, so it runs no pane action here"}
	}
	if _, named := params["session"]; !named {
		if sess := d.manager.GetSessionByID(sessionID); sess != nil {
			params["session"] = sess.Name()
		}
	}
	as := &connState{clientID: "tml:" + s.id, done: make(chan struct{})}
	if owner != "" {
		as.paneBound.Store(&owner)
	}
	if _, verr := d.callVerb(as, verb, params); verr != nil {
		return map[string]any{"verb": a.Verb, "ok": false, "error": verr.Message, "code": verr.Code}
	}
	return map[string]any{"verb": a.Verb, "ok": true}
}

// verbTMLPlace moves a surface between a popup and a tiled pane: the person's
// choice of where it sits. The surface, its state and its window stay; only
// the window's placement changes, so a form keeps what the person typed. It
// acts as the person, so it needs the attach nonce of a client. A patch or a
// replace from the owner never moves the window back.
func (d *Daemon) verbTMLPlace(cs *connState, params json.RawMessage) (any, *verbError) {
	var p struct {
		Surface    string `json:"surface"`
		Target     string `json:"target"`
		HumanNonce string `json:"human_nonce"`
	}
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	if p.Target != TMLTargetPopup && p.Target != TMLTargetPane {
		return nil, invalidParam("target", "unknown target "+echoName(p.Target), tmlTargets...)
	}
	s, verr := d.tmlSurfaceOf(p.Surface)
	if verr != nil {
		return nil, verr
	}
	d.tml.mu.Lock()
	sessionID, windowID := s.sessionID, s.windowID
	d.tml.mu.Unlock()
	if _, ok := d.humanNonceFor(p.HumanNonce, sessionID, cs); !ok {
		return nil, hintedVerbError(ErrVerbNotHuman, "tml-place acts as the person, so it needs the attach nonce of a client", &VerbHint{
			Param:  "human_nonce",
			Detail: "The person moves a surface between a popup and a pane. A program chooses the place once, with tml-open.",
		})
	}
	sess := d.manager.GetSessionByID(sessionID)
	if sess == nil || windowID == "" {
		return nil, noSurface(p.Surface)
	}
	popup := p.Target == TMLTargetPopup
	err := sess.mutateState(func(state *SessionState) error {
		idx, err := findWindowStateIndex(state.Windows, windowID)
		if err != nil {
			return err
		}
		w := &state.Windows[idx]
		w.Popup, w.IsFloating = popup, popup
		return nil
	})
	if err != nil {
		return nil, mapResolveErr(err, sess)
	}
	d.artifacts.update(s.caller, s.id, func(r *artifactRec) { r.target = p.Target })
	return map[string]any{"type": "tml_placed", "surface": p.Surface, "window_id": windowID, "target": p.Target}, nil
}
