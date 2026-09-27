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

// forget drops every source's high-water mark for a pane.
func (h *herdrSeqs) forget(window string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for k := range h.high {
		if strings.HasPrefix(k, window+"\x00") {
			delete(h.high, k)
		}
	}
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
	case "pane.report_agent", "pane.report_agent_session", "pane.release_agent":
	default:
		return nil, "unsupported", "this is tuios, which accepts only pane.report_agent, pane.report_agent_session and pane.release_agent here"
	}
	var p herdrParams
	if len(req.Params) > 0 {
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return nil, "invalid_params", "params: " + err.Error()
		}
	}
	window, code, msg := d.herdrPane(cs, p.PaneID)
	if code != "" {
		return nil, code, msg
	}
	sess := d.sessionOfWindow(window)
	if sess == "" {
		return nil, "pane_not_found", "no pane " + p.PaneID
	}
	if !d.herdrSeqs.fresh(window+"\x00"+p.Source, p.Seq) {
		// herdr drops a stale report without an error, and so does this.
		return map[string]any{"type": "ok"}, "", ""
	}
	harness, _ := integration.Canonical(p.Agent)
	switch req.Method {
	case "pane.report_agent":
		return d.herdrReport(sess, window, harness, p)
	case "pane.report_agent_session":
		if harness == "" || p.AgentSessionID == nil || *p.AgentSessionID == "" {
			return nil, "invalid_params", "a session report needs a known agent and an agent_session_id"
		}
		raw, _ := json.Marshal(map[string]any{"session": sess, "window": window, "harness": harness, "agent_session_id": *p.AgentSessionID})
		if _, verr := d.verbSetAgentSession(nil, raw); verr != nil {
			return nil, "report_failed", verr.Message
		}
	case "pane.release_agent":
		d.herdrSeqs.forget(window)
		if _, code, msg := d.herdrSetState(sess, window, harness, "none", "", "", ""); code != "" {
			return nil, code, msg
		}
	}
	return map[string]any{"type": "ok"}, "", ""
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

// herdrReport applies one pane.report_agent.
func (d *Daemon) herdrReport(sess, window, harness string, p herdrParams) (any, string, string) {
	msg := ""
	if p.Message != nil {
		msg = *p.Message
	}
	sid := ""
	if p.AgentSessionID != nil {
		sid = *p.AgentSessionID
	}
	switch p.State {
	case "working":
		_, code, text := d.herdrSetState(sess, window, harness, "working", msg, sid, "")
		if code != "" {
			return nil, code, text
		}
	case "blocked":
		kind := herdrBlockedKind[harness]
		if msg == "" {
			msg = "waits for you"
			if kind == harnessKindApproval {
				msg = "waits for approval"
			}
		}
		_, code, text := d.herdrSetStateKind(sess, window, harness, "needs_input", kind, msg, sid, "")
		if code != "" {
			return nil, code, text
		}
	case "idle":
		// Rest after a turn is a finished turn; rest from anywhere else,
		// Crush's first report included, is idle.
		reason, code, text := d.herdrSetState(sess, window, harness, "done", msg, sid, "working,needs_input")
		if code != "" {
			return nil, code, text
		}
		if reason == agentRefusedIfState {
			if _, code, text := d.herdrSetState(sess, window, harness, "idle", msg, sid, ""); code != "" {
				return nil, code, text
			}
		}
	case "unknown":
	default:
		return nil, "invalid_params", "unknown state " + echoName(p.State)
	}
	return map[string]any{"type": "ok"}, "", ""
}

// harnessKindApproval is the kind of a block that waits on an approval.
const harnessKindApproval = "approval"

// herdrBlockedKind is what blocked means for a harness whose herdr reporter
// says: Crush reports blocked only for a permission request
// (PermissionRequested in its internal/herdr/client.go). For any other
// reporter blocked says nothing about the kind, which is then read from the
// message as for any report.
var herdrBlockedKind = map[string]string{"crush": harnessKindApproval}

// herdrSetState reports one state for the pane through set-agent-state,
// returning the refusal reason, if any, or an error code and message.
func (d *Daemon) herdrSetState(sess, window, harness, state, msg, sid, ifState string) (string, string, string) {
	return d.herdrSetStateKind(sess, window, harness, state, "", msg, sid, ifState)
}

// herdrSetStateKind is herdrSetState with the kind of a needs_input block.
func (d *Daemon) herdrSetStateKind(sess, window, harness, state, kind, msg, sid, ifState string) (string, string, string) {
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
