package session

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/federation"
)

// Mail to a machine whose link is down.
//
// send-agent-message to a session on another machine used to fail with
// host_unreachable whenever that machine's link was down, which on a laptop is
// every time the lid closed. Now a send can name the host (send-agent-message's
// host param), and this daemon delivers it over the link, or, when the link is
// down, keeps it here and delivers it when the link comes back, in the order it
// was sent. The sender is told queued. Each machine with mail waiting has one
// Inbox item (kind outbox) that says how many wait and which were refused, and
// the rail's host row says how many.
//
// What is kept and how it is bounded:
//
//   - The queue is written to disk, owner only, on every change, so a daemon
//     restart does not lose it.
//   - At most outboxMaxPerHost messages wait for one machine and
//     outboxMaxTotal in all. Past that a send is refused with rate_limited:
//     nothing is dropped to make room.
//   - A message is delivered with this daemon's own call on the link, so the
//     far machine holds it to the link policy it has for this machine, as it
//     does for any mail from here. A message from human cannot carry a nonce
//     that far machine would honour, so it arrives there as claimed_human.
//   - A delivery the far machine answers with an error is not retried: the
//     answer is final (the session is gone, the policy refuses mail). It is
//     dropped, and the Inbox item says so until the person dismisses it.
//     Dismissing the item also discards what is still waiting for that host.
//   - A caller that arrived over a link may not use it, since that would send
//     as this machine to a machine the caller cannot reach itself. A pane's
//     report channel drops the param.

const (
	// outboxMaxPerHost bounds the mail waiting for one machine.
	outboxMaxPerHost = 64
	// outboxMaxTotal bounds the mail waiting for every machine.
	outboxMaxTotal = 256
	// outboxMaxFailures is how many refused deliveries per machine the Inbox
	// item remembers.
	outboxMaxFailures = 8
	// outboxDeliverBudget bounds one delivery.
	outboxDeliverBudget = 10 * time.Second
)

// outboxEntry is one message waiting for a machine.
type outboxEntry struct {
	ID       uint64          `json:"id"`
	Host     string          `json:"host"`
	Session  string          `json:"session"`
	To       string          `json:"to,omitempty"`
	Params   json.RawMessage `json:"params"`
	QueuedAt int64           `json:"queued_at"`
}

// outboxFailure is one delivery the far machine refused.
type outboxFailure struct {
	Session string `json:"session"`
	To      string `json:"to,omitempty"`
	Reason  string `json:"reason"`
	At      int64  `json:"at"`
}

// outboxFile is the on-disk form.
type outboxFile struct {
	Version  int                        `json:"version"`
	NextID   uint64                     `json:"next_id"`
	Entries  []outboxEntry              `json:"entries"`
	Failures map[string][]outboxFailure `json:"failures,omitempty"`
}

// hostOutbox is the daemon's queue of mail for machines whose link is down.
type hostOutbox struct {
	d *Daemon

	mu       sync.Mutex
	path     string
	nextID   uint64
	entries  []outboxEntry
	failures map[string][]outboxFailure
	// flushing marks a host whose queue is being delivered now, so a second
	// status report does not start a second delivery out of order.
	flushing map[string]bool
}

func newHostOutbox(d *Daemon) *hostOutbox {
	return &hostOutbox{d: d, failures: map[string][]outboxFailure{}, flushing: map[string]bool{}}
}

// outboxPath is where the queue is saved, beside the Inbox.
func outboxPath() string {
	return filepath.Join(getResurrectionDir(), "outbox", "mail.json")
}

// load reads the saved queue and opens an Inbox item for every machine it
// names. A file that cannot be read is set aside rather than trusted.
func (o *hostOutbox) load(path string) {
	var f outboxFile
	if data, err := os.ReadFile(path); err == nil {
		if err := json.Unmarshal(data, &f); err != nil || f.Version != 1 {
			LogError("Discarding the saved outbox, it could not be read: %v", err)
			f = outboxFile{}
		}
	}
	o.mu.Lock()
	o.path = path
	o.nextID = max(o.nextID, f.NextID)
	o.entries = append(o.entries, f.Entries...)
	for host, fails := range f.Failures {
		o.failures[host] = append(o.failures[host], fails...)
	}
	hosts := o.hostsLocked()
	o.mu.Unlock()
	for _, h := range hosts {
		o.noteAttention(h)
	}
}

// hostsLocked is every machine with mail waiting or a refusal to report.
func (o *hostOutbox) hostsLocked() []string {
	seen := map[string]bool{}
	var out []string
	for _, e := range o.entries {
		if !seen[e.Host] {
			seen[e.Host] = true
			out = append(out, e.Host)
		}
	}
	for h := range o.failures {
		if !seen[h] {
			seen[h] = true
			out = append(out, h)
		}
	}
	return out
}

// saveLocked writes the queue. The caller holds mu.
func (o *hostOutbox) saveLocked() {
	if o.path == "" {
		return
	}
	f := outboxFile{Version: 1, NextID: o.nextID, Entries: o.entries, Failures: o.failures}
	data, err := json.Marshal(f)
	if err != nil {
		return
	}
	if err := os.MkdirAll(filepath.Dir(o.path), 0o700); err != nil {
		LogError("Failed to create the outbox directory: %v", err)
		return
	}
	tmp := o.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		LogError("Failed to save the outbox: %v", err)
		return
	}
	if err := os.Rename(tmp, o.path); err != nil {
		_ = os.Remove(tmp)
		LogError("Failed to save the outbox: %v", err)
	}
}

// enqueue keeps a message for host. It refuses when the queue is full.
func (o *hostOutbox) enqueue(host, sessionName, to string, params json.RawMessage) (outboxEntry, int, *verbError) {
	o.mu.Lock()
	waiting := 0
	for _, e := range o.entries {
		if e.Host == host {
			waiting++
		}
	}
	if waiting >= outboxMaxPerHost || len(o.entries) >= outboxMaxTotal {
		o.mu.Unlock()
		return outboxEntry{}, waiting, hintedVerbError(ErrVerbRateLimited, strconv.Itoa(waiting)+" messages already wait for "+host+", which is the cap", &VerbHint{
			Command: "tuios hosts",
			Detail:  "Nothing was queued. A machine whose link is down holds at most 64 messages here, 256 for every machine. Wait for the link, or dismiss the outbox item in the Inbox to discard what waits.",
		})
	}
	o.nextID++
	e := outboxEntry{ID: o.nextID, Host: host, Session: sessionName, To: to, Params: params, QueuedAt: time.Now().UnixNano()}
	o.entries = append(o.entries, e)
	o.saveLocked()
	o.mu.Unlock()
	o.noteAttention(host)
	o.pushHost(host)
	return e, waiting + 1, nil
}

// pushHost tells the attached clients the host's row changed, so the rail's
// count follows the queue.
func (o *hostOutbox) pushHost(host string) {
	if o.d.fleet != nil {
		o.d.fleet.schedulePush(host)
	}
}

// count is how many messages wait for host.
func (o *hostOutbox) count(host string) int {
	if o == nil {
		return 0
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	n := 0
	for _, e := range o.entries {
		if e.Host == host {
			n++
		}
	}
	return n
}

// discard drops everything waiting for host, and what it remembered being
// refused. It is what dismissing the host's outbox item does.
func (o *hostOutbox) discard(host string) int {
	o.mu.Lock()
	kept := o.entries[:0]
	dropped := 0
	for _, e := range o.entries {
		if e.Host == host {
			dropped++
			continue
		}
		kept = append(kept, e)
	}
	o.entries = kept
	delete(o.failures, host)
	o.saveLocked()
	o.mu.Unlock()
	if dropped > 0 {
		LogBasic("Discarded %d message(s) waiting for %s", dropped, host)
	}
	o.pushHost(host)
	return dropped
}

// kick delivers what waits for host, in order, on its own goroutine. It is
// called when the host's link comes up and after a send was queued while the
// link looked up.
func (o *hostOutbox) kick(host string) {
	if o == nil {
		return
	}
	o.mu.Lock()
	if o.flushing[host] {
		o.mu.Unlock()
		return
	}
	has := false
	for _, e := range o.entries {
		if e.Host == host {
			has = true
			break
		}
	}
	if !has {
		o.mu.Unlock()
		return
	}
	o.flushing[host] = true
	o.mu.Unlock()
	go o.flush(host)
}

// flush delivers host's queue until it is empty or the link fails.
func (o *hostOutbox) flush(host string) {
	defer func() {
		o.mu.Lock()
		delete(o.flushing, host)
		o.mu.Unlock()
		o.noteAttention(host)
		o.pushHost(host)
	}()
	for {
		o.mu.Lock()
		var next *outboxEntry
		for i := range o.entries {
			if o.entries[i].Host == host {
				e := o.entries[i]
				next = &e
				break
			}
		}
		o.mu.Unlock()
		if next == nil || o.d.federation == nil || o.d.ctx.Err() != nil {
			return
		}
		ctx, cancel := context.WithTimeout(o.d.ctx, outboxDeliverBudget)
		_, err := o.d.federation.Call(ctx, host, "send-agent-message", next.Params)
		cancel()
		var remote *federation.RemoteError
		switch {
		case err == nil:
			LogBasic("Delivered queued message %d to %s", next.ID, host)
			o.remove(next.ID, nil)
		case errors.As(err, &remote), errors.Is(err, federation.ErrUnknownHost):
			LogBasic("Queued message %d to %s was refused there: %v", next.ID, host, err)
			o.remove(next.ID, &outboxFailure{Session: next.Session, To: next.To, Reason: err.Error(), At: time.Now().UnixNano()})
		default:
			// The link failed under the delivery. What is left waits for the
			// next time it comes up.
			return
		}
	}
}

// remove drops a delivered or refused entry, recording the refusal.
func (o *hostOutbox) remove(id uint64, failed *outboxFailure) {
	o.mu.Lock()
	defer o.mu.Unlock()
	for i, e := range o.entries {
		if e.ID != id {
			continue
		}
		o.entries = append(o.entries[:i], o.entries[i+1:]...)
		if failed != nil {
			fails := append(o.failures[e.Host], *failed)
			if len(fails) > outboxMaxFailures {
				fails = fails[len(fails)-outboxMaxFailures:]
			}
			o.failures[e.Host] = fails
		}
		break
	}
	o.saveLocked()
}

// noteAttention opens, updates or closes host's outbox item.
func (o *hostOutbox) noteAttention(host string) {
	o.mu.Lock()
	waiting := 0
	var since int64
	for _, e := range o.entries {
		if e.Host == host {
			waiting++
			if since == 0 {
				since = e.QueuedAt
			}
		}
	}
	fails := append([]outboxFailure(nil), o.failures[host]...)
	o.mu.Unlock()
	o.d.attention.noteOutbox(host, waiting, fails, since)
}

// sendAgentMessageToHost is send-agent-message with host: deliver the message
// to a session on that machine over this machine's link, or keep it here while
// the link is down. params is the request as it came, and is sent on without
// host and human_nonce, with from_host set to this machine's name.
func (d *Daemon) sendAgentMessageToHost(cs *connState, host, sessionName, to, from string, params json.RawMessage) (any, *verbError) {
	if cs != nil && (cs.viaLink || cs.paneOnly) {
		return nil, hintedVerbError(ErrVerbForbidden, "send-agent-message with host is for a caller on this machine", &VerbHint{
			Param:  "host",
			Detail: "It sends as this machine to another one. Nothing was sent.",
		})
	}
	if host == federation.LocalHostName {
		return nil, invalidParam("host", "host local is this machine: send without host")
	}
	if verr := d.checkHostParam(host); verr != nil {
		return nil, verr
	}
	if sessionName == "" {
		return nil, invalidParam("session", "a message for another machine names the session there: the far machine's most recent session is not known here")
	}
	if from == AgentInboxHuman && !d.mayActAsHuman(cs) {
		return nil, humanForbiddenError("send-agent-message")
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(params, &fields); err != nil {
		return nil, newVerbError(ErrVerbInvalidParams, "params: "+err.Error())
	}
	delete(fields, "host")
	delete(fields, "human_nonce")
	if self := linkSelfName(d.hostedPaneHostName()); self != "" {
		fields["from_host"] = mustJSON(self)
	}
	out, err := json.Marshal(fields)
	if err != nil {
		return nil, newVerbError(ErrVerbInternal, "could not encode the message")
	}

	ctx, cancel := context.WithTimeout(d.ctx, outboxDeliverBudget)
	raw, err := d.federation.Call(ctx, host, "send-agent-message", json.RawMessage(out))
	cancel()
	var remote *federation.RemoteError
	switch {
	case err == nil:
		var res map[string]any
		if json.Unmarshal(raw, &res) != nil {
			res = map[string]any{"type": "agent_message_sent"}
		}
		res["host"] = host
		res["queued"] = false
		return res, nil
	case errors.As(err, &remote):
		return nil, newVerbError(remote.Code, remote.Message)
	case errors.Is(err, federation.ErrUnknownHost):
		message, code := federationErrorText(err)
		return nil, newVerbError(code, message)
	}
	e, waiting, verr := d.outbox.enqueue(host, sessionName, to, out)
	if verr != nil {
		return nil, verr
	}
	LogBasic("Queued message %d for %s: its link is down", e.ID, host)
	return map[string]any{
		"type":     "agent_message_queued",
		"host":     host,
		"session":  sessionName,
		"to":       to,
		"queued":   true,
		"queue_id": e.ID,
		"waiting":  waiting,
	}, nil
}

// outboxSummary is the Inbox line for a machine's outbox.
func outboxSummary(host string, waiting int, fails []outboxFailure) string {
	var s string
	switch {
	case waiting == 1:
		s = "1 message waits for the link to " + host
	case waiting > 1:
		s = strconv.Itoa(waiting) + " messages wait for the link to " + host
	}
	if len(fails) > 0 {
		last := fails[len(fails)-1]
		refused := strconv.Itoa(len(fails)) + " refused by " + host + ": " + last.Reason
		if s == "" {
			s = refused
		} else {
			s += "; " + refused
		}
	}
	return s
}
