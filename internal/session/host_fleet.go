package session

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/federation"
)

// The fleet: this daemon following the agents of every linked host.
//
// The rail used to poll every host's session list every five seconds while it
// was open and every thirty while it was not, the listing named only each
// host's most recently active session, a transition on another machine raised
// no alert, and a host whose link went down disappeared from the rail with
// whatever its agents were waiting on.
//
// Now the hub keeps one stream per linked host. For each host whose link is up
// it opens a connection to that host's daemon over the link, lists the host's
// Inbox, and subscribes to the host's attention, agent and session events from
// the position the listing was current to. From then on:
//
//   - The host's Inbox items are mirrored into this daemon's Inbox with host
//     set (attention_hosts.go), so list-attention and the client's Inbox cover
//     every machine, and a new item there raises the same alert a local one
//     does.
//   - Any agent or session change on the host refreshes a cached copy of the
//     host's session and agent listings and publishes host-changed, which is
//     what a client listens for instead of polling.
//   - agent-state, session-created and session-closed are relayed onto this
//     daemon's stream with host set, for a subscriber that asks with hosts.
//
// When the link drops the mirrored items are marked stale with when the host
// was last heard from, and the listings answer from the cache with stale set,
// so the rail keeps the host's rows, dimmed, rather than dropping them. On a
// redial the stream resumes from the last seq it delivered (the host's replay
// ring), and relists when the host says the replay cannot be exact.
//
// A host whose tuios predates the Inbox or the resumable stream is followed by
// polling, as every host was before: list-hosts says so in events and
// events_note, and the client keeps its poll for that host.
//
// Everything a host sends is data from another machine. Lines are bounded,
// items are cleaned and capped per host, relayed events carry only their
// identifying fields, and nothing a host sends here reaches a pane, a hook or a
// command.

// Fleet event modes, the events field of list-hosts.
const (
	fleetEventsLive    = "live"
	fleetEventsPolling = "polling"
)

// fleetEventTypes are what the hub subscribes to on each host.
var fleetEventTypes = []string{
	EventAttention, EventAgentState, EventAgentMessage,
	EventSessionCreated, EventSessionClosed, EventWindowCreated, EventWindowClosed,
}

// fleetRelayed are the host events copied onto this daemon's stream.
var fleetRelayed = map[string]bool{EventAgentState: true, EventSessionCreated: true, EventSessionClosed: true}

const (
	// fleetMaxLine bounds one line from a host's stream. An event is a few
	// hundred bytes; a listing of a full Inbox is well under this.
	fleetMaxLine = 1 << 20
	// fleetCallBudget bounds each call the fleet makes on a host.
	fleetCallBudget = 10 * time.Second
	// fleetRefreshDelay gathers a burst of host events into one refresh of the
	// cached listings, so sixteen agents finishing together cost one round
	// trip and one host-changed.
	fleetRefreshDelay = 300 * time.Millisecond
	// fleetUpPoll is how often a pump waiting for its host's link looks again
	// when no status change woke it. The link reports every change, so this
	// is only the backstop.
	fleetUpPoll = 5 * time.Second
	// fleetRetry is how long a pump waits before following its host again
	// after the stream failed with the link still up.
	fleetRetry = 2 * time.Second
	// fleetPollingRetry is how long a pump waits before trying again to
	// stream from a host whose tuios could not, in case it was upgraded.
	fleetPollingRetry = time.Minute
	// fleetMaxSessions and fleetMaxAgents bound what the cache keeps per host.
	fleetMaxSessions = 512
	fleetMaxAgents   = 2048
)

// errFleetOld reports a host whose tuios cannot stream its agents.
type errFleetOld struct{ note string }

func (e errFleetOld) Error() string { return e.note }

// errFleetResync ends a stream so the next one relists from scratch.
var errFleetResync = errors.New("the host's stream has a gap, so its Inbox is listed again")

// hostFleet holds one pump per linked host.
type hostFleet struct {
	d *Daemon

	mu      sync.Mutex
	hosts   map[string]*fleetHost
	ctx     context.Context
	started bool
	wg      sync.WaitGroup

	// pushHosts and pushTimer gather host changes into one MsgHostsChanged to
	// the attached clients, so a host coming up, being listed and settling is
	// one relisting in each client rather than three.
	pushHosts map[string]bool
	pushTimer *time.Timer
}

// fleetPushDelay is how long host changes gather before the clients are told.
const fleetPushDelay = 250 * time.Millisecond

// fleetHost is what the hub knows about one host's agents.
type fleetHost struct {
	name   string
	cancel context.CancelFunc
	wake   chan struct{}

	// The fields below are guarded by hostFleet.mu.
	events string
	note   string
	// seenAt is when the host last said anything on the stream or answered a
	// listing.
	seenAt time.Time
	// bootID and cursor are where the stream is: the last seq delivered, and
	// the daemon start it belongs to. cursorOK says a resume from them is
	// worth trying.
	bootID   string
	cursor   uint64
	cursorOK bool

	sessions   []remoteSessionListRow
	sessionsAt time.Time
	agents     []remoteAgentRow
	agentsAt   time.Time

	refreshTimer *time.Timer
}

func newHostFleet(d *Daemon) *hostFleet {
	return &hostFleet{d: d, hosts: make(map[string]*fleetHost)}
}

// start begins following every host in the table. It is called once the links
// have been started.
func (f *hostFleet) start(ctx context.Context) {
	if f == nil || f.d.federation == nil {
		return
	}
	f.mu.Lock()
	f.ctx = ctx
	f.started = true
	f.mu.Unlock()
	f.reconcile(f.d.federation.Table().Names(), nil)
}

// stop ends every pump and waits for them.
func (f *hostFleet) stop() {
	if f == nil {
		return
	}
	f.mu.Lock()
	f.started = false
	if f.pushTimer != nil {
		f.pushTimer.Stop()
		f.pushTimer = nil
	}
	for _, h := range f.hosts {
		h.cancel()
		if h.refreshTimer != nil {
			h.refreshTimer.Stop()
		}
	}
	f.mu.Unlock()
	f.wg.Wait()
}

// reconcile starts a pump for every name without one and ends the pumps of
// names that are gone. reset names hosts that now point at another machine:
// what was known about the old one is dropped and the pump starts over.
func (f *hostFleet) reconcile(names, reset []string) {
	if f == nil {
		return
	}
	var dropped []string
	f.mu.Lock()
	if !f.started {
		f.mu.Unlock()
		return
	}
	for name, h := range f.hosts {
		if slices.Contains(names, name) && !slices.Contains(reset, name) {
			continue
		}
		h.cancel()
		if h.refreshTimer != nil {
			h.refreshTimer.Stop()
		}
		delete(f.hosts, name)
		dropped = append(dropped, name)
	}
	for _, name := range names {
		if _, ok := f.hosts[name]; ok {
			continue
		}
		ctx, cancel := context.WithCancel(f.ctx)
		h := &fleetHost{name: name, cancel: cancel, wake: make(chan struct{}, 1)}
		f.hosts[name] = h
		f.wg.Add(1)
		go func() {
			defer f.wg.Done()
			f.run(ctx, h)
		}()
	}
	f.mu.Unlock()
	for _, name := range dropped {
		f.d.attention.hostDrop(name, AttentionClosedHostRemoved)
	}
}

// onStatus is the link layer's report of a status change. It wakes the host's
// pump and tells the clients.
func (f *hostFleet) onStatus(host string, status federation.Status) {
	if f == nil {
		return
	}
	f.mu.Lock()
	h := f.hosts[host]
	f.mu.Unlock()
	if h != nil {
		select {
		case h.wake <- struct{}{}:
		default:
		}
	}
	f.publishChanged(host, status)
}

// publishChanged puts a host-changed event on this daemon's stream.
func (f *hostFleet) publishChanged(host string, status federation.Status) {
	if status == "" && f.d.federation != nil {
		if r, ok := f.d.federation.Report(host); ok {
			status = r.Status
		}
	}
	f.d.events.publish(streamEvent{Type: EventHostChanged, Host: host, Status: string(status)})
	f.schedulePush(host)
}

// schedulePush arranges one MsgHostsChanged to the attached clients naming
// host, gathered with any other host that changes within fleetPushDelay.
func (f *hostFleet) schedulePush(host string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.started {
		return
	}
	if f.pushHosts == nil {
		f.pushHosts = make(map[string]bool)
	}
	f.pushHosts[host] = true
	if f.pushTimer != nil {
		return
	}
	f.pushTimer = time.AfterFunc(fleetPushDelay, func() {
		f.mu.Lock()
		hosts := make([]string, 0, len(f.pushHosts))
		for h := range f.pushHosts {
			hosts = append(hosts, h)
		}
		f.pushHosts, f.pushTimer = nil, nil
		live := f.started
		f.mu.Unlock()
		if !live || len(hosts) == 0 {
			return
		}
		slices.Sort(hosts)
		f.d.broadcastHostsChanged(federation.TableChange{}, hosts)
	})
}

// hostUp reports whether a host's link is up now.
func (f *hostFleet) hostUp(host string) bool {
	r, ok := f.d.federation.Report(host)
	return ok && r.Status == federation.StatusUp
}

// run is one host's pump: wait for the link, follow the host until the stream
// ends, mark what it knew stale, and go again.
func (f *hostFleet) run(ctx context.Context, h *fleetHost) {
	for ctx.Err() == nil {
		if !f.hostUp(h.name) {
			select {
			case <-ctx.Done():
				return
			case <-h.wake:
			case <-time.After(fleetUpPoll):
			}
			continue
		}
		err := f.follow(ctx, h)
		if ctx.Err() != nil {
			return
		}
		wait := fleetRetry
		var old errFleetOld
		switch {
		case errors.As(err, &old):
			f.setMode(h, fleetEventsPolling, old.note)
			wait = fleetPollingRetry
		case errors.Is(err, errFleetResync):
			wait = 0
		default:
			f.lost(h)
		}
		if wait == 0 {
			continue
		}
		select {
		case <-ctx.Done():
			return
		case <-h.wake:
		case <-time.After(wait):
		}
	}
}

// setMode records how the host is followed and tells the clients when that
// changed.
func (f *hostFleet) setMode(h *fleetHost, mode, note string) {
	f.mu.Lock()
	changed := h.events != mode || h.note != note
	h.events, h.note = mode, note
	f.mu.Unlock()
	if changed {
		f.publishChanged(h.name, "")
	}
}

// lost is the stream ending with the host's link down or broken. What the hub
// knew is kept, marked stale.
func (f *hostFleet) lost(h *fleetHost) {
	f.mu.Lock()
	seen := h.seenAt
	f.mu.Unlock()
	f.setMode(h, "", "")
	var at int64
	if !seen.IsZero() {
		at = seen.UnixNano()
	}
	f.d.attention.hostStale(h.name, at)
}

// follow opens a stream to one host and delivers its events until the stream
// ends. It returns errFleetOld for a host that cannot stream.
func (f *hostFleet) follow(ctx context.Context, h *fleetHost) error {
	conn, err := f.d.federation.OpenConnection(ctx, h.name)
	if err != nil {
		return err
	}
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()
	defer func() { _ = conn.Close() }()
	fc := &fleetConn{rw: conn, br: bufio.NewReaderSize(conn, 64<<10)}

	f.mu.Lock()
	boot, cursor, resume := h.bootID, h.cursor, h.cursorOK
	f.mu.Unlock()

	if !resume {
		listing, err := fc.listAttention()
		if err != nil {
			return err
		}
		f.d.attention.hostReplace(h.name, listing.Items)
		boot, cursor = listing.BootID, listing.Seq
	}
	if err := fc.subscribe(cursor, boot); err != nil {
		if resume && !isFleetOld(err) {
			// The host may have restarted with a boot id this side has no
			// record of seq for. A fresh listing settles it.
			f.forgetCursor(h)
			return errFleetResync
		}
		return err
	}

	f.mu.Lock()
	h.bootID, h.cursor, h.cursorOK = boot, cursor, true
	h.seenAt = time.Now()
	f.mu.Unlock()
	if resume {
		f.d.attention.hostFresh(h.name)
	}
	f.setMode(h, fleetEventsLive, "")
	f.scheduleRefresh(h, 0)

	for {
		line, err := fc.readLine()
		if err != nil {
			return err
		}
		var ev fleetEvent
		if json.Unmarshal(line, &ev) != nil {
			continue
		}
		if ev.Type == EventGap {
			// A gap on the host's side: the resume could not be exact, the
			// host restarted, or this stream was read too slowly. The Inbox
			// is listed again rather than guessed at.
			f.forgetCursor(h)
			return errFleetResync
		}
		f.note(h, ev)
	}
}

// forgetCursor drops the resume point, so the next stream lists first.
func (f *hostFleet) forgetCursor(h *fleetHost) {
	f.mu.Lock()
	h.cursorOK = false
	f.mu.Unlock()
}

// fleetEvent is the part of a host's event this daemon reads.
type fleetEvent struct {
	Seq       uint64         `json:"seq"`
	BootID    string         `json:"boot_id"`
	Type      string         `json:"type"`
	Session   string         `json:"session"`
	Window    string         `json:"window"`
	State     string         `json:"state"`
	Action    string         `json:"action"`
	Attention *AttentionItem `json:"attention"`
}

// note applies one event from a host.
func (f *hostFleet) note(h *fleetHost, ev fleetEvent) {
	f.mu.Lock()
	h.seenAt = time.Now()
	if ev.Seq > h.cursor && ev.BootID == h.bootID {
		h.cursor = ev.Seq
	}
	f.mu.Unlock()

	switch ev.Type {
	case EventAttention:
		// Only the host's own items. A host that is a hub itself also
		// streams the items it mirrors from its own hosts, and those are
		// that machine's to report, not this one's to pass along.
		if ev.Attention != nil && ev.Attention.Host == "" {
			f.d.attention.hostApply(h.name, ev.Action, *ev.Attention)
		}
		return
	case EventAgentState, EventAgentMessage, EventSessionCreated, EventSessionClosed, EventWindowCreated, EventWindowClosed:
	default:
		return
	}
	if fleetRelayed[ev.Type] {
		relay := streamEvent{
			Type:    ev.Type,
			Host:    h.name,
			Session: attentionText(ev.Session, 128),
			Window:  attentionText(ev.Window, 128),
			relayed: true,
		}
		if ev.Type == EventAgentState {
			if st, ok := ParseAgentState(ev.State); ok {
				relay.State = st.Name()
			}
		}
		f.d.events.publish(relay)
	}
	f.scheduleRefresh(h, fleetRefreshDelay)
}

// scheduleRefresh arranges one refresh of a host's cached listings after
// delay. A refresh already pending absorbs the request.
func (f *hostFleet) scheduleRefresh(h *fleetHost, delay time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if h.refreshTimer != nil {
		return
	}
	h.refreshTimer = time.AfterFunc(delay, func() {
		f.mu.Lock()
		h.refreshTimer = nil
		live := f.hosts[h.name] == h
		f.mu.Unlock()
		if !live {
			return
		}
		f.refresh(h)
	})
}

// refresh reads a host's session and agent listings into the cache, then tells
// the clients.
func (f *hostFleet) refresh(h *fleetHost) {
	ctx, cancel := context.WithTimeout(f.d.ctx, fleetCallBudget)
	defer cancel()
	if rows, err := f.d.fetchHostSessions(ctx, h.name); err == nil {
		f.storeSessions(h.name, rows)
	}
	if rows, err := f.d.fetchHostAgents(ctx, h.name, false); err == nil {
		f.storeAgents(h.name, rows)
	}
	f.publishChanged(h.name, "")
}

// storeSessions keeps a host's session rows, for when the host cannot be asked.
func (f *hostFleet) storeSessions(host string, rows []remoteSessionListRow) {
	if f == nil {
		return
	}
	if len(rows) > fleetMaxSessions {
		rows = rows[:fleetMaxSessions]
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if h := f.hosts[host]; h != nil {
		h.sessions, h.sessionsAt = rows, time.Now()
		h.seenAt = h.sessionsAt
	}
}

// storeAgents keeps a host's agent rows, for when the host cannot be asked.
func (f *hostFleet) storeAgents(host string, rows []remoteAgentRow) {
	if f == nil {
		return
	}
	if len(rows) > fleetMaxAgents {
		rows = rows[:fleetMaxAgents]
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if h := f.hosts[host]; h != nil {
		h.agents, h.agentsAt = rows, time.Now()
		h.seenAt = h.agentsAt
	}
}

// cachedSessions is a host's last session listing and when it was taken.
func (f *hostFleet) cachedSessions(host string) ([]remoteSessionListRow, time.Time) {
	if f == nil {
		return nil, time.Time{}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if h := f.hosts[host]; h != nil {
		return h.sessions, h.sessionsAt
	}
	return nil, time.Time{}
}

// cachedAgents is a host's last agent listing and when it was taken.
func (f *hostFleet) cachedAgents(host string) ([]remoteAgentRow, time.Time) {
	if f == nil {
		return nil, time.Time{}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if h := f.hosts[host]; h != nil {
		return h.agents, h.agentsAt
	}
	return nil, time.Time{}
}

// mode is how a host is followed, for list-hosts and the host listings.
func (f *hostFleet) mode(host string) (events, note string) {
	if f == nil {
		return "", ""
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if h := f.hosts[host]; h != nil {
		return h.events, h.note
	}
	return "", ""
}

// fleetConn speaks the verb protocol on one connection to a host's daemon:
// calls until the subscribe, then event lines.
type fleetConn struct {
	rw     io.ReadWriteCloser
	br     *bufio.Reader
	nextID int
}

// call sends one request and reads its answer. The caller bounds it by closing
// the connection, which is what the pump's context does.
func (c *fleetConn) call(verb string, params any) (json.RawMessage, error) {
	c.nextID++
	raw, err := json.Marshal(params)
	if err != nil {
		return nil, err
	}
	req, err := json.Marshal(verbRequest{ID: json.RawMessage(fmt.Sprint(c.nextID)), Verb: verb, Params: raw})
	if err != nil {
		return nil, err
	}
	if _, err := c.rw.Write(append(req, '\n')); err != nil {
		return nil, err
	}
	// A host that takes the request and never answers would hold the pump
	// forever. Closing the connection ends the read, and the pump starts over.
	timer := time.AfterFunc(fleetCallBudget, func() { _ = c.rw.Close() })
	defer timer.Stop()
	line, err := c.readLine()
	if err != nil {
		return nil, err
	}
	var resp struct {
		Result json.RawMessage `json:"result"`
		Error  *verbError      `json:"error"`
	}
	if err := json.Unmarshal(line, &resp); err != nil {
		return nil, fmt.Errorf("the host sent an answer this build cannot read: %w", err)
	}
	if resp.Error != nil {
		return nil, resp.Error
	}
	return resp.Result, nil
}

// readLine reads one bounded line.
func (c *fleetConn) readLine() ([]byte, error) {
	return readLimitedLine(c.br, fleetMaxLine)
}

// fleetListing is a host's list-attention answer.
type fleetListing struct {
	Items  []AttentionItem `json:"items"`
	Seq    uint64          `json:"seq"`
	BootID string          `json:"boot_id"`
}

// listAttention lists a host's Inbox. A host that has none is errFleetOld.
func (c *fleetConn) listAttention() (fleetListing, error) {
	raw, err := c.call("list-attention", map[string]any{"host": localAttentionHost})
	var verr *verbError
	if errors.As(err, &verr) && verr.Code == ErrVerbInvalidParams {
		// A host from before items of other machines were listed takes no
		// host param, and lists only its own items anyway.
		raw, err = c.call("list-attention", map[string]any{})
	}
	if err != nil {
		if errors.As(err, &verr) && verr.Code == ErrVerbUnknownVerb {
			return fleetListing{}, errFleetOld{note: "The tuios on this host has no Inbox, so its agents are polled and what waits there is not shown here. Update tuios on the host and restart its daemon."}
		}
		return fleetListing{}, err
	}
	var l fleetListing
	if err := json.Unmarshal(raw, &l); err != nil {
		return fleetListing{}, fmt.Errorf("the host sent an Inbox this build cannot read: %w", err)
	}
	if l.BootID == "" {
		return fleetListing{}, errFleetOld{note: "The tuios on this host cannot resume its event stream, so its agents are polled. Update tuios on the host and restart its daemon."}
	}
	return l, nil
}

// subscribe turns the connection into the host's event stream, from after seq
// of boot.
func (c *fleetConn) subscribe(after uint64, boot string) error {
	_, err := c.call("subscribe", map[string]any{
		"types":     fleetEventTypes,
		"after_seq": after,
		"boot_id":   boot,
		"queue":     1024,
	})
	var verr *verbError
	if errors.As(err, &verr) && verr.Code == ErrVerbInvalidParams && verr.Hint != nil && (verr.Hint.Param == "after_seq" || verr.Hint.Param == "boot_id") && strings.Contains(verr.Message, "has no parameter") {
		return errFleetOld{note: "The tuios on this host cannot resume its event stream, so its agents are polled. Update tuios on the host and restart its daemon."}
	}
	return err
}

// isFleetOld reports whether err says the host cannot stream.
func isFleetOld(err error) bool {
	var old errFleetOld
	return errors.As(err, &old)
}
