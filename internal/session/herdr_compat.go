package session

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net"
	"os"
	"path/filepath"
	"runtime/debug"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/herdrplugin"
	"github.com/Gaurav-Gosain/tuios/internal/integration"
	"github.com/Gaurav-Gosain/tuios/internal/jsonx"
	"github.com/Gaurav-Gosain/tuios/internal/procinfo"
)

const herdrMaxRequest = 1 << 20

const herdrIOTimeout = 2 * time.Second

const herdrSeqMax = 4096

// HerdrSocketPath is the socket tuios accepts herdr's pane state protocol on,
// beside the daemon's own socket.
func HerdrSocketPath(socketPath string) string {
	return socketPath + ".herdr"
}

// HerdrLinkDir is the directory the herdr link is made in, beside the daemon
// socket: <dir>/bin/herdr points at tuios. It is private to the user in the
// same way the socket is.
func HerdrLinkDir(socketPath string) string {
	return filepath.Join(filepath.Dir(socketPath), "herdr")
}

type herdrRequest struct {
	ID     string          `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
}

type herdrParams struct {
	PaneID         string   `json:"pane_id"`
	Source         string   `json:"source"`
	Agent          string   `json:"agent"`
	State          string   `json:"state"`
	Message        *string  `json:"message"`
	Seq            *uint64  `json:"seq"`
	AgentSessionID *string  `json:"agent_session_id"`
	ResumeArgv     []string `json:"resume_argv"`

	Title      *string         `json:"title"`
	ClearTitle bool            `json:"clear_title"`
	Tokens     json.RawMessage `json:"tokens"`
	TTLMs      int64           `json:"ttl_ms"`

	Body string `json:"body"`
}

const (
	herdrConnsPerCaller   = 64
	herdrStreamsPerCaller = 8
)

type herdrConnCount struct {
	mu sync.Mutex
	n  map[int]int
}

func (c *herdrConnCount) take(key, limit int) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.n == nil {
		c.n = make(map[int]int)
	}
	if c.n[key] >= limit {
		return false
	}
	c.n[key]++
	return true
}

func (c *herdrConnCount) give(key int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.n[key]--; c.n[key] <= 0 {
		delete(c.n, key)
	}
}

type herdrSeqs struct {
	mu   sync.Mutex
	high map[string]uint64
}

func (h *herdrSeqs) fresh(key string, seq *uint64) bool {
	if seq == nil {
		return true
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.high == nil || len(h.high) > herdrSeqMax {
		h.high = make(map[string]uint64)
	}
	if last, ok := h.high[key]; ok && *seq <= last {
		return false
	}
	h.high[key] = *seq
	return true
}

func (h *herdrSeqs) atOrBelow(key string, seq *uint64) bool {
	if seq == nil {
		return false
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	last, ok := h.high[key]
	return ok && *seq <= last
}

const (
	herdrEventRate  = 5.0
	herdrEventBurst = 20.0
	herdrBucketsMax = 4096
)

type paneBuckets struct {
	mu sync.Mutex
	b  map[string]*paneBucket
}

type paneBucket struct {
	tokens float64
	at     time.Time
}

func (h *paneBuckets) take(window string, now time.Time, rate, burst float64) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.b == nil || len(h.b) > herdrBucketsMax {
		h.b = make(map[string]*paneBucket)
	}
	bk := h.b[window]
	if bk == nil {
		bk = &paneBucket{tokens: burst, at: now}
		h.b[window] = bk
	}
	bk.tokens = min(burst, bk.tokens+now.Sub(bk.at).Seconds()*rate)
	bk.at = now
	if bk.tokens < 1 {
		return false
	}
	bk.tokens--
	return true
}

func (d *Daemon) herdrYields(window, harness, source string) bool {
	if !strings.HasPrefix(source, "herdr:") {
		return false
	}
	sess := d.sessionHoldingWindow(window)
	if sess == nil {
		return false
	}
	sess.stateMu.RLock()
	defer sess.stateMu.RUnlock()
	claim, held := sess.agentClaims[window]
	return held && claim.source == AgentSourceReport && claim.herdrAt == 0 && claim.harness != "" && claim.harness == harness
}

func listenHerdrSocket(path string) net.Listener {
	_ = os.Remove(path)
	l, err := net.Listen("unix", path)
	if err != nil {
		log.Printf("The herdr protocol socket %s could not be opened: %v. Harnesses that report to herdr are read from the screen instead.", path, err)
		return nil
	}
	if ul, ok := l.(*net.UnixListener); ok {
		ul.SetUnlinkOnClose(false)
	}
	if err := os.Chmod(path, 0o700); err != nil { //nolint:gosec // a socket, owner only; the execute bit means nothing on it
		_ = l.Close()
		_ = os.Remove(path)
		log.Printf("The herdr protocol socket %s could not be secured: %v", path, err)
		return nil
	}
	return l
}

func (d *Daemon) acceptHerdrLoop(l net.Listener) {
	for {
		conn, err := l.Accept()
		if err != nil {
			if d.acceptPaused.Load() {
				return
			}
			select {
			case <-d.ctx.Done():
				return
			default:
			}
			if errors.Is(err, net.ErrClosed) {
				return
			}
			log.Printf("Accept error on the herdr protocol socket: %v", err)
			continue
		}
		go d.serveHerdr(conn)
	}
}

func (d *Daemon) serveHerdr(conn net.Conn) {
	defer func() { _ = conn.Close() }()
	defer func() {
		if r := recover(); r != nil {
			log.Printf("PANIC in the herdr protocol socket: %v\n%s", r, debug.Stack())
		}
	}()
	_ = conn.SetDeadline(time.Now().Add(herdrIOTimeout))
	cs := &connState{conn: conn, clientID: "herdr-" + newClientID(), done: make(chan struct{}), peerPID: procinfo.PeerPID(conn)}
	if !d.herdrConns.take(cs.peerPID, herdrConnsPerCaller) {
		writeHerdr(conn, herdrRequestID(conn), nil, "rate_limited", "this process has too many connections open on the herdr socket. Close some and try again")
		return
	}
	defer d.herdrConns.give(cs.peerPID)
	d.pinPeer(cs)
	line, err := bufio.NewReaderSize(io.LimitReader(conn, herdrMaxRequest), 4096).ReadBytes('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return
	}
	if len(bytes.TrimSpace(line)) == 0 {
		return
	}
	var req herdrRequest
	if err := jsonx.Unmarshal(line, &req); err != nil {
		var idOnly struct {
			ID string `json:"id"`
		}
		_ = jsonx.Unmarshal(line, &idOnly)
		writeHerdr(conn, idOnly.ID, nil, "invalid_request", "invalid request: "+err.Error())
		return
	}
	if req.Method == "events.subscribe" {
		if herr := d.herdrAllowed(cs, req.Method); herr != nil {
			writeHerdr(conn, req.ID, nil, herr.code, herr.msg)
			return
		}
		if !d.herdrConns.take(-1-cs.peerPID, herdrStreamsPerCaller) {
			writeHerdr(conn, req.ID, nil, "rate_limited", "this process has too many event streams open. Close one and subscribe again")
			return
		}
		defer d.herdrConns.give(-1 - cs.peerPID)
		d.serveHerdrEvents(cs, req.ID, req.Params)
		return
	}
	_ = conn.SetDeadline(time.Time{})
	result, code, msg := d.herdrCall(cs, req)
	_ = conn.SetWriteDeadline(time.Now().Add(herdrIOTimeout))
	writeHerdr(conn, req.ID, result, code, msg)
}

func herdrRequestID(conn net.Conn) string {
	line, _ := bufio.NewReaderSize(io.LimitReader(conn, herdrMaxRequest), 4096).ReadBytes('\n')
	var idOnly struct {
		ID string `json:"id"`
	}
	_ = jsonx.Unmarshal(line, &idOnly)
	return idOnly.ID
}

func writeHerdr(w io.Writer, id string, result any, code, msg string) {
	var out any
	if code != "" {
		out = map[string]any{"id": id, "error": map[string]string{"code": code, "message": msg}}
	} else {
		out = map[string]any{"id": id, "result": result}
	}
	data, err := jsonx.Marshal(out)
	if err != nil {
		return
	}
	_, _ = w.Write(append(data, '\n'))
}

var herdrReportMethods = []string{
	"pane.report_agent", "pane.report_agent_session", "pane.release_agent",
	"pane.report_metadata", "notification.show", "pane.clear_agent_authority",
}

var herdrRetiredMethods = []string{
	"pane.graphics.info", "pane.graphics.set", "pane.graphics.clear", "pane.graphics.stream",
}

func (d *Daemon) herdrCall(cs *connState, req herdrRequest) (any, string, string) {
	if strings.HasPrefix(req.Method, "pane.graphics.") && slices.Contains(herdrRetiredMethods, req.Method) {
		return nil, "unknown_method", "unknown method: " + req.Method
	}
	if !slices.Contains(herdrReportMethods, req.Method) {
		out, herr, handled := d.herdrAPICall(cs, req.Method, req.Params)
		if !handled {
			return nil, "invalid_request", "invalid request: unknown variant `" + echoName(req.Method) + "`"
		}
		if herr != nil {
			return nil, herr.code, herr.msg
		}
		return out, "", ""
	}
	if herr := d.herdrAllowed(cs, req.Method); herr != nil {
		return nil, herr.code, herr.msg
	}
	var p herdrParams
	if len(req.Params) > 0 {
		if err := jsonx.Unmarshal(req.Params, &p); err != nil {
			return nil, "invalid_params", "params: " + err.Error()
		}
	}
	if req.Method == "notification.show" {
		fromPane, window := d.peerPane(cs)
		if !fromPane || window == "" {
			return nil, "forbidden", "the caller runs in no pane of this tuios"
		}
		p.PaneID = window
	}
	window, outside, code, msg := d.herdrPane(cs, req.Method, p.PaneID)
	if code != "" {
		return nil, code, msg
	}
	sess := d.sessionOfWindow(window)
	if sess == "" {
		return nil, "pane_not_found", "no pane " + p.PaneID
	}
	if req.Method == "notification.show" || req.Method == "pane.report_metadata" {
		if !d.herdrEvents.take(window, time.Now(), herdrEventRate, herdrEventBurst) {
			return nil, "rate_limited", "this pane sends notifications and metadata too fast; wait and send again"
		}
	}
	if req.Method == "notification.show" {
		return d.herdrNotify(window, p)
	}
	seqKey := window + "\x00" + p.Source
	if req.Method == "pane.report_metadata" {
		seqKey = window + "\x00meta\x00" + p.Source
	}
	if req.Method == "pane.report_metadata" && d.herdrSeqs.atOrBelow(window+"\x00"+p.Source, p.Seq) {
		return map[string]any{"type": "ok"}, "", ""
	}
	if !d.herdrSeqs.fresh(seqKey, p.Seq) {
		return map[string]any{"type": "ok"}, "", ""
	}
	if req.Method == "pane.report_metadata" {
		return d.herdrMetadata(sess, window, p)
	}
	harness := herdrHarness(p.Agent)
	pid := 0
	anchorCS := cs
	if outside {
		anchorCS = nil
	} else if cs != nil {
		pid = cs.peerPID
	}
	switch req.Method {
	case "pane.report_agent":
		if d.herdrYields(window, harness, p.Source) {
			return map[string]any{"type": "ok"}, "", ""
		}
		out, code, msg := d.herdrReport(sess, window, harness, pid, p, !outside)
		if code == "" {
			wins := crushScreenWins(harness) && d.windowRunsHarness(window, harness)
			d.markHerdrClaim(window, d.herdrAnchorsFor(anchorCS, window), wins)
			if wins && (p.State == "working" || p.State == "blocked") {
				d.lookAtScreenNow(window)
			}
		}
		return out, code, msg
	case "pane.report_agent_session":
		if harness == "" || p.AgentSessionID == nil || *p.AgentSessionID == "" {
			return nil, "invalid_params", "a session report needs an agent and an agent_session_id"
		}
		raw, _ := jsonx.Marshal(map[string]any{"session": sess, "window": window, "harness": harness, "agent_session_id": *p.AgentSessionID})
		if _, verr := d.verbSetAgentSession(nil, raw); verr != nil {
			return nil, "report_failed", verr.Message
		}
	case "pane.clear_agent_authority":
		if held := d.reportClaimHarness(window); held != nil {
			if _, code, msg := d.herdrSetState(sess, window, *held, "none", "", "", "", 0, !outside); code != "" {
				return nil, code, msg
			}
		}
	case "pane.release_agent":
		if d.herdrYields(window, harness, p.Source) {
			return map[string]any{"type": "ok"}, "", ""
		}
		if _, code, msg := d.herdrSetState(sess, window, harness, "none", "", "", "", 0, !outside); code != "" {
			return nil, code, msg
		}
	}
	return map[string]any{"type": "ok"}, "", ""
}

func (d *Daemon) reportClaimHarness(window string) *string {
	sess := d.sessionHoldingWindow(window)
	if sess == nil {
		return nil
	}
	sess.stateMu.RLock()
	defer sess.stateMu.RUnlock()
	claim, held := sess.agentClaims[window]
	if !held || claim.source != AgentSourceReport {
		return nil
	}
	return &claim.harness
}

func herdrHarness(agent string) string {
	if id, ok := integration.Canonical(agent); ok {
		return id
	}
	var b strings.Builder
	for _, r := range strings.ToLower(strings.TrimSpace(agent)) {
		if b.Len() >= 32 {
			break
		}
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-', r == '_':
			b.WriteRune(r)
		case r == ' ':
			b.WriteByte('-')
		}
	}
	return strings.Trim(b.String(), "-_")
}

func (d *Daemon) herdrPane(cs *connState, method, paneID string) (window string, outside bool, code, msg string) {
	fromPane, window := d.peerPane(cs)
	if !fromPane || window == "" {
		if cs != nil && (cs.viaLink || cs.paneOnly) {
			return "", false, "forbidden", "a pane report from another machine is refused"
		}
		sess, win, ierr := d.herdrFindPane(paneID)
		if ierr != nil {
			return "", false, "pane_not_found", "no pane " + echoName(paneID)
		}
		raw, _ := jsonx.Marshal(map[string]string{"session": sess.Name(), "window": win.ID})
		if _, _, verr := d.admitVerb(cs, herdrReportVerb(method), raw); verr != nil {
			return "", false, "forbidden", verr.Message
		}
		return win.ID, true, "", ""
	}
	if _, part, ok := strings.Cut(paneID, ":p"); paneID != window && (!ok || part != herdrHex(window)) {
		if target, ok := d.pluginPaneMetadataTarget(cs, method, window, paneID); ok {
			return target, true, "", ""
		}
		return "", false, "forbidden", "a pane may report only for itself"
	}
	return window, false, "", ""
}

func (d *Daemon) pluginPaneMetadataTarget(cs *connState, method, own, paneID string) (string, bool) {
	if method != "pane.report_metadata" || d.plugins == nil || cs == nil || cs.viaLink || cs.paneOnly || d.manager.grants.strict() {
		return "", false
	}
	d.plugins.mu.Lock()
	rec, isPluginPane := d.plugins.panes[own]
	d.plugins.mu.Unlock()
	if !isPluginPane {
		return "", false
	}
	if e, herr := d.herdrPluginEntry(rec.pluginID); herr != nil || e.Plugin.Format == herdrplugin.FormatTuios {
		return "", false
	}
	_, win, ierr := d.herdrFindPane(paneID)
	if ierr != nil {
		return "", false
	}
	return win.ID, true
}

func herdrReportVerb(method string) string {
	switch method {
	case "pane.report_metadata":
		return "set-agent-meta"
	case "pane.report_agent_session":
		return "set-agent-session"
	}
	return "set-agent-state"
}

func (d *Daemon) markHerdrClaim(window string, anchors []herdrAnchor, screenWins bool) {
	sess := d.sessionHoldingWindow(window)
	if sess == nil {
		return
	}
	sess.stateMu.Lock()
	defer sess.stateMu.Unlock()
	claim, held := sess.agentClaims[window]
	if !held || claim.source != AgentSourceReport {
		return
	}
	claim.herdrAt = time.Now().UnixNano()
	claim.herdrAnchors = anchors
	claim.screenWins = screenWins
	sess.agentClaims[window] = claim
}

func crushScreenWins(harness string) bool {
	return harness == "crush"
}

func (d *Daemon) windowRunsHarness(window, harness string) bool {
	sess := d.sessionHoldingWindow(window)
	if sess == nil {
		return false
	}
	st := sess.GetState()
	i, err := findWindowStateIndex(st.Windows, window)
	if err != nil {
		return false
	}
	return d.paneRunsHarness(sess, st.Windows[i].PTYID, harness)
}

func (d *Daemon) paneRunsHarness(sess *Session, ptyID, harness string) bool {
	info, running := d.foregroundResolver(sess)(ptyID)
	if !running {
		return false
	}
	det, ok := d.agentMatcher.identifyDetail(info)
	return ok && det.harness == harness
}

func (d *Daemon) lookAtScreenNow(window string) {
	sess := d.sessionHoldingWindow(window)
	if sess == nil {
		return
	}
	st := sess.GetState()
	i, err := findWindowStateIndex(st.Windows, window)
	if err != nil {
		return
	}
	if pty := sess.GetPTY(st.Windows[i].PTYID); pty != nil {
		pty.runScreenLook()
	}
}

func (d *Daemon) herdrAnchorsFor(cs *connState, window string) []herdrAnchor {
	if cs == nil || cs.peerPID <= 1 || cs.peerPID == os.Getpid() {
		return nil
	}
	start, ok := cs.peerStart, cs.peerStartOK
	if !ok {
		start, ok = procinfo.StartTime(cs.peerPID)
	}
	if !ok {
		return nil
	}
	anchors := []herdrAnchor{{pid: cs.peerPID, start: start}}
	paneShell := 0
	for _, sh := range d.localPaneShells() {
		if sh.windowID == window {
			paneShell = sh.shellPID
		}
	}
	cur := cs.peerPID
	for range paneOriginMaxDepth {
		ppid, _, ok := readProcLineage(cur)
		if !ok || ppid <= 1 || ppid == paneShell || ppid == os.Getpid() {
			break
		}
		cur = ppid
		info := readProcessInfo(ppid)
		name := agentBaseName(info.comm)
		if len(info.argv) > 0 {
			name = agentBaseName(info.argv[0])
		}
		if name == "" || loginShells[name] {
			continue
		}
		if st, ok := procinfo.StartTime(ppid); ok {
			anchors = append(anchors, herdrAnchor{pid: ppid, start: st})
		}
		break
	}
	return anchors
}

func (d *Daemon) herdrMetadata(sessName, window string, p herdrParams) (any, string, string) {
	tokens := map[string]*string{}
	if len(p.Tokens) > 0 && string(p.Tokens) != "null" {
		var raw map[string]*string
		if err := jsonx.Unmarshal(p.Tokens, &raw); err != nil {
			return nil, "invalid_params", "tokens: " + err.Error()
		}
		for k, v := range raw {
			k = strings.ToLower(k)
			if !ValidAgentMetaKey(k) || slices.Contains(reservedAgentMetaKeys, k) {
				continue
			}
			if v != nil && strings.TrimSpace(*v) == "" {
				v = nil
			}
			tokens[k] = v
		}
	}
	if p.ClearTitle && p.Title == nil {
		tokens["title"] = nil
	}
	if p.Title != nil {
		t := strings.TrimSpace(*p.Title)
		if t == "" {
			tokens["title"] = nil
		} else {
			tokens["title"] = &t
		}
	}
	if len(tokens) == 0 {
		return map[string]any{"type": "ok"}, "", ""
	}
	if len(tokens) > AgentMetaMaxPerCall {
		return nil, "invalid_params", "one report sets at most 16 tokens"
	}
	if p.TTLMs < 0 || time.Duration(p.TTLMs)*time.Millisecond > AgentMetaMaxTTL {
		return nil, "invalid_params", "ttl_ms must be between 1 and 86400000"
	}
	rawTokens, _ := jsonx.Marshal(tokens)
	params := map[string]any{"session": sessName, "window": window, "tokens": json.RawMessage(rawTokens), "source": herdrMetaSource(p.Source)}
	if p.TTLMs > 0 {
		params["ttl_ms"] = p.TTLMs
	}
	raw, _ := jsonx.Marshal(params)
	if _, verr := d.verbSetAgentMeta(nil, raw); verr != nil {
		return nil, "report_failed", verr.Message
	}
	return map[string]any{"type": "ok"}, "", ""
}

func herdrMetaSource(source string) string {
	if source == "" {
		return "herdr"
	}
	return "herdr:" + source
}

func (d *Daemon) herdrNotify(window string, p herdrParams) (any, string, string) {
	title := ""
	if p.Title != nil {
		title = strings.TrimSpace(*p.Title)
	}
	if title == "" {
		return nil, "invalid_params", "a notification needs a title"
	}
	sess := d.sessionHoldingWindow(window)
	if sess == nil {
		return nil, "pane_not_found", "no pane " + window
	}
	ptyID := ""
	st := sess.GetState()
	for i := range st.Windows {
		if st.Windows[i].ID == window {
			ptyID = st.Windows[i].PTYID
		}
	}
	n := paneNotification{title: capNotifyText(title), body: capNotifyText(strings.TrimSpace(p.Body))}
	sess.emit(SessionEvent{Type: EventNotification, Window: window, PTYID: ptyID, Title: n.title, Body: n.body})
	if ptyID != "" {
		sess.applyAgentNotify(ptyID, n, d.agentMatcher.registry)
	}
	return map[string]any{"type": "ok"}, "", ""
}

func (d *Daemon) herdrReport(sess, window, harness string, pid int, p herdrParams, own bool) (any, string, string) {
	msg := ""
	if p.Message != nil {
		msg = strings.TrimSpace(*p.Message)
	}
	sid := ""
	if p.AgentSessionID != nil {
		sid = *p.AgentSessionID
	}
	switch p.State {
	case "working":
		_, code, text := d.herdrSetState(sess, window, harness, "working", msg, sid, "", pid, own)
		if code != "" {
			return nil, code, text
		}
	case "blocked":
		kind := herdrBlockedKind(harness, msg)
		if msg == "" {
			msg = "waits for you"
			if kind == harnessKindApproval {
				msg = herdrBlockedPlaceholder
			}
		}
		_, code, text := d.herdrSetStateKind(sess, window, harness, "needs_input", kind, msg, sid, "", pid, own)
		if code != "" {
			return nil, code, text
		}
	case "idle":
		reason, code, text := d.herdrSetState(sess, window, harness, "done", "", sid, "working,needs_input", pid, own)
		if code != "" {
			return nil, code, text
		}
		if reason == agentRefusedIfState {
			if _, code, text := d.herdrSetState(sess, window, harness, "idle", "", sid, "", pid, own); code != "" {
				return nil, code, text
			}
		}
	case "unknown":
	default:
		return nil, "invalid_params", "unknown state " + echoName(p.State)
	}
	return map[string]any{"type": "ok"}, "", ""
}

const herdrBlockedPlaceholder = "waits for approval"

const (
	harnessKindApproval = "approval"
	harnessKindQuestion = "question"
)

func herdrBlockedKind(harness, msg string) string {
	if harness != "crush" {
		return ""
	}
	if msg == "" || strings.HasPrefix(msg, "Permission") {
		return harnessKindApproval
	}
	return harnessKindQuestion
}

func (d *Daemon) herdrSetState(sess, window, harness, state, msg, sid, ifState string, pid int, own bool) (string, string, string) {
	return d.herdrSetStateKind(sess, window, harness, state, "", msg, sid, ifState, pid, own)
}

func (d *Daemon) herdrSetStateKind(sess, window, harness, state, kind, msg, sid, ifState string, pid int, own bool) (string, string, string) {
	params := map[string]any{"session": sess, "window": window, "state": state}
	if kind != "" {
		params["kind"] = kind
	}
	if harness != "" {
		params["harness"] = harness
	}
	if msg != "" {
		params["message"] = msg
	}
	if sid != "" {
		params["agent_session_id"] = sid
		if pid > 1 {
			params["harness_pid"] = pid
		}
	}
	if ifState != "" {
		params["if_state"] = ifState
	}
	raw, _ := jsonx.Marshal(params)
	out, verr := d.setAgentState(nil, raw, own)
	if verr != nil {
		return "", "report_failed", verr.Message
	}
	if m, ok := out.(map[string]any); ok {
		if r, ok := m["reason"].(string); ok {
			return r, "", ""
		}
	}
	return "", "", ""
}
