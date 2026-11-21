package session

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/federation"
	"github.com/Gaurav-Gosain/tuios/internal/jsonx"
	"github.com/Gaurav-Gosain/tuios/internal/sessiontree"
)

// ErrVerbHostUnreachable reports a host that is configured and not answering.
// It is final: the caller reports it, and it does not retry into another name.
const ErrVerbHostUnreachable = "host_unreachable"

// ErrVerbPaneEnded is a pane on another machine that cannot come back: the
// daemon there no longer has it, or the link stayed down past its grace.
const ErrVerbPaneEnded = "pane_ended"

// ErrVerbUnknownHost reports a host name that is not in the configured table.
// Also final, and its message names every configured host.
const ErrVerbUnknownHost = "unknown_host"

const federationVerbBudget = 15 * time.Second

const hostAskBudget = 2 * time.Second

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

type hostFreshness struct {
	Stale     bool   `json:"stale,omitempty"`
	FetchedAt int64  `json:"fetched_at,omitempty"`
	Events    string `json:"events,omitempty"`
}

type remoteSessionRow struct {
	Name        string            `json:"name"`
	DisplayName string            `json:"display_name,omitempty"`
	WindowCount int               `json:"window_count"`
	Attached    bool              `json:"attached,omitempty"`
	Restored    bool              `json:"restored,omitempty"`
	Global      bool              `json:"global,omitempty"`
	Space       string            `json:"space,omitempty"`
	LastActive  int64             `json:"last_active,omitempty"`
	Created     int64             `json:"created,omitempty"`
	AgentState  string            `json:"agent_state,omitempty"`
	Agents      []remoteAgentPane `json:"agents,omitempty"`
}

type remoteAgentPane struct {
	ID        string           `json:"id"`
	Title     string           `json:"title,omitempty"`
	State     string           `json:"state"`
	StateAt   int64            `json:"state_at,omitempty"`
	Harness   string           `json:"harness,omitempty"`
	Message   string           `json:"message,omitempty"`
	Kind      string           `json:"kind,omitempty"`
	Meta      []AgentMetaToken `json:"meta,omitempty"`
	Subagents int              `json:"subagents,omitempty"`
	Settled   bool             `json:"settled,omitempty"`
}

const (
	remoteAgentPanesMax = 32
	remoteAgentHostMax  = 256
	remoteAgentMetaMax  = 8
	remoteAgentTextMax  = 160
	remoteAgentIDMax    = 64
)

func plausibleWindowID(id string) bool {
	if id == "" || len(id) > remoteAgentIDMax {
		return false
	}
	for _, r := range id {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-') {
			return false
		}
	}
	return true
}

func agentPanes(windows []WindowSummary) []remoteAgentPane {
	var out []remoteAgentPane
	clip := func(s string) string {
		if len(s) <= remoteAgentTextMax {
			return s
		}
		return strings.ToValidUTF8(s[:remoteAgentTextMax], "")
	}
	for _, w := range windows {
		if w.AgentState == "" || !plausibleWindowID(w.ID) {
			continue
		}
		if len(out) == remoteAgentPanesMax {
			break
		}
		p := remoteAgentPane{ID: w.ID, Title: clip(w.Title), State: clip(w.AgentState), StateAt: w.AgentStateAt,
			Harness: clip(w.AgentHarness), Message: clip(w.AgentMessage), Kind: clip(w.AgentKind), Subagents: max(w.Subagents, 0),
			Settled: w.AgentSettled}
		for _, t := range w.AgentMeta {
			if len(p.Meta) == remoteAgentMetaMax {
				break
			}
			p.Meta = append(p.Meta, AgentMetaToken{Key: clip(t.Key), Value: clip(t.Value)})
		}
		out = append(out, p)
	}
	return out
}

type remoteSessionListRow struct {
	remoteSessionRow
	Windows []WindowSummary `json:"windows"`
}

func rollUpAgentState(windows []WindowSummary) string {
	best, state := 0, ""
	for _, w := range windows {
		if r := sessiontree.AgentRank(w.AgentState, false); r > best {
			best, state = r, w.AgentState
		}
	}
	return state
}

type hostAgentsEntry struct {
	Host    string            `json:"host"`
	Status  federation.Status `json:"status"`
	Reason  string            `json:"reason,omitempty"`
	Error   string            `json:"error,omitempty"`
	Code    string            `json:"code,omitempty"`
	Session string            `json:"session,omitempty"`
	Agents  []remoteAgentRow  `json:"agents,omitempty"`
	hostFreshness
}

type remoteAgentRow struct {
	Session        string `json:"session,omitempty"`
	WindowID       string `json:"window_id"`
	Name           string `json:"name"`
	State          string `json:"state"`
	Message        string `json:"message,omitempty"`
	HarnessID      string `json:"harness_id,omitempty"`
	Unread         int    `json:"unread,omitempty"`
	Seen           int    `json:"seen,omitempty"`
	Ready          bool   `json:"ready,omitempty"`
	Since          int64  `json:"agent_state_at,omitempty"`
	Cwd            string `json:"cwd,omitempty"`
	BlockedBy      string `json:"blocked_by,omitempty"`
	CompletionSeq  uint64 `json:"completion_seq,omitempty"`
	FinishedUnread bool   `json:"finished_unread,omitempty"`
	Group          string `json:"group,omitempty"`
	Queued         int    `json:"queued,omitempty"`
}

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

func (d *Daemon) verbListHosts(cs *connState, _ json.RawMessage) (any, *verbError) {
	out := map[string]any{"type": "host_list", "events_push": true, "load": machineLoad(d.manager.ListSessions())}
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
	askCtx, cancelAsk := context.WithTimeout(ctx, hostAskBudget)
	d.federation.AskWaiting(askCtx)
	cancelAsk()
	reports := d.federation.Reports(ctx)
	for i := range reports {
		reports[i].Events, reports[i].EventsNote = d.fleet.mode(reports[i].Host)
		reports[i].Queued = d.outbox.count(reports[i].Host)
		if cs != nil && cs.viaLink {
			reports[i].ApprovalURL = ""
			reports[i].ApprovalRefused = false
			reports[i].Reason = linkSafeReason(reports[i].Status, reports[i].Reason)
		}
	}
	out["hosts"] = reports
	out["total"] = len(reports)
	return out, nil
}

func linkSafeReason(status federation.Status, reason string) string {
	if status == federation.StatusApproval {
		return federation.SignInSentence
	}
	return reason
}

func (d *Daemon) verbRetryHost(_ *connState, params json.RawMessage) (any, *verbError) {
	var p struct {
		Host string `json:"host"`
	}
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	if p.Host == "" {
		return nil, newVerbError(ErrVerbInvalidParams, "retry-host needs a host")
	}
	if verr := d.checkHostParam(p.Host); verr != nil {
		return nil, verr
	}
	d.federation.Retry(p.Host)
	out := map[string]any{"type": "host_retry", "host": p.Host}
	if r, ok := d.federation.Report(p.Host); ok {
		out["status"] = r.Status
		if r.ApprovalURL != "" {
			out["approval_url"] = r.ApprovalURL
		}
		if r.ApprovalRefused {
			out["approval_refused"] = true
		}
	}
	return out, nil
}

func (d *Daemon) verbListHostSessions(cs *connState, params json.RawMessage) (any, *verbError) {
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
		if cs != nil && cs.viaLink {
			e.Reason = linkSafeReason(e.Status, e.Reason)
		}
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

var errHostAnswerUnreadable = errors.New("the host sent a listing this build cannot read")

func decodeHostSessions(raw json.RawMessage) ([]remoteSessionListRow, error) {
	var decoded struct {
		Sessions []remoteSessionListRow `json:"sessions"`
	}
	if err := jsonx.Unmarshal(raw, &decoded); err != nil {
		return nil, errHostAnswerUnreadable
	}
	return decoded.Sessions, nil
}

func sessionRowsOut(in []remoteSessionListRow) []remoteSessionRow {
	rows := make([]remoteSessionRow, 0, len(in))
	left := remoteAgentHostMax
	for _, r := range in {
		r.remoteSessionRow.AgentState = rollUpAgentState(r.Windows)
		agents := agentPanes(r.Windows)
		if len(agents) > left {
			agents = agents[:left]
		}
		left -= len(agents)
		r.remoteSessionRow.Agents = agents
		rows = append(rows, r.remoteSessionRow)
	}
	return rows
}

func (d *Daemon) fetchHostSessions(ctx context.Context, host string) ([]remoteSessionListRow, error) {
	raw, err := d.federation.Call(ctx, host, "list-sessions", nil)
	if err != nil {
		return nil, err
	}
	return decodeHostSessions(raw)
}

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

func decodeHostAgents(raw json.RawMessage, session string) ([]remoteAgentRow, error) {
	var decoded struct {
		Session string           `json:"session"`
		Agents  []remoteAgentRow `json:"agents"`
	}
	if err := jsonx.Unmarshal(raw, &decoded); err != nil {
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
		localParams, _ := jsonx.Marshal(map[string]any{"all": p.All, "all_sessions": true})
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
	var reports []federation.HostReport
	for _, r := range d.federation.Reports(ctx) {
		if p.Host == "" || r.Host == p.Host {
			reports = append(reports, r)
		}
	}
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

func hostListingErrorText(err error, what string) (string, string) {
	if errors.Is(err, errHostAnswerUnreadable) {
		return "The host sent a " + what + " this build cannot read.", ErrVerbInternal
	}
	return federationErrorText(err)
}

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

func federationErrorText(err error) (string, string) {
	if err == nil {
		return "", ""
	}
	if errors.Is(err, federation.ErrUnknownHost) {
		return err.Error(), ErrVerbUnknownHost
	}
	return err.Error(), ErrVerbHostUnreachable
}

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
			Space:       s.Space,
			LastActive:  s.LastActive,
			Created:     s.Created,
			AgentState:  rollUpAgentState(s.Windows),
		})
	}
	return out
}

func localAgentRows(result any) []remoteAgentRow {
	raw, err := jsonx.Marshal(result)
	if err != nil {
		return nil
	}
	rows, err := decodeHostAgents(raw, "")
	if err != nil {
		return nil
	}
	return rows
}
