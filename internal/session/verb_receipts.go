package session

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"slices"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/federation"
	"github.com/Gaurav-Gosain/tuios/internal/jsonx"
)

// Read receipts: the sender's view of its own mail.
//
// The recipient's copy of a message has always recorded when it was seen
// (SeenAt) and read (ReadAt). Nothing showed the sender, whose own session
// does not hold the message, so a worker whose reports were read and acted on
// saw only its send's answer and mailed again to ask whether they arrived.
//
// agent-message-receipts answers for one sender window: every message that
// window sent that a ring on this daemon still holds, with its state. On this
// machine it also asks every linked machine the same question and gives the
// machines it could not ask. Over a link it answers only for messages that
// arrived on that same link (OriginLink is the caller's link peer) and that
// claim that sender, and only with ids, recipients and times: a machine learns
// the read state of the mail it sent, and nothing else.

const (
	receiptsDefaultLimit = 20
	receiptsMaxLimit     = 200
	// receiptsHostBudget bounds the far calls. One slow machine costs its own
	// line and no more.
	receiptsHostBudget = 4 * time.Second
	// receiptsPreview is how much of a local message's own text a row shows.
	receiptsPreview = 60
)

func receiptVerbs() map[string]verbEntry {
	return map[string]verbEntry{
		"agent-message-receipts": {
			scope:       scopeRead,
			link:        needMail,
			description: "The mail one window sent and its state, newest last: delivered, seen (the recipient looked at its inbox without reading) or read, with the times, and undeliverable when the recipient window is gone. Marks nothing. On this machine it reads every session's ring and asks each linked machine for the mail this window sent there; over a link it answers only for mail that arrived on that link from that window, with ids, recipients and times. A message a ring no longer holds is not listed.",
			params: []verbParam{
				{Name: "window", Type: "string", Required: true, Description: "The sending window's id, normally $TUIOS_PANE_ID. A restricted connection may name only its own window."},
				{Name: "limit", Type: "int", Description: "At most this many rows, newest last, at most 200.", Default: "20"},
			},
			returns: []verbParam{
				{Name: "receipts", Type: "[]object", Description: "One per message: id, host (empty for this machine), session, to, to_label, thread_id, sent_at, seen_at, read_at (Unix nanoseconds, 0 when not yet), undeliverable, and on this machine preview, the first line of the text."},
				{Name: "waiting", Type: "[]object", Description: "Mail this window sent to a machine whose link is down, kept here until it is back: queue_id, host, session, to, queued_at."},
				{Name: "unreachable", Type: "[]object", Description: "Linked machines that could not be asked now: host and reason. Mail sent there is not listed."},
			},
			examples: []string{
				`{"id":1,"verb":"agent-message-receipts","params":{"window":"3f2a91c0-5b1e-4d7a-9c2f-0e8b6a4d1c3e"}}`,
			},
			handler: (*Daemon).verbAgentMessageReceipts,
		},
	}
}

// mailReceipt is one row of agent-message-receipts.
type mailReceipt struct {
	ID            uint64 `json:"id"`
	Host          string `json:"host,omitempty"`
	Session       string `json:"session"`
	To            string `json:"to"`
	ToLabel       string `json:"to_label,omitempty"`
	ThreadID      uint64 `json:"thread_id,omitempty"`
	Preview       string `json:"preview,omitempty"`
	SentAt        int64  `json:"sent_at"`
	SeenAt        int64  `json:"seen_at,omitempty"`
	ReadAt        int64  `json:"read_at,omitempty"`
	Undeliverable bool   `json:"undeliverable,omitempty"`
}

type receiptWaiting struct {
	QueueID  uint64 `json:"queue_id"`
	Host     string `json:"host"`
	Session  string `json:"session"`
	To       string `json:"to,omitempty"`
	QueuedAt int64  `json:"queued_at"`
}

type receiptUnreachable struct {
	Host   string `json:"host"`
	Reason string `json:"reason"`
}

type receiptsResult struct {
	Receipts    []mailReceipt        `json:"receipts"`
	Waiting     []receiptWaiting     `json:"waiting,omitempty"`
	Unreachable []receiptUnreachable `json:"unreachable,omitempty"`
}

func (d *Daemon) verbAgentMessageReceipts(cs *connState, params json.RawMessage) (any, *verbError) {
	var p struct {
		Window string `json:"window"`
		Limit  int    `json:"limit"`
	}
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	if p.Window == "" {
		return nil, invalidParam("window", "window is required: the sending window's id, normally $TUIOS_PANE_ID")
	}
	limit := cmp.Or(p.Limit, receiptsDefaultLimit)
	if limit < 0 || limit > receiptsMaxLimit {
		limit = receiptsMaxLimit
	}

	live := map[string]bool{AgentInboxHuman: true}
	for _, sess := range d.manager.AllSessions() {
		for _, w := range sess.GetState().Windows {
			live[w.ID] = true
		}
	}

	if cs.viaLink {
		// Only mail that came on this link, from the window it names.
		peer := cs.linkPeer
		rows := d.agents.receipts(func(m *AgentMessage) bool {
			return m.Origin == AgentOriginLink && peer != "" && m.OriginLink == peer && m.FromLabel == p.Window
		}, live, limit, false)
		return receiptsResult{Receipts: rows}, nil
	}

	res := receiptsResult{Receipts: d.agents.receipts(func(m *AgentMessage) bool {
		return m.Origin == "" && m.From == p.Window
	}, live, limit, true)}
	res.Waiting = d.outbox.waitingFrom(p.Window)

	if d.federation != nil && len(d.federation.Table().Names()) > 0 {
		ctx, cancel := context.WithTimeout(d.ctx, receiptsHostBudget)
		answers := d.federation.CallAll(ctx, "agent-message-receipts", map[string]any{"window": p.Window, "limit": limit})
		cancel()
		for _, a := range answers {
			if a.Err != nil {
				reason := a.Err.Error()
				var remote *federation.RemoteError
				if errors.As(a.Err, &remote) && remote.Code == ErrVerbUnknownVerb {
					reason = "tuios there is too old to say"
				}
				res.Unreachable = append(res.Unreachable, receiptUnreachable{Host: a.Host, Reason: reason})
				continue
			}
			var far receiptsResult
			if jsonx.Unmarshal(a.Result, &far) != nil {
				res.Unreachable = append(res.Unreachable, receiptUnreachable{Host: a.Host, Reason: "its answer could not be read"})
				continue
			}
			for _, r := range far.Receipts {
				// The far machine's words are a claim: keep what a row needs,
				// cut to size.
				res.Receipts = append(res.Receipts, mailReceipt{
					ID: r.ID, Host: a.Host, Session: printableClaim(r.Session, agentMsgMaxSubject),
					To: printableClaim(r.To, agentMsgMaxSubject), ToLabel: printableClaim(r.ToLabel, agentMsgMaxSubject),
					ThreadID: r.ThreadID, SentAt: r.SentAt, SeenAt: r.SeenAt, ReadAt: r.ReadAt,
					Undeliverable: r.Undeliverable,
				})
			}
		}
		slices.SortStableFunc(res.Receipts, func(a, b mailReceipt) int { return cmp.Compare(a.SentAt, b.SentAt) })
		if len(res.Receipts) > limit {
			res.Receipts = res.Receipts[len(res.Receipts)-limit:]
		}
	}
	return res, nil
}

// receipts is the rows of the directed messages every ring holds that match,
// oldest first, at most limit of the newest. It marks nothing.
func (b *agentBus) receipts(match func(*AgentMessage) bool, live map[string]bool, limit int, preview bool) []mailReceipt {
	b.mu.Lock()
	defer b.mu.Unlock()
	var rows []mailReceipt
	for _, mb := range b.boxes {
		for _, m := range mb.msgs {
			if m.Kind != agentMsgDirect || m.To == "" || !match(m) {
				continue
			}
			r := mailReceipt{
				ID: m.ID, Session: m.Session, To: m.To, ToLabel: m.ToLabel, ThreadID: m.ThreadID,
				SentAt: m.SentAt, SeenAt: m.SeenAt, ReadAt: m.ReadAt, Undeliverable: !live[m.To],
			}
			if preview {
				r.Preview = oneLineOf(cmp.Or(m.Subject, m.Text), receiptsPreview)
			}
			rows = append(rows, r)
		}
	}
	slices.SortStableFunc(rows, func(a, b mailReceipt) int { return cmp.Compare(a.SentAt, b.SentAt) })
	if len(rows) > limit {
		rows = rows[len(rows)-limit:]
	}
	return rows
}

// waitingFrom is the mail window sent that waits here for a link.
func (o *hostOutbox) waitingFrom(window string) []receiptWaiting {
	if o == nil {
		return nil
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	var out []receiptWaiting
	for _, e := range o.entries {
		var f struct {
			From string `json:"from"`
		}
		if jsonx.Unmarshal(e.Params, &f) != nil || f.From != window {
			continue
		}
		out = append(out, receiptWaiting{QueueID: e.ID, Host: e.Host, Session: e.Session, To: e.To, QueuedAt: e.QueuedAt})
	}
	return out
}
