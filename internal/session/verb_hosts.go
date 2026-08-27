package session

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/federation"
)

// The federation control plane, stage 1: three read verbs and nothing else.
//
// Every one of them asks a remote daemon for a listing and never tells it to do
// anything. There is no verb here that creates, kills, resizes, writes or
// attaches, and adding one is stage 2 work with a different risk profile.
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
	LastActive  int64  `json:"last_active,omitempty"`
	Created     int64  `json:"created,omitempty"`
}

// hostAgentsEntry is one host's slice of an aggregated agent listing.
type hostAgentsEntry struct {
	Host   string            `json:"host"`
	Status federation.Status `json:"status"`
	Reason string            `json:"reason,omitempty"`
	Error  string            `json:"error,omitempty"`
	Code   string            `json:"code,omitempty"`
	// Session is which session on that host the rows came from. The remote
	// daemon picks its own most recently active one, so saying which it picked
	// is the difference between a listing and a guess.
	Session string           `json:"session,omitempty"`
	Agents  []remoteAgentRow `json:"agents,omitempty"`
}

// remoteAgentRow is one agent pane on another machine.
type remoteAgentRow struct {
	WindowID  string `json:"window_id"`
	Name      string `json:"name"`
	State     string `json:"state"`
	Message   string `json:"message,omitempty"`
	HarnessID string `json:"harness_id,omitempty"`
	Unread    int    `json:"unread,omitempty"`
	Ready     bool   `json:"ready,omitempty"`
}

// verbListHosts reports every configured host with its status and versions.
//
// This is `tuios hosts`. It answers the question the design document says a
// listing has to answer on its own: why is this host not usable, and is it the
// machine, the daemon, or the version.
func (d *Daemon) verbListHosts(_ *connState, _ json.RawMessage) (any, *verbError) {
	out := map[string]any{"type": "host_list"}
	if len(d.federationProblems) > 0 {
		out["config_problems"] = d.federationProblems
	}
	if d.federation == nil {
		out["hosts"] = []federation.HostReport{}
		out["total"] = 0
		return out, nil
	}

	ctx, cancel := context.WithTimeout(d.ctx, federationVerbBudget)
	defer cancel()
	reports := d.federation.Reports(ctx)
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
		if a.Err != nil {
			e.Error, e.Code = federationErrorText(a.Err)
			entries = append(entries, e)
			continue
		}
		var decoded struct {
			Sessions []remoteSessionRow `json:"sessions"`
		}
		if err := json.Unmarshal(a.Result, &decoded); err != nil {
			e.Error, e.Code = "The host sent a session list this build cannot read.", ErrVerbInternal
			entries = append(entries, e)
			continue
		}
		e.Result = decoded.Sessions
		entries = append(entries, e)
	}
	return map[string]any{"type": "host_session_list", "hosts": entries}, nil
}

// verbListHostAgents is the aggregated `tuios list-agents --all-hosts`. Each
// remote host answers about its own most recently active session, and the reply
// says which session that was.
func (d *Daemon) verbListHostAgents(cs *connState, params json.RawMessage) (any, *verbError) {
	var p struct {
		Host string `json:"host"`
		All  bool   `json:"all"`
	}
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}

	entries := make([]hostAgentsEntry, 0, 4)
	if p.Host == "" || p.Host == federation.LocalHostName {
		e := hostAgentsEntry{Host: federation.LocalHostName, Status: federation.StatusUp}
		// Rebuilt rather than forwarded: this verb's params carry a host field
		// list-agents does not declare, and the local half must be called with
		// exactly the parameters list-agents takes.
		localParams, _ := json.Marshal(map[string]any{"all": p.All})
		result, verr := d.verbListAgents(cs, localParams)
		switch {
		case verr != nil:
			e.Error, e.Code = verr.Message, verr.Code
		default:
			e.Session, e.Agents = localAgentRows(result)
		}
		entries = append(entries, e)
	}
	if p.Host == federation.LocalHostName {
		return map[string]any{"type": "host_agent_list", "hosts": entries}, nil
	}

	if verr := d.checkHostParam(p.Host); verr != nil {
		return nil, verr
	}

	ctx, cancel := context.WithTimeout(d.ctx, federationVerbBudget)
	defer cancel()
	remoteParams := map[string]any{"all": p.All}
	for _, a := range d.federationAnswers(ctx, p.Host, "list-agents", remoteParams) {
		e := hostAgentsEntry{Host: a.Host, Status: a.Report.Status, Reason: a.Report.Reason}
		if a.Err != nil {
			e.Error, e.Code = federationErrorText(a.Err)
			entries = append(entries, e)
			continue
		}
		var decoded struct {
			Session string           `json:"session"`
			Agents  []remoteAgentRow `json:"agents"`
		}
		if err := json.Unmarshal(a.Result, &decoded); err != nil {
			e.Error, e.Code = "The host sent an agent list this build cannot read.", ErrVerbInternal
			entries = append(entries, e)
			continue
		}
		e.Session, e.Agents = decoded.Session, decoded.Agents
		entries = append(entries, e)
	}
	return map[string]any{"type": "host_agent_list", "hosts": entries}, nil
}

// checkHostParam refuses a named host that is not configured, before anything
// is dialed. An empty name means every host and is always allowed.
func (d *Daemon) checkHostParam(name string) *verbError {
	if name == "" {
		return nil
	}
	if d.federation == nil {
		return hintedVerbError(ErrVerbUnknownHost,
			"unknown host "+echoName(name)+". No hosts are configured.",
			&VerbHint{
				Param:   "host",
				Command: "tuios hosts",
				Detail:  "Add a [hosts." + name + "] table to the config file with an addr, then restart the daemon.",
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
			LastActive:  s.LastActive,
			Created:     s.Created,
		})
	}
	return out
}

// localAgentRows narrows the local list-agents result the same way. It goes
// through JSON rather than reaching into the handler's map so the local rows
// and the remote rows are decoded by one piece of code.
func localAgentRows(result any) (string, []remoteAgentRow) {
	raw, err := json.Marshal(result)
	if err != nil {
		return "", nil
	}
	var decoded struct {
		Session string           `json:"session"`
		Agents  []remoteAgentRow `json:"agents"`
	}
	if json.Unmarshal(raw, &decoded) != nil {
		return "", nil
	}
	return decoded.Session, decoded.Agents
}
