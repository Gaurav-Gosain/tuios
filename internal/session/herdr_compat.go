package session

// herdr's pane state protocol, accepted as an input, and herdr's socket API.
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
// The same socket answers herdr's socket API for tools built on herdr:
// Collie's herdr adapter, herdr plugins, bar widgets and editor bridges. That
// part is herdr_api.go and herdr_events.go. This file carries the transport
// and the pane report methods.
//
// tuios's own contract stays set-agent-state on the daemon socket (see
// docs/AGENT_STATE.md). This is a second door into the same report path, and
// it is kept apart from herdr's own so a real herdr on the same machine is
// never confused:
//
//   - It is a socket of tuios's own, beside the daemon's (HerdrSocketPath),
//     never herdr's path. herdr reads HERDR_SOCKET_PATH as the path of its own
//     server, and HERDR_ENV=1 as "inside herdr", which makes herdr refuse to
//     start nested. So tuios sets the variables only in a pane where they
//     are wanted: one that starts a harness known to report this way, or every
//     pane when [agents] herdr_protocol = "always" asks for it. A shell pane
//     is not told it is a herdr pane.
//   - A pane never inherits an outer herdr's HERDR_ENV or pane ids from the
//     daemon's environment (guestenv.WithoutHostMultiplexer), so an agent in a
//     tuios pane cannot report to the herdr pane tuios itself runs in.
//   - A method herdr has and tuios cannot map answers herdr's error shape with
//     code "unsupported", and a method herdr does not have answers as herdr
//     does, with invalid_request. A pong and a snapshot report herdr's
//     version with "+tuios" and server "tuios", so a client can tell.
//
// A report may speak only for the caller's own pane. The daemon places the
// process on the other end of the connection the way it places every caller
// (peerPane: the kernel's peer pid, its ancestors, its terminal, and last its
// TUIOS_PANE_ID) and refuses a pane_id that is not that pane. A process
// outside every pane, or one the daemon cannot place, is refused a report.
// The API methods hold a caller to what the same tuios verb would: see
// herdr_api.go.
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
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net"
	"os"
	"runtime/debug"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/integration"
	"github.com/Gaurav-Gosain/tuios/internal/procinfo"
)

// herdrMaxRequest bounds one request line, as herdr's 1 MiB cap does. A
// report is a few hundred bytes; a pane.send_text can be a long paste.
const herdrMaxRequest = 1 << 20

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

// herdrConnsPerCaller and herdrStreamsPerCaller bound what one process may
// hold open on the herdr socket at once: connections of any kind, and of
// those the event streams. A wait holds its connection for as long as it
// waits, so without a bound one process could pin a goroutine and an event
// subscription each for as many connections as it opened. A caller is its
// peer pid; every caller the kernel names no pid for shares one count.
const (
	herdrConnsPerCaller   = 64
	herdrStreamsPerCaller = 8
)

// herdrConnCount is the connections each caller holds open.
type herdrConnCount struct {
	mu sync.Mutex
	n  map[int]int
}

// take counts one more for key and reports whether it fits under limit.
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

// give counts one fewer for key.
func (c *herdrConnCount) give(key int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.n[key]--; c.n[key] <= 0 {
		delete(c.n, key)
	}
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

// atOrBelow reports whether seq is at or below the highest seen for key. A
// request with no seq, or a key with no mark, is never below.
func (h *herdrSeqs) atOrBelow(key string, seq *uint64) bool {
	if seq == nil {
		return false
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	last, ok := h.high[key]
	return ok && *seq <= last
}

// herdrEventRate and herdrEventBurst bound the notifications and metadata
// reports one pane may send: a burst of herdrEventBurst, then
// herdrEventRate a second. Each one becomes an event or a state push to
// every client, so a pane must not be able to flood them.
const (
	herdrEventRate  = 5.0
	herdrEventBurst = 20.0
	herdrBucketsMax = 4096
)

// herdrBuckets is a token bucket per pane.
type herdrBuckets struct {
	mu sync.Mutex
	b  map[string]*herdrBucket
}

type herdrBucket struct {
	tokens float64
	at     time.Time
}

// take spends one token for window and reports whether there was one.
func (h *herdrBuckets) take(window string, now time.Time) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.b == nil || len(h.b) > herdrBucketsMax {
		h.b = make(map[string]*herdrBucket)
	}
	bk := h.b[window]
	if bk == nil {
		bk = &herdrBucket{tokens: herdrEventBurst, at: now}
		h.b[window] = bk
	}
	bk.tokens = min(herdrEventBurst, bk.tokens+now.Sub(bk.at).Seconds()*herdrEventRate)
	bk.at = now
	if bk.tokens < 1 {
		return false
	}
	bk.tokens--
	return true
}

// herdrYields reports whether a state report from source should give way
// to the harness's own tuios integration in the pane. herdr's own hook
// scripts report with a source that starts with "herdr:". A person who has
// installed both herdr's scripts and tuios's hooks for the same agent has
// two reporters for one pane, and they must not take the pane from each
// other turn by turn. tuios's hook wins: while a report that did not come
// over this socket holds the pane for the same harness, a report from one
// of herdr's scripts is dropped. An agent that reports to herdr by itself
// (Crush, source "crush") is not a herdr script and never yields.
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

// serveHerdr answers one request and closes the connection, or for
// events.subscribe streams events on it until the client closes it.
func (d *Daemon) serveHerdr(conn net.Conn) {
	defer func() { _ = conn.Close() }()
	defer func() {
		if r := recover(); r != nil {
			log.Printf("PANIC in the herdr protocol socket: %v\n%s", r, debug.Stack())
		}
	}()
	_ = conn.SetDeadline(time.Now().Add(herdrIOTimeout))
	cs := &connState{conn: conn, clientID: "herdr-" + newClientID(), done: make(chan struct{}), peerPID: peerPID(conn)}
	if !d.herdrConns.take(cs.peerPID, herdrConnsPerCaller) {
		writeHerdr(conn, "", nil, "rate_limited", "this process has too many connections open on the herdr socket. Close some and try again")
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
	if err := json.Unmarshal(line, &req); err != nil {
		// herdr answers with the id only when it is a string it can read.
		var idOnly struct {
			ID string `json:"id"`
		}
		_ = json.Unmarshal(line, &idOnly)
		writeHerdr(conn, idOnly.ID, nil, "invalid_request", "invalid request: "+err.Error())
		return
	}
	if req.Method == "events.subscribe" {
		// A stream is held open, so a caller holds fewer of them. The key
		// is apart from the connection count's.
		if !d.herdrConns.take(-1-cs.peerPID, herdrStreamsPerCaller) {
			writeHerdr(conn, req.ID, nil, "rate_limited", "this process has too many event streams open. Close one and subscribe again")
			return
		}
		defer d.herdrConns.give(-1 - cs.peerPID)
		d.serveHerdrEvents(cs, req.ID, req.Params)
		return
	}
	// A wait runs as long as its own timeout says; the other methods answer
	// at once.
	_ = conn.SetDeadline(time.Time{})
	result, code, msg := d.herdrCall(cs, req)
	_ = conn.SetWriteDeadline(time.Now().Add(herdrIOTimeout))
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

// herdrReportMethods are the methods a pane reports its own agent with.
var herdrReportMethods = []string{
	"pane.report_agent", "pane.report_agent_session", "pane.release_agent",
	"pane.report_metadata", "notification.show",
}

// herdrCall carries out one request: a result, or an error code and message.
func (d *Daemon) herdrCall(cs *connState, req herdrRequest) (any, string, string) {
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
	if req.Method == "notification.show" || req.Method == "pane.report_metadata" {
		if !d.herdrEvents.take(window, time.Now()) {
			return nil, "rate_limited", "this pane sends notifications and metadata too fast; wait and send again"
		}
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
	if req.Method == "pane.report_metadata" && d.herdrSeqs.atOrBelow(window+"\x00"+p.Source, p.Seq) {
		// Crush numbers state and metadata from one counter. Metadata at or
		// below the source's last state report, the release included, was
		// sent before it, and must not bring back what the release cleared.
		return map[string]any{"type": "ok"}, "", ""
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
		if d.herdrYields(window, harness, p.Source) {
			return map[string]any{"type": "ok"}, "", ""
		}
		out, code, msg := d.herdrReport(sess, window, harness, pid, p)
		if code == "" {
			d.markHerdrClaim(window, d.herdrAnchorsFor(cs, window))
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
		if d.herdrYields(window, harness, p.Source) {
			return map[string]any{"type": "ok"}, "", ""
		}
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
// harness it knows, or else the label itself, lower-cased, spaces turned to
// hyphens, and cut to letters, digits, '-' and '_' and 32 bytes, so an agent
// tuios has never heard of still shows under its own name and a label can
// never be a path.
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

// herdrPane places the caller in its pane and checks pane_id names it. The
// id is herdr's form (HERDR_PANE_ID) or the pane's tuios window id.
func (d *Daemon) herdrPane(cs *connState, paneID string) (string, string, string) {
	fromPane, window := d.peerPane(cs)
	if !fromPane || window == "" {
		return "", "forbidden", "the caller runs in no pane of this tuios"
	}
	// The caller's own window by herdr's id: the part after ":p" names it.
	// A scratch terminal, which herdr's lists leave out, reports this way
	// too.
	if _, part, ok := strings.Cut(paneID, ":p"); paneID != window && (!ok || part != herdrHex(window)) {
		return "", "forbidden", "a pane may report only for itself"
	}
	return window, "", ""
}

// markHerdrClaim records that the pane's state came from a herdr reporter,
// so the pane clears when it is back at its shell prompt. A harness that
// crashes sends no pane.release_agent, and without this its last report,
// working as often as not, would stand for as long as the pane lives. herdr
// has the same safety net. See detectionPass.
func (d *Daemon) markHerdrClaim(window string, anchors []herdrAnchor) {
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
	sess.agentClaims[window] = claim
}

// herdrAnchorsFor is the processes a herdr claim from cs follows: the
// reporter itself, and the first program above it that is not a shell,
// below the pane's own shell. Crush reports from its own process, so the
// first is Crush. A hook script (herdr's own for Claude Code, say) is gone
// a moment after it reports, so the second is the agent that ran it. A
// wrapper shell between the two (sh -c, a script) is passed over, because
// it can outlive the harness it started. Nil when the reporter cannot be
// read.
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
	for depth := 0; depth < paneOriginMaxDepth; depth++ {
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
