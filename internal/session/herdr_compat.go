package session

// herdr's pane state protocol, accepted as an input.
//
// herdr (github.com/herdrdev/herdr) is another terminal multiplexer for
// coding agents. Its panes carry HERDR_ENV=1, HERDR_SOCKET_PATH and
// HERDR_PANE_ID, and a harness that finds them reports its state over that
// socket in herdr's JSON-RPC shape: one request per connection, a JSON object
// on one line, answered with one line before the server closes. Crush does
// this natively (internal/herdr/client.go in github.com/charmbracelet/crush),
// with no install step, so accepting the same requests gives a Crush pane its
// exact state.
//
// tuios's own contract stays set-agent-state on the daemon socket (see
// docs/AGENT_STATE.md). This is a second door into the same report path, and
// it is kept apart from herdr's own so a real herdr on the same machine is
// never confused:
//
//   - It is a socket of tuios's own, beside the daemon's (HerdrSocketPath),
//     never herdr's path. herdr reads HERDR_SOCKET_PATH as the path of its own
//     server, and HERDR_ENV=1 as "inside herdr", which makes herdr refuse to
//     start nested. So tuios sets the three variables only in a pane where they
//     are wanted: one that starts a harness known to report this way, or every
//     pane when [agents] herdr_protocol = "always" asks for it. A shell pane
//     is not told it is a herdr pane.
//   - A pane never inherits an outer herdr's HERDR_ENV or pane ids from the
//     daemon's environment (guestenv.WithoutHostMultiplexer), so an agent in a
//     tuios pane cannot report to the herdr pane tuios itself runs in.
//   - Only the requests that report a pane's own agent are answered:
//     pane.report_agent, pane.report_agent_session, pane.release_agent, and
//     ping. Everything else gets herdr's error shape with code
//     "unsupported", so a herdr client that reached this socket by mistake
//     fails plainly instead of being answered as if tuios were herdr.
//
// A request may speak only for the caller's own pane. The daemon places the
// process on the other end of the connection the way it places every caller
// (peerPane: the kernel's peer pid, its ancestors, its terminal, and last its
// TUIOS_PANE_ID) and refuses a pane_id that is not that pane. A process
// outside every pane, or one the daemon cannot place, is refused.
//
// Mapping, from herdr's PaneAgentState (src/api/schema/common.rs):
//
//	working   working
//	blocked   needs_input, with herdr's message when it sends one; for
//	          Crush, which is blocked only on a permission request, kind
//	          approval
//	idle      done when the pane is working or in needs_input, since the
//	          harness went to rest from a turn, and idle otherwise
//	unknown   nothing
//	pane.report_agent_session   set-agent-session
//	pane.release_agent          none
//
// herdr drops a report whose seq is not above the highest it has seen from
// the same source for the pane, and Crush seeds its seq from the clock so a
// restarted Crush is never stale. The same rule applies here.

import (
	"bufio"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net"
	"os"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/integration"
)

// herdrMaxRequest bounds one request line. A report is a few hundred bytes.
const herdrMaxRequest = 64 << 10

// herdrIOTimeout bounds reading the request and writing the answer.
const herdrIOTimeout = 2 * time.Second

// herdrSeqMax bounds the high-water table. Past it the table is cleared,
// which at worst accepts one stale report per pane.
const herdrSeqMax = 4096

// HerdrSocketPath is the socket tuios accepts herdr's pane state protocol on,
// beside the daemon's own socket.
func HerdrSocketPath(socketPath string) string {
	return socketPath + ".herdr"
}

// herdrRequest is one JSON-RPC request in herdr's shape.
type herdrRequest struct {
	ID     string          `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
}

// herdrParams are the params of the requests tuios answers.
type herdrParams struct {
	PaneID         string  `json:"pane_id"`
	Source         string  `json:"source"`
	Agent          string  `json:"agent"`
	State          string  `json:"state"`
	Message        *string `json:"message"`
	Seq            *uint64 `json:"seq"`
	AgentSessionID *string `json:"agent_session_id"`
	// ResumeArgv is herdr's resume command (herdr 0.9.2 and later). tuios
	// resumes a conversation from the harness and session id instead, so
	// the field is accepted and not used, as an older herdr does.
	ResumeArgv []string `json:"resume_argv"`

	// pane.report_metadata. Title and the tokens are display only; see
	// herdrMetadata.
	Title  *string         `json:"title"`
	Tokens json.RawMessage `json:"tokens"`
	TTLMs  int64           `json:"ttl_ms"`

	// notification.show, which names no pane: the caller's own is used.
	Body string `json:"body"`
}

// herdrSeqs is the highest seq seen per pane and source.
type herdrSeqs struct {
	mu   sync.Mutex
	high map[string]uint64
}

// fresh reports whether seq is above the highest seen for key, and records
// it when it is. A request with no seq is always fresh.
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

// listenHerdrSocket opens the herdr protocol socket, owner only, and returns
// nil after logging when it cannot. The start lock is held, so a stale file
// at the path is a dead daemon's and is removed.
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

// acceptHerdrLoop serves the herdr protocol socket until the daemon stops.
func (d *Daemon) acceptHerdrLoop(l net.Listener) {
	for {
		conn, err := l.Accept()
		if err != nil {
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

// serveHerdr answers one request and closes the connection.
func (d *Daemon) serveHerdr(conn net.Conn) {
	defer func() { _ = conn.Close() }()
	_ = conn.SetDeadline(time.Now().Add(herdrIOTimeout))
	cs := &connState{conn: conn, peerPID: peerPID(conn)}
	d.pinPeer(cs)
	line, err := bufio.NewReaderSize(io.LimitReader(conn, herdrMaxRequest), 4096).ReadBytes('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return
	}
	var req herdrRequest
	if err := json.Unmarshal(line, &req); err != nil {
		writeHerdr(conn, "", nil, "invalid_request", "the request is not a JSON object")
		return
	}
	result, code, msg := d.herdrCall(cs, req)
	writeHerdr(conn, req.ID, result, code, msg)
}

// writeHerdr writes a success or an error in herdr's response shape.
func writeHerdr(w io.Writer, id string, result any, code, msg string) {
	var out any
	if code != "" {
		out = map[string]any{"id": id, "error": map[string]string{"code": code, "message": msg}}
	} else {
		out = map[string]any{"id": id, "result": result}
	}
	data, err := json.Marshal(out)
	if err != nil {
		return
	}
	_, _ = w.Write(append(data, '\n'))
}

// herdrCall carries out one request: a result, or an error code and message.
func (d *Daemon) herdrCall(cs *connState, req herdrRequest) (any, string, string) {
	switch req.Method {
	case "ping":
		return map[string]any{"type": "pong", "version": "tuios", "protocol": 0}, "", ""
	case "pane.report_agent", "pane.report_agent_session", "pane.release_agent",
		"pane.report_metadata", "notification.show":
	default:
		return nil, "unsupported", "this is tuios, which accepts only pane.report_agent, pane.report_agent_session, pane.release_agent, pane.report_metadata and notification.show here"
	}
	var p herdrParams
	if len(req.Params) > 0 {
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return nil, "invalid_params", "params: " + err.Error()
		}
	}
	if req.Method == "notification.show" {
		// herdr's notification names no pane. The caller's own pane is the
		// one it comes from, placed the same way a report's pane is.
		fromPane, window := d.peerPane(cs)
		if !fromPane || window == "" {
			return nil, "forbidden", "the caller runs in no pane of this tuios"
		}
		p.PaneID = window
	}
	window, code, msg := d.herdrPane(cs, p.PaneID)
	if code != "" {
		return nil, code, msg
	}
	sess := d.sessionOfWindow(window)
	if sess == "" {
		return nil, "pane_not_found", "no pane " + p.PaneID
	}
	if req.Method == "notification.show" {
		return d.herdrNotify(window, p)
	}
	// Metadata has a high-water mark of its own, so a hook that numbers its
	// metadata and its state reports apart is not dropped. Crush numbers
	// both from one counter, which suits either.
	seqKey := window + "\x00" + p.Source
	if req.Method == "pane.report_metadata" {
		seqKey = window + "\x00meta\x00" + p.Source
	}
	if !d.herdrSeqs.fresh(seqKey, p.Seq) {
		// herdr drops a stale report without an error, and so does this.
		return map[string]any{"type": "ok"}, "", ""
	}
	if req.Method == "pane.report_metadata" {
		return d.herdrMetadata(sess, window, p)
	}
	harness := herdrHarness(p.Agent)
	pid := 0
	if cs != nil {
		pid = cs.peerPID
	}
	switch req.Method {
	case "pane.report_agent":
		out, code, msg := d.herdrReport(sess, window, harness, pid, p)
		if code == "" {
			d.markHerdrClaim(window)
		}
		return out, code, msg
	case "pane.report_agent_session":
		if harness == "" || p.AgentSessionID == nil || *p.AgentSessionID == "" {
			return nil, "invalid_params", "a session report needs an agent and an agent_session_id"
		}
		raw, _ := json.Marshal(map[string]any{"session": sess, "window": window, "harness": harness, "agent_session_id": *p.AgentSessionID})
		if _, verr := d.verbSetAgentSession(nil, raw); verr != nil {
			return nil, "report_failed", verr.Message
		}
	case "pane.release_agent":
		// The release carries a seq, recorded above, and the high-water
		// mark stays. A report Crush queued before it quit can reach the
		// socket after the release, and it must not bring the pane back.
		if _, code, msg := d.herdrSetState(sess, window, harness, "none", "", "", "", 0); code != "" {
			return nil, code, msg
		}
	}
	return map[string]any{"type": "ok"}, "", ""
}

// herdrHarness is the harness a herdr agent label names: tuios's id for a
// harness it knows, or else the label itself, cleaned to a short name, so an
// agent tuios has never heard of still shows under its own name.
func herdrHarness(agent string) string {
	if id, ok := integration.Canonical(agent); ok {
		return id
	}
	agent = strings.ToLower(strings.TrimSpace(agent))
	var b strings.Builder
	for _, r := range agent {
		if b.Len() >= 32 {
			break
		}
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-', r == '_', r == '.':
			b.WriteRune(r)
		case r == ' ':
			b.WriteByte('-')
		}
	}
	return b.String()
}

// herdrPane places the caller in its pane and checks pane_id names it.
func (d *Daemon) herdrPane(cs *connState, paneID string) (string, string, string) {
	fromPane, window := d.peerPane(cs)
	switch {
	case !fromPane || window == "":
		return "", "forbidden", "the caller runs in no pane of this tuios"
	case paneID != window:
		return "", "forbidden", "a pane may report only for itself"
	}
	return window, "", ""
}

// markHerdrClaim records that the pane's state came from a herdr reporter,
// so the pane clears when it is back at its shell prompt. A harness that
// crashes sends no pane.release_agent, and without this its last report,
// working as often as not, would stand for as long as the pane lives. herdr
// has the same safety net. See detectionPass.
func (d *Daemon) markHerdrClaim(window string) {
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
	sess.agentClaims[window] = claim
}

// herdrMetadata applies one pane.report_metadata as agent metadata: each
// token, and the title as the title token. It is display only in herdr and
// in tuios alike. herdr's names allow capitals and are up to 32 long, and a
// name tuios cannot hold is skipped rather than failing the report, since
// the rest of it is still worth showing.
func (d *Daemon) herdrMetadata(sessName, window string, p herdrParams) (any, string, string) {
	tokens := map[string]*string{}
	if len(p.Tokens) > 0 && string(p.Tokens) != "null" {
		var raw map[string]*string
		if err := json.Unmarshal(p.Tokens, &raw); err != nil {
			return nil, "invalid_params", "tokens: " + err.Error()
		}
		for k, v := range raw {
			k = strings.ToLower(k)
			if !ValidAgentMetaKey(k) || slices.Contains(reservedAgentMetaKeys, k) {
				continue
			}
			if v != nil && strings.TrimSpace(*v) == "" {
				v = nil // herdr: an empty value clears the key
			}
			tokens[k] = v
		}
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
	rawTokens, _ := json.Marshal(tokens)
	params := map[string]any{"session": sessName, "window": window, "tokens": json.RawMessage(rawTokens), "source": herdrMetaSource(p.Source)}
	if p.TTLMs > 0 {
		params["ttl_ms"] = p.TTLMs
	}
	raw, _ := json.Marshal(params)
	if _, verr := d.verbSetAgentMeta(nil, raw); verr != nil {
		return nil, "report_failed", verr.Message
	}
	return map[string]any{"type": "ok"}, "", ""
}

// herdrMetaSource is the metadata source a herdr reporter's tokens are
// filed under, so they never mix with tuios's own writers'.
func herdrMetaSource(source string) string {
	if source == "" {
		return "herdr"
	}
	return "herdr:" + source
}

// herdrNotify applies one notification.show from a pane the way a desktop
// notification the pane sent over OSC 9 is applied: published on the event
// stream, and matched against the [notify] rules of the harness in the pane.
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

// herdrReport applies one pane.report_agent. pid is the reporting process,
// sent as the harness pid, so a Crush that moves to another conversation
// while it works is the same harness in the pane and not a nested one.
func (d *Daemon) herdrReport(sess, window, harness string, pid int, p herdrParams) (any, string, string) {
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
		_, code, text := d.herdrSetState(sess, window, harness, "working", msg, sid, "", pid)
		if code != "" {
			return nil, code, text
		}
	case "blocked":
		kind := herdrBlockedKind(harness, msg)
		if msg == "" {
			msg = "waits for you"
			if kind == harnessKindApproval {
				msg = "waits for approval"
			}
		}
		_, code, text := d.herdrSetStateKind(sess, window, harness, "needs_input", kind, msg, sid, "", pid)
		if code != "" {
			return nil, code, text
		}
	case "idle":
		// Rest after a turn is a finished turn; rest from anywhere else,
		// Crush's first report included, is idle. The message a reporter
		// sends with idle is empty or stale, and is not kept.
		reason, code, text := d.herdrSetState(sess, window, harness, "done", "", sid, "working,needs_input", pid)
		if code != "" {
			return nil, code, text
		}
		if reason == agentRefusedIfState {
			if _, code, text := d.herdrSetState(sess, window, harness, "idle", "", sid, "", pid); code != "" {
				return nil, code, text
			}
		}
	case "unknown":
	default:
		return nil, "invalid_params", "unknown state " + echoName(p.State)
	}
	return map[string]any{"type": "ok"}, "", ""
}

// harnessKindApproval and harnessKindQuestion are the kinds of a block.
const (
	harnessKindApproval = "approval"
	harnessKindQuestion = "question"
)

// herdrBlockedKind is what a blocked report waits on, "" when the report
// does not say, which leaves the kind to be read from the message as for
// any report.
//
// Crush says. Up to v0.x it reports blocked only for a permission request
// (PermissionRequested in its internal/herdr/client.go) and sends no
// message. From charmbracelet/crush#3541 it sends one with every block:
// "Permission: <tool> - <detail>" or "Permission required" for a permission
// request, the question itself while its question tool waits, and
// "Re-authentication required" when a provider needs a new login. Only the
// first is an approval. The others wait for the person to answer or act.
func herdrBlockedKind(harness, msg string) string {
	if harness != "crush" {
		return ""
	}
	if msg == "" || strings.HasPrefix(msg, "Permission") {
		return harnessKindApproval
	}
	return harnessKindQuestion
}

// herdrSetState reports one state for the pane through set-agent-state,
// returning the refusal reason, if any, or an error code and message.
func (d *Daemon) herdrSetState(sess, window, harness, state, msg, sid, ifState string, pid int) (string, string, string) {
	return d.herdrSetStateKind(sess, window, harness, state, "", msg, sid, ifState, pid)
}

// herdrSetStateKind is herdrSetState with the kind of a needs_input block.
func (d *Daemon) herdrSetStateKind(sess, window, harness, state, kind, msg, sid, ifState string, pid int) (string, string, string) {
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
	raw, _ := json.Marshal(params)
	out, verr := d.verbSetAgentState(nil, raw)
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
