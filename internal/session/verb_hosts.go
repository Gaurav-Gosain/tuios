package session

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/federation"
	"github.com/Gaurav-Gosain/tuios/internal/sessiontree"
)

// The federation control plane's three read verbs.
//
// Every one of them asks a remote daemon for a listing and never tells it to do
// anything. There is no verb here that creates, kills, resizes, writes or
// attaches. Acting on another machine goes through open-host-connection
// (verb_host_connection.go), which relays the client's own connection and
// decodes none of it.
//
// The aggregation rule these three share: hosts that answer are listed, hosts
// that do not are listed with their status and reason, and one dead host never
// fails the call. That is section 7's failure model expressed as a result
// shape, and it is why the result is a list of per-host envelopes rather than a
// flat list that would have nowhere to put a failure.

// ErrVerbHostUnreachable reports a host that is configured and not answering.
// It is final: the caller reports it, and it does not retry into another name.
const ErrVerbHostUnreachable = "host_unreachable"

// ErrVerbUnknownHost reports a host name that is not in the configured table.
// Also final, and its message names every configured host.
const ErrVerbUnknownHost = "unknown_host"

// federationVerbBudget bounds how long a host verb may take in total. It is
// under the CLI's own 30 second read deadline with room to spare, and the work
// under it is already bounded per host, so this is a backstop rather than the
// mechanism.
const federationVerbBudget = 15 * time.Second

// hostSessionsEntry is one host's slice of an aggregated session listing.
type hostSessionsEntry struct {
	Host   string             `json:"host"`
	Status federation.Status  `json:"status"`
	Reason string             `json:"reason,omitempty"`
	Detail string             `json:"detail,omitempty"`
	Error  string             `json:"error,omitempty"`
	Code   string             `json:"code,omitempty"`
	Result []remoteSessionRow `json:"sessions,omitempty"`
	hostFreshness
}

// hostFreshness says how current a host's rows are. It is on the entries of
// both host listings.
type hostFreshness struct {
	// Stale is set when the host did not answer and the rows are the last
	// ones it gave, from FetchedAt (unix seconds). A host that never answered
	// has no rows and no stale mark.
	Stale     bool  `json:"stale,omitempty"`
	FetchedAt int64 `json:"fetched_at,omitempty"`
	// Events is how this daemon follows the host: live, polling, or empty
	// while the link is not up. See list-hosts.
	Events string `json:"events,omitempty"`
}

// remoteSessionRow is a session on another machine, as that machine described
// it. Only the fields a listing shows are decoded: a remote daemon is untrusted
// and newer than this one as often as not, so its extra fields are dropped here
// rather than carried into anything.
type remoteSessionRow struct {
	Name        string `json:"name"`
	DisplayName string `json:"display_name,omitempty"`
	WindowCount int    `json:"window_count"`
	Attached    bool   `json:"attached,omitempty"`
	Restored    bool   `json:"restored,omitempty"`
	// Global marks a session meant to hold panes from more than one machine.
	// It is carried across the link so every machine files the session the
	// same way: a global session is in the rail's global group whichever
	// machine you are looking from, rather than being a global session here
	// and an ordinary session on the machine next to it.
	Global     bool  `json:"global,omitempty"`
	LastActive int64 `json:"last_active,omitempty"`
	Created    int64 `json:"created,omitempty"`
	// AgentState is the most urgent agent state among the session's panes, by
	// the rail's own ranking, empty when nothing in it is running an agent.
	//
	// It is rolled up here rather than sent pane by pane because a session row
	// is one row: the rail asks "does anything in there want a person", and a
	// listing that carried every pane's state would be answering a question
	// nobody on this side asked. The unread bit is deliberately not carried.
	// Whether a finished agent has been looked at is a fact about a viewer, not
	// about the session, and this client has not looked at another machine's
	// panes, so they roll up as unseen, which is what they are.
	AgentState string `json:"agent_state,omitempty"`
}

// remoteSessionListRow is a row as a host sends it. It carries the window
// summaries the rollup reads and this daemon does not pass on: the embedded
// row is what crosses to the client.
type remoteSessionListRow struct {
	remoteSessionRow
	Windows []WindowSummary `json:"windows"`
}

// rollUpAgentState is the most urgent state among a session's panes, by the
// same ranking the rail draws with, so a row on another machine wears the
// glyph a row on this one would.
//
// Every pane is ranked as unseen. The unread bit belongs to a viewer, and the
// daemon answering has no idea what the person reading the listing has looked
// at.
func rollUpAgentState(windows []WindowSummary) string {
	best, state := 0, ""
	for _, w := range windows {
		if r := sessiontree.AgentRank(w.AgentState, false); r > best {
			best, state = r, w.AgentState
		}
	}
	return state
}

// hostAgentsEntry is one host's slice of an aggregated agent listing.
type hostAgentsEntry struct {
	Host   string            `json:"host"`
	Status federation.Status `json:"status"`
	Reason string            `json:"reason,omitempty"`
	Error  string            `json:"error,omitempty"`
	Code   string            `json:"code,omitempty"`
	// Session used to name the one session a host answered about, its most
	// recently active. Every session is listed now and each row names its
	// own, so this is set only when the host has exactly one session with
	// rows in the answer, which keeps the field meaning what it meant.
	Session string           `json:"session,omitempty"`
	Agents  []remoteAgentRow `json:"agents,omitempty"`
	hostFreshness
}

// remoteAgentRow is one agent pane on another machine. Only these fields are
// decoded from the host's answer: the host is untrusted, and a field this
// build does not carry is dropped rather than passed on.
type remoteAgentRow struct {
	Session        string `json:"session,omitempty"`
	WindowID       string `json:"window_id"`
	Name           string `json:"name"`
	State          string `json:"state"`
	Message        string `json:"message,omitempty"`
	HarnessID      string `json:"harness_id,omitempty"`
	Unread         int    `json:"unread,omitempty"`
	Ready          bool   `json:"ready,omitempty"`
	Since          int64  `json:"agent_state_at,omitempty"`
	Cwd            string `json:"cwd,omitempty"`
	BlockedBy      string `json:"blocked_by,omitempty"`
	CompletionSeq  uint64 `json:"completion_seq,omitempty"`
	FinishedUnread bool   `json:"finished_unread,omitempty"`
	// Group is the fan-out group of the pane's session. A host from before
	// selectors sends none, so a group: term matches none of its rows.
	Group string `json:"group,omitempty"`
	// Queued is how many messages wait in the pane's delivery queue there.
	Queued int `json:"queued,omitempty"`
}

// selectorTarget is what a selector reads from a row of host.
func (r remoteAgentRow) selectorTarget(host string) SelectorTarget {
	state := AgentState(r.State)
	return SelectorTarget{
		Host:     host,
		Session:  r.Session,
		Name:     r.Name,
		State:    r.State,
		Harness:  r.HarnessID,
		Cwd:      r.Cwd,
		Group:    r.Group,
		NeedsYou: state.NeedsYou(),
	}
}

// filterHostAgents keeps the rows of every entry that sel matches.
func filterHostAgents(entries []hostAgentsEntry, sel *Selector) {
	if sel == nil {
		return
	}
	for i := range entries {
		kept := entries[i].Agents[:0:0]
		for _, r := range entries[i].Agents {
			if sel.Match(r.selectorTarget(entries[i].Host)) {
				kept = append(kept, r)
			}
		}
		entries[i].Agents = kept
		entries[i].Session = soleSession(kept)
	}
}

// verbListHosts reports every configured host with its status and versions.
//
// This is `tuios hosts`. It answers the question the design document says a
// listing has to answer on its own: why is this host not usable, and is it the
// machine, the daemon, or the version.
func (d *Daemon) verbListHosts(_ *connState, _ json.RawMessage) (any, *verbError) {
	// events_push says this daemon pushes every change to a host it streams:
	// host-changed on subscribe, and MsgHostsChanged to attached clients. A
	// client that sees it polls only the hosts whose events are not live.
	out := map[string]any{"type": "host_list", "events_push": true}
	if problems := d.configProblems(); len(problems) > 0 {
		out["config_problems"] = problems
	}
	if d.federation == nil {
		out["hosts"] = []federation.HostReport{}
		out["total"] = 0
		return out, nil
	}

	ctx, cancel := context.WithTimeout(d.ctx, federationVerbBudget)
	defer cancel()
	reports := d.federation.Reports(ctx)
	for i := range reports {
		reports[i].Events, reports[i].EventsNote = d.fleet.mode(reports[i].Host)
		reports[i].Queued = d.outbox.count(reports[i].Host)
	}
	out["hosts"] = reports
	out["total"] = len(reports)
	return out, nil
}

// verbListHostSessions is the aggregated `tuios ls --all-hosts`.
//
// Local always comes first and is never fetched over a link; it is this
// daemon's own listing. Remote hosts follow in the table's sorted order,
// answering or not.
func (d *Daemon) verbListHostSessions(_ *connState, params json.RawMessage) (any, *verbError) {
	var p struct {
		Host string `json:"host"`
	}
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}

	entries := make([]hostSessionsEntry, 0, 4)
	if p.Host == "" || p.Host == federation.LocalHostName {
		entries = append(entries, hostSessionsEntry{
			Host:   federation.LocalHostName,
			Status: federation.StatusUp,
			Result: localSessionRows(d.listSessions()),
		})
	}
	if p.Host == federation.LocalHostName {
		return map[string]any{"type": "host_session_list", "hosts": entries}, nil
	}

	if verr := d.checkHostParam(p.Host); verr != nil {
		return nil, verr
	}

	ctx, cancel := context.WithTimeout(d.ctx, federationVerbBudget)
	defer cancel()
	for _, a := range d.federationAnswers(ctx, p.Host, "list-sessions", nil) {
		e := hostSessionsEntry{Host: a.Host, Status: a.Report.Status, Reason: a.Report.Reason, Detail: a.Report.Detail}
		e.Events, _ = d.fleet.mode(a.Host)
		var rows []remoteSessionListRow
		err := a.Err
		if err == nil {
			rows, err = decodeHostSessions(a.Result)
			if err == nil {
				d.fleet.storeSessions(a.Host, rows)
			}
		}
		if err != nil {
			e.Error, e.Code = hostListingErrorText(err, "session list")
			// A host that did not answer keeps the rows it last gave, marked
			// stale with when they were taken, so a listing still says what
			// was waiting there rather than going blank.
			if cached, at := d.fleet.cachedSessions(a.Host); !at.IsZero() {
				e.Result, e.Stale, e.FetchedAt = sessionRowsOut(cached), true, at.Unix()
			}
			entries = append(entries, e)
			continue
		}
		e.Result = sessionRowsOut(rows)
		entries = append(entries, e)
	}
	return map[string]any{"type": "host_session_list", "hosts": entries}, nil
}

// errHostAnswerUnreadable is a host's answer this build cannot decode.
var errHostAnswerUnreadable = errors.New("the host sent a listing this build cannot read")

// decodeHostSessions reads a host's list-sessions answer.
func decodeHostSessions(raw json.RawMessage) ([]remoteSessionListRow, error) {
	var decoded struct {
		Sessions []remoteSessionListRow `json:"sessions"`
	}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return nil, errHostAnswerUnreadable
	}
	return decoded.Sessions, nil
}

// sessionRowsOut is a host's rows as a listing sends them, each rolled up to
// its most urgent agent state.
func sessionRowsOut(in []remoteSessionListRow) []remoteSessionRow {
	rows := make([]remoteSessionRow, 0, len(in))
	for _, r := range in {
		r.remoteSessionRow.AgentState = rollUpAgentState(r.Windows)
		rows = append(rows, r.remoteSessionRow)
	}
	return rows
}

// fetchHostSessions asks one host for its sessions.
func (d *Daemon) fetchHostSessions(ctx context.Context, host string) ([]remoteSessionListRow, error) {
	raw, err := d.federation.Call(ctx, host, "list-sessions", nil)
	if err != nil {
		return nil, err
	}
	return decodeHostSessions(raw)
}

// fetchHostAgents asks one host for the agent panes of every session it holds.
//
// A host from before list-agents took all_sessions refuses the param, and is
// asked the old way instead: its sessions, then each session's agents. That is
// one round trip per session, which is slower and answers the same question.
func (d *Daemon) fetchHostAgents(ctx context.Context, host string, all bool) ([]remoteAgentRow, error) {
	raw, err := d.federation.Call(ctx, host, "list-agents", map[string]any{"all": all, "all_sessions": true})
	if err == nil {
		return decodeHostAgents(raw, "")
	}
	var rerr *federation.RemoteError
	if !errors.As(err, &rerr) || rerr.Code != ErrVerbInvalidParams {
		return nil, err
	}
	sessions, err := d.fetchHostSessions(ctx, host)
	if err != nil {
		return nil, err
	}
	var out []remoteAgentRow
	for _, s := range sessions {
		raw, err := d.federation.Call(ctx, host, "list-agents", map[string]any{"all": all, "session": s.Name})
		if err != nil {
			if errors.As(err, &rerr) && rerr.Code == ErrVerbSessionNotFound {
				// The session ended between the two calls.
				continue
			}
			return nil, err
		}
		rows, err := decodeHostAgents(raw, s.Name)
		if err != nil {
			return nil, err
		}
		out = append(out, rows...)
		if len(out) >= fleetMaxAgents {
			break
		}
	}
	return out, nil
}

// decodeHostAgents reads a host's list-agents answer. A row from a host that
// does not name its session is given the session the answer names, and a
// queue count is held to 0..config.MaxQueueMax, since no queue of this build
// can hold more and the host is not trusted to say otherwise.
func decodeHostAgents(raw json.RawMessage, session string) ([]remoteAgentRow, error) {
	var decoded struct {
		Session string           `json:"session"`
		Agents  []remoteAgentRow `json:"agents"`
	}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return nil, errHostAnswerUnreadable
	}
	fallback := firstNonEmpty(decoded.Session, session)
	for i := range decoded.Agents {
		if decoded.Agents[i].Session == "" {
			decoded.Agents[i].Session = fallback
		}
		decoded.Agents[i].Queued = min(max(decoded.Agents[i].Queued, 0), config.MaxQueueMax)
	}
	return decoded.Agents, nil
}

// soleSession is the session every row is in, or empty when they are in
// several or there are none. It fills the entry's old session field.
func soleSession(rows []remoteAgentRow) string {
	if len(rows) == 0 {
		return ""
	}
	s := rows[0].Session
	for _, r := range rows[1:] {
		if r.Session != s {
			return ""
		}
	}
	return s
}

// verbListHostAgents is the aggregated `tuios list-agents --all-hosts`. Every
// session on every host is listed, and each row names its session.
func (d *Daemon) verbListHostAgents(cs *connState, params json.RawMessage) (any, *verbError) {
	var p struct {
		Host   string `json:"host"`
		All    bool   `json:"all"`
		Select string `json:"select"`
	}
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	var sel *Selector
	if p.Select != "" {
		var verr *verbError
		if sel, verr = d.parseVerbSelector(p.Select); verr != nil {
			return nil, verr
		}
	}
	// The selector is applied here, to every host's rows alike, rather than
	// sent on: a host from before selectors would refuse the param, and the
	// host term is about which machine a row came from, which only this side
	// knows.
	answer := func(entries []hostAgentsEntry) (any, *verbError) {
		filterHostAgents(entries, sel)
		out := map[string]any{"type": "host_agent_list", "hosts": entries}
		if sel != nil {
			out["select"] = sel.String()
		}
		return out, nil
	}

	entries := make([]hostAgentsEntry, 0, 4)
	if p.Host == "" || p.Host == federation.LocalHostName {
		e := hostAgentsEntry{Host: federation.LocalHostName, Status: federation.StatusUp}
		// Rebuilt rather than forwarded: this verb's params carry a host field
		// list-agents does not declare, and the local half must be called with
		// exactly the parameters list-agents takes.
		localParams, _ := json.Marshal(map[string]any{"all": p.All, "all_sessions": true})
		result, verr := d.verbListAgents(cs, localParams)
		switch {
		case verr != nil:
			e.Error, e.Code = verr.Message, verr.Code
		default:
			e.Agents = localAgentRows(result)
			e.Session = soleSession(e.Agents)
		}
		entries = append(entries, e)
	}
	if p.Host == federation.LocalHostName {
		return answer(entries)
	}

	if verr := d.checkHostParam(p.Host); verr != nil {
		return nil, verr
	}
	if d.federation == nil {
		return answer(entries)
	}

	ctx, cancel := context.WithTimeout(d.ctx, federationVerbBudget)
	defer cancel()
	// The reports wait, within the budget, for a host whose first dial has
	// not settled, so a listing right after the daemon starts says up or down
	// rather than connecting.
	var reports []federation.HostReport
	for _, r := range d.federation.Reports(ctx) {
		if p.Host == "" || r.Host == p.Host {
			reports = append(reports, r)
		}
	}
	// Every host is asked at once, so one slow machine costs the listing its
	// own row and nothing more.
	remote := make([]hostAgentsEntry, len(reports))
	var wg sync.WaitGroup
	for i, r := range reports {
		wg.Go(func() {
			remote[i] = d.hostAgentsEntryFor(ctx, r, p.All)
		})
	}
	wg.Wait()
	entries = append(entries, remote...)
	return answer(entries)
}

// hostAgentsEntryFor is one host's entry of list-host-agents: its rows when it
// answers, else the rows it last gave, marked stale.
func (d *Daemon) hostAgentsEntryFor(ctx context.Context, r federation.HostReport, all bool) hostAgentsEntry {
	host := r.Host
	e := hostAgentsEntry{Host: host, Status: r.Status, Reason: r.Reason}
	e.Events, _ = d.fleet.mode(host)
	var err error
	var rows []remoteAgentRow
	if r.Status != federation.StatusUp {
		err = &federation.UnreachableError{Host: host, Status: r.Status, Reason: r.Reason}
	} else {
		rows, err = d.fetchHostAgents(ctx, host, all)
	}
	if err != nil {
		e.Error, e.Code = hostListingErrorText(err, "agent list")
		if cached, at := d.fleet.cachedAgents(host); !at.IsZero() {
			e.Agents, e.Stale, e.FetchedAt = cached, true, at.Unix()
		}
		return e
	}
	if !all {
		d.fleet.storeAgents(host, rows)
	}
	e.Agents, e.Session = rows, soleSession(rows)
	return e
}

// hostListingErrorText is federationErrorText for a listing, which can also
// fail because the host's answer could not be read.
func hostListingErrorText(err error, what string) (string, string) {
	if errors.Is(err, errHostAnswerUnreadable) {
		return "The host sent a " + what + " this build cannot read.", ErrVerbInternal
	}
	return federationErrorText(err)
}

// checkHostParam refuses a named host that is not configured, before anything
// is dialed. An empty name means every host and is always allowed.
func (d *Daemon) checkHostParam(name string) *verbError {
	if name == "" {
		return nil
	}
	if !d.hasHosts() {
		return hintedVerbError(ErrVerbUnknownHost,
			"unknown host "+echoName(name)+". No hosts are configured.",
			&VerbHint{
				Param:   "host",
				Command: "tuios hosts add " + name + " user@machine",
				Detail:  "Add the machine with 'tuios hosts add'. The daemon opens the link at once.",
			})
	}
	if _, err := d.federation.Table().Lookup(name); err != nil {
		return hintedVerbError(ErrVerbUnknownHost, err.Error(), &VerbHint{
			Param:     "host",
			Command:   "tuios hosts",
			Available: d.federation.Table().Names(),
			Detail:    "A host name is matched exactly. Nothing is guessed, so a near miss cannot reach the wrong machine.",
		})
	}
	return nil
}

// federationAnswers runs one read verb against one host or every host.
func (d *Daemon) federationAnswers(ctx context.Context, host, verb string, params any) []federation.Answer {
	if d.federation == nil {
		return nil
	}
	if host == "" {
		return d.federation.CallAll(ctx, verb, params)
	}
	a := federation.Answer{Host: host}
	for _, r := range d.federation.Reports(ctx) {
		if r.Host == host {
			a.Report = r
			break
		}
	}
	a.Result, a.Err = d.federation.Call(ctx, host, verb, params)
	return []federation.Answer{a}
}

// federationErrorText turns a link failure into the sentence a user reads and
// the stable code a machine reads.
func federationErrorText(err error) (string, string) {
	if err == nil {
		return "", ""
	}
	if errors.Is(err, federation.ErrUnknownHost) {
		return err.Error(), ErrVerbUnknownHost
	}
	// Every other failure across a link is the host not answering, whatever the
	// underlying cause was. The cause is still carried, in the report's detail.
	return err.Error(), ErrVerbHostUnreachable
}

// localSessionRows narrows this daemon's own listing to the fields a federated
// listing shows, so the local row and a remote row are the same shape.
func localSessionRows(infos []SessionInfo) []remoteSessionRow {
	out := make([]remoteSessionRow, 0, len(infos))
	for _, s := range infos {
		out = append(out, remoteSessionRow{
			Name:        s.Name,
			DisplayName: s.DisplayName,
			WindowCount: s.WindowCount,
			Attached:    s.Attached,
			Restored:    s.Restored,
			Global:      s.Global,
			LastActive:  s.LastActive,
			Created:     s.Created,
			AgentState:  rollUpAgentState(s.Windows),
		})
	}
	return out
}

// localAgentRows narrows the local list-agents result the same way. It goes
// through JSON rather than reaching into the handler's map so the local rows
// and the remote rows are decoded by one piece of code.
func localAgentRows(result any) []remoteAgentRow {
	raw, err := json.Marshal(result)
	if err != nil {
		return nil
	}
	rows, err := decodeHostAgents(raw, "")
	if err != nil {
		return nil
	}
	return rows
}
