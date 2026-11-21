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

	"github.com/Gaurav-Gosain/tuios/internal/atomicfile"
	"github.com/Gaurav-Gosain/tuios/internal/federation"
	"github.com/Gaurav-Gosain/tuios/internal/jsonx"
	"github.com/Gaurav-Gosain/tuios/internal/plural"
)

const (
	outboxMaxPerHost    = 64
	outboxMaxTotal      = 256
	outboxMaxFailures   = 8
	outboxDeliverBudget = 10 * time.Second
	outboxRetryMin      = time.Second
	outboxRetryMax      = 30 * time.Second
)

type outboxEntry struct {
	ID       uint64          `json:"id"`
	Host     string          `json:"host"`
	Session  string          `json:"session"`
	To       string          `json:"to,omitempty"`
	Params   json.RawMessage `json:"params"`
	QueuedAt int64           `json:"queued_at"`
}

type outboxFailure struct {
	Session string `json:"session"`
	To      string `json:"to,omitempty"`
	Reason  string `json:"reason"`
	At      int64  `json:"at"`
}

type outboxFile struct {
	Version  int                        `json:"version"`
	NextID   uint64                     `json:"next_id"`
	Entries  []outboxEntry              `json:"entries"`
	Failures map[string][]outboxFailure `json:"failures,omitempty"`
}

type hostOutbox struct {
	d *Daemon

	mu       sync.Mutex
	path     string
	nextID   uint64
	entries  []outboxEntry
	failures map[string][]outboxFailure
	flushing map[string]bool
	retry    map[string]*time.Timer
	backoff  map[string]time.Duration
	failCall func(params json.RawMessage) error
}

func newHostOutbox(d *Daemon) *hostOutbox {
	return &hostOutbox{
		d:        d,
		failures: map[string][]outboxFailure{},
		flushing: map[string]bool{},
		retry:    map[string]*time.Timer{},
		backoff:  map[string]time.Duration{},
	}
}

func (o *hostOutbox) call(host string, params json.RawMessage) (json.RawMessage, error) {
	o.mu.Lock()
	fail := o.failCall
	o.mu.Unlock()
	if fail != nil {
		if err := fail(params); err != nil {
			return nil, err
		}
	}
	ctx, cancel := context.WithTimeout(o.d.ctx, outboxDeliverBudget)
	defer cancel()
	return o.d.federation.Call(ctx, host, "send-agent-message", params)
}

func (o *hostOutbox) hostUp(host string) bool {
	if o.d.federation == nil {
		return false
	}
	r, ok := o.d.federation.Report(host)
	return ok && r.Status == federation.StatusUp
}

func (o *hostOutbox) busy(host string) bool {
	if o == nil {
		return false
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.flushing[host] || o.hasLocked(host)
}

func outboxPath() string {
	return filepath.Join(getResurrectionDir(), "outbox", "mail.json")
}

func (o *hostOutbox) load(path string) {
	var f outboxFile
	if data, err := os.ReadFile(path); err == nil {
		if err := jsonx.Unmarshal(data, &f); err != nil || f.Version != 1 {
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

func (o *hostOutbox) saveLocked() {
	if o.path == "" {
		return
	}
	f := outboxFile{Version: 1, NextID: o.nextID, Entries: o.entries, Failures: o.failures}
	data, err := jsonx.Marshal(f)
	if err != nil {
		return
	}
	if err := atomicfile.WritePrivate(o.path, data, false); err != nil {
		LogError("Failed to save the outbox: %v", err)
	}
}

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

func (o *hostOutbox) pushHost(host string) {
	if o.d.fleet != nil {
		o.d.fleet.schedulePush(host)
	}
}

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
	if t := o.retry[host]; t != nil {
		t.Stop()
		delete(o.retry, host)
	}
	delete(o.backoff, host)
	o.saveLocked()
	o.mu.Unlock()
	if dropped > 0 {
		LogBasic("Discarded %s waiting for %s", plural.Count(dropped, "message"), host)
	}
	o.pushHost(host)
	return dropped
}

func (o *hostOutbox) kick(host string) {
	if o == nil {
		return
	}
	o.mu.Lock()
	if t := o.retry[host]; t != nil {
		t.Stop()
		delete(o.retry, host)
	}
	if o.flushing[host] || !o.hasLocked(host) {
		o.mu.Unlock()
		return
	}
	o.flushing[host] = true
	o.mu.Unlock()
	go o.flush(host)
}

func (o *hostOutbox) hasLocked(host string) bool {
	for _, e := range o.entries {
		if e.Host == host {
			return true
		}
	}
	return false
}

func (o *hostOutbox) flush(host string) {
	defer func() {
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
		if next == nil || o.d.federation == nil || o.d.ctx.Err() != nil {
			delete(o.flushing, host)
			delete(o.backoff, host)
			o.mu.Unlock()
			return
		}
		o.mu.Unlock()
		_, err := o.call(host, next.Params)
		var remote *federation.RemoteError
		switch {
		case err == nil:
			LogBasic("Delivered queued message %d to %s", next.ID, host)
			o.remove(next.ID, nil)
		case errors.As(err, &remote), errors.Is(err, federation.ErrUnknownHost):
			LogBasic("Queued message %d to %s was refused there: %v", next.ID, host, err)
			o.remove(next.ID, &outboxFailure{Session: next.Session, To: next.To, Reason: err.Error(), At: time.Now().UnixNano()})
		default:
			up := o.hostUp(host)
			o.mu.Lock()
			delete(o.flushing, host)
			if up && o.retry[host] == nil && o.d.ctx.Err() == nil {
				wait := o.backoff[host]
				if wait == 0 {
					wait = outboxRetryMin
				}
				o.backoff[host] = min(2*wait, outboxRetryMax)
				LogBasic("Delivery of queued message %d to %s failed with the link up, trying again in %s: %v", next.ID, host, wait, err)
				o.retry[host] = time.AfterFunc(wait, func() { o.kick(host) })
			}
			o.mu.Unlock()
			return
		}
	}
}

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

func (o *hostOutbox) waitReason(host string) string {
	if o.d.federation == nil {
		return ""
	}
	r, ok := o.d.federation.Report(host)
	if !ok || r.Status == federation.StatusUp {
		return ""
	}
	if r.Status == federation.StatusApproval {
		return federation.SignInSentence
	}
	return r.Reason
}

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
	o.d.attention.noteOutbox(host, waiting, fails, since, o.waitReason(host))
}

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
	if err := jsonx.Unmarshal(params, &fields); err != nil {
		return nil, newVerbError(ErrVerbInvalidParams, "params: "+err.Error())
	}
	delete(fields, "host")
	delete(fields, "human_nonce")
	if self := linkSelfName(d.hostedPaneHostName()); self != "" {
		fields["from_host"] = mustJSON(self)
	}
	out, err := jsonx.Marshal(fields)
	if err != nil {
		return nil, newVerbError(ErrVerbInternal, "could not encode the message")
	}

	if d.outbox.busy(host) {
		return d.queueForHost(host, sessionName, to, out, "earlier mail waits")
	}

	raw, err := d.outbox.call(host, json.RawMessage(out))
	var remote *federation.RemoteError
	switch {
	case err == nil:
		var res map[string]any
		if jsonx.Unmarshal(raw, &res) != nil {
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
	return d.queueForHost(host, sessionName, to, out, err.Error())
}

func (d *Daemon) queueForHost(host, sessionName, to string, params []byte, why string) (any, *verbError) {
	e, waiting, verr := d.outbox.enqueue(host, sessionName, to, params)
	if verr != nil {
		return nil, verr
	}
	LogBasic("Queued message %d for %s: %s", e.ID, host, why)
	if d.outbox.hostUp(host) {
		d.outbox.kick(host)
	}
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

func outboxSummary(host string, waiting int, fails []outboxFailure, why string) string {
	var s string
	switch {
	case waiting == 1:
		s = "1 message waits for the link to " + host
	case waiting > 1:
		s = strconv.Itoa(waiting) + " messages wait for the link to " + host
	}
	if s != "" && why != "" {
		s += ". " + why
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
