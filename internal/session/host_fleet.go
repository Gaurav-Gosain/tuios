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

	"github.com/Gaurav-Gosain/tuios/internal/errfmt"
	"github.com/Gaurav-Gosain/tuios/internal/federation"
	"github.com/Gaurav-Gosain/tuios/internal/jsonx"
)

const (
	fleetEventsLive    = "live"
	fleetEventsPolling = "polling"
)

var fleetEventTypes = []string{
	EventAttention, EventAgentState, EventAgentMessage,
	EventSessionCreated, EventSessionClosed, EventWindowCreated, EventWindowClosed,
}

var fleetRelayed = map[string]bool{EventAgentState: true, EventSessionCreated: true, EventSessionClosed: true}

const (
	fleetMaxLine      = 1 << 20
	fleetCallBudget   = 10 * time.Second
	fleetRefreshDelay = 300 * time.Millisecond
	fleetUpPoll       = 5 * time.Second
	fleetRetry        = 2 * time.Second
	fleetPollingRetry = time.Minute
	fleetMaxSessions  = 512
	fleetMaxAgents    = 2048
)

type errFleetOld struct{ note string }

func (e errFleetOld) Error() string { return e.note }

var errFleetResync = errors.New("the host's stream has a gap, so its Inbox is listed again")

type hostFleet struct {
	d *Daemon

	mu      sync.Mutex
	hosts   map[string]*fleetHost
	ctx     context.Context
	started bool
	wg      sync.WaitGroup

	pushHosts map[string]bool
	pushTimer *time.Timer
}

const fleetPushDelay = 250 * time.Millisecond

type fleetHost struct {
	name   string
	cancel context.CancelFunc
	wake   chan struct{}

	events   string
	note     string
	seenAt   time.Time
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

func (f *hostFleet) start(ctx context.Context) {
	if f == nil || f.d.federation == nil {
		return
	}
	f.mu.Lock()
	f.ctx = ctx
	f.started = true
	for _, h := range f.hosts {
		f.pump(h)
	}
	f.mu.Unlock()
	f.reconcile(f.d.federation.Table().Names(), nil)
}

func (f *hostFleet) pump(h *fleetHost) {
	ctx, cancel := context.WithCancel(f.ctx)
	h.cancel = cancel
	f.wg.Go(func() {
		f.run(ctx, h)
	})
}

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
		h := &fleetHost{name: name, wake: make(chan struct{}, 1)}
		f.hosts[name] = h
		f.pump(h)
	}
	f.mu.Unlock()
	for _, name := range dropped {
		f.d.attention.hostDrop(name, AttentionClosedHostRemoved)
	}
}

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
	if status == federation.StatusUp {
		f.d.outbox.kick(host)
	}
	if f.d.outbox.count(host) > 0 {
		f.d.outbox.noteAttention(host)
	}
	f.publishChanged(host, status)
}

func (f *hostFleet) publishChanged(host string, status federation.Status) {
	if status == "" && f.d.federation != nil {
		if r, ok := f.d.federation.Report(host); ok {
			status = r.Status
		}
	}
	f.d.events.publish(streamEvent{Type: EventHostChanged, Host: host, Status: string(status)})
	f.schedulePush(host)
}

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

func (f *hostFleet) hostUp(host string) bool {
	r, ok := f.d.federation.Report(host)
	return ok && r.Status == federation.StatusUp
}

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

func (f *hostFleet) setMode(h *fleetHost, mode, note string) {
	f.mu.Lock()
	changed := h.events != mode || h.note != note
	h.events, h.note = mode, note
	f.mu.Unlock()
	if changed {
		f.publishChanged(h.name, "")
	}
}

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
		if jsonx.Unmarshal(line, &ev) != nil {
			continue
		}
		if ev.Type == EventGap {
			f.forgetCursor(h)
			return errFleetResync
		}
		f.note(h, ev)
	}
}

func (f *hostFleet) forgetCursor(h *fleetHost) {
	f.mu.Lock()
	h.cursorOK = false
	f.mu.Unlock()
}

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

func (f *hostFleet) note(h *fleetHost, ev fleetEvent) {
	f.mu.Lock()
	h.seenAt = time.Now()
	if ev.Seq > h.cursor && ev.BootID == h.bootID {
		h.cursor = ev.Seq
	}
	f.mu.Unlock()

	switch ev.Type {
	case EventAttention:
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

type fleetConn struct {
	rw     io.ReadWriteCloser
	br     *bufio.Reader
	nextID int
}

func (c *fleetConn) call(verb string, params any, budget time.Duration) (json.RawMessage, error) {
	c.nextID++
	raw, err := jsonx.Marshal(params)
	if err != nil {
		return nil, err
	}
	req, err := jsonx.Marshal(verbRequest{ID: json.RawMessage(fmt.Sprint(c.nextID)), Verb: verb, Params: raw})
	if err != nil {
		return nil, err
	}
	if _, err := c.rw.Write(append(req, '\n')); err != nil {
		return nil, err
	}
	timer := time.AfterFunc(budget, func() { _ = c.rw.Close() })
	defer timer.Stop()
	line, err := c.readLine()
	if err != nil {
		return nil, err
	}
	var resp struct {
		Result json.RawMessage `json:"result"`
		Error  *verbError      `json:"error"`
	}
	if err := jsonx.Unmarshal(line, &resp); err != nil {
		return nil, errfmt.Errorf("the host sent an answer this build cannot read: %w", err)
	}
	if resp.Error != nil {
		return nil, resp.Error
	}
	return resp.Result, nil
}

func (c *fleetConn) readLine() ([]byte, error) {
	return readLimitedLine(c.br, fleetMaxLine)
}

type fleetListing struct {
	Items  []AttentionItem `json:"items"`
	Seq    uint64          `json:"seq"`
	BootID string          `json:"boot_id"`
}

func (c *fleetConn) listAttention() (fleetListing, error) {
	raw, err := c.call("list-attention", map[string]any{"host": localAttentionHost}, fleetCallBudget)
	var verr *verbError
	if errors.As(err, &verr) && verr.Code == ErrVerbInvalidParams {
		raw, err = c.call("list-attention", map[string]any{}, fleetCallBudget)
	}
	if err != nil {
		if errors.As(err, &verr) && verr.Code == ErrVerbUnknownVerb {
			return fleetListing{}, errFleetOld{note: "The tuios on this host has no Inbox, so its agents are polled and what waits there is not shown here. Update tuios on the host and restart its daemon."}
		}
		return fleetListing{}, err
	}
	var l fleetListing
	if err := jsonx.Unmarshal(raw, &l); err != nil {
		return fleetListing{}, errfmt.Errorf("the host sent an Inbox this build cannot read: %w", err)
	}
	if l.BootID == "" {
		return fleetListing{}, errFleetOld{note: "The tuios on this host cannot resume its event stream, so its agents are polled. Update tuios on the host and restart its daemon."}
	}
	return l, nil
}

func (c *fleetConn) subscribe(after uint64, boot string) error {
	_, err := c.call("subscribe", map[string]any{
		"types":     fleetEventTypes,
		"after_seq": after,
		"boot_id":   boot,
		"queue":     1024,
	}, fleetCallBudget)
	var verr *verbError
	if errors.As(err, &verr) && verr.Code == ErrVerbInvalidParams && verr.Hint != nil && (verr.Hint.Param == "after_seq" || verr.Hint.Param == "boot_id") && strings.Contains(verr.Message, "has no parameter") {
		return errFleetOld{note: "The tuios on this host cannot resume its event stream, so its agents are polled. Update tuios on the host and restart its daemon."}
	}
	return err
}

func isFleetOld(err error) bool {
	var old errFleetOld
	return errors.As(err, &old)
}
