package courier

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// syncWait is how long one fetch asks the relay to hold the request when the
// client is waiting for mail. It is well under the idle limits of corporate
// proxies and ingresses, and short enough that mail another process of this
// person fetched into the store is noticed soon.
const syncWait = 10 * time.Second

// requestSlack is added to a fetch's wait for the HTTP deadline.
const requestSlack = 10 * time.Second

// ErrWaitTimeout is a Wait that ran out of time with no mail.
var ErrWaitTimeout = errors.New("no mail arrived before the timeout")

// RelayError is a refusal the relay answered with.
type RelayError struct {
	Status  int
	Code    string
	Message string
}

func (e *RelayError) Error() string {
	if e.Message == "" {
		return fmt.Sprintf("relay answered %d %s", e.Status, e.Code)
	}
	return fmt.Sprintf("relay refused (%s): %s", e.Code, e.Message)
}

// ClientOptions tune a Client. The zero value is right outside tests.
type ClientOptions struct {
	HTTP *http.Client
	Now  func() time.Time
}

// Client sends and fetches mail for one person.
type Client struct {
	cfg   *Config
	keys  *Keys
	store *Store
	http  *http.Client
	now   func() time.Time
	base  string
	gcRan bool
}

// DefaultHTTPClient is the client's HTTP client: the standard transport, which
// honours HTTPS_PROXY and NO_PROXY and trusts the system roots (SSL_CERT_FILE
// adds a corporate CA), with no overall timeout because every request carries
// its own deadline.
func DefaultHTTPClient() *http.Client {
	return &http.Client{Transport: http.DefaultTransport.(*http.Transport).Clone()}
}

// UsesEnvironmentProxy reports whether c takes its proxy from the environment.
func UsesEnvironmentProxy(c *http.Client) bool {
	t, ok := c.Transport.(*http.Transport)
	return ok && t.Proxy != nil
}

// NewClient makes a client.
func NewClient(cfg *Config, keys *Keys, store *Store, opts ClientOptions) (*Client, error) {
	if err := ValidateRelayURL(cfg.Relay); err != nil {
		return nil, err
	}
	if opts.HTTP == nil {
		opts.HTTP = DefaultHTTPClient()
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	return &Client{cfg: cfg, keys: keys, store: store, http: opts.HTTP, now: opts.Now, base: strings.TrimSuffix(cfg.Relay, "/")}, nil
}

// do makes one signed request. path is relative to the relay, with its query.
func (c *Client) do(ctx context.Context, method, path string, body []byte, out any) error {
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", SignRequest(c.keys, method, path, body, c.now()))
	if body != nil {
		req.Header.Set("Content-Type", "application/octet-stream")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("relay %s: %w", c.cfg.Relay, err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if err != nil {
		return fmt.Errorf("relay %s: %w", c.cfg.Relay, err)
	}
	if resp.StatusCode/100 != 2 {
		re := &RelayError{Status: resp.StatusCode}
		var e struct {
			Error   string `json:"error"`
			Message string `json:"message"`
		}
		if json.Unmarshal(data, &e) == nil && e.Error != "" {
			re.Code, re.Message = e.Error, CleanText(e.Message)
		} else {
			// Not the relay: a proxy or an ingress answered.
			re.Code = http.StatusText(resp.StatusCode)
			re.Message = "the answer did not come from a tuios-courier relay; check the relay URL and any proxy in between"
		}
		return re
	}
	if out != nil {
		if err := json.Unmarshal(data, out); err != nil {
			return fmt.Errorf("relay %s answered something that is not a relay reply: %v", c.cfg.Relay, err)
		}
	}
	return nil
}

// Outgoing is a message to send.
type Outgoing struct {
	// To is a peer name.
	To        string
	Agent     string
	FromAgent string
	Subject   string
	Body      string
	// Thread and ReplyTo continue a conversation; empty starts one.
	Thread  string
	ReplyTo string
}

// Send seals and posts a message to a peer and records it as sent.
func (c *Client) Send(ctx context.Context, o Outgoing) (Message, error) {
	peer, ok := c.cfg.Peers[o.To]
	if !ok {
		return Message{}, fmt.Errorf("no peer named %q: run tuios-courier peers to list them, or tuios-courier peers add NAME IDENTITY", clip(o.To))
	}
	m := Message{
		V:         1,
		ID:        NewID(),
		Thread:    o.Thread,
		ReplyTo:   o.ReplyTo,
		Agent:     o.Agent,
		FromAgent: o.FromAgent,
		Subject:   o.Subject,
		Body:      o.Body,
		SentAt:    c.now().UTC(),
	}
	if m.Thread == "" {
		m.Thread = m.ID
	}
	box, err := Seal(c.keys, peer.Identity, m)
	if err != nil {
		return Message{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if err := c.do(ctx, http.MethodPost, "/v1/mail/"+peer.Identity.MailboxID(), box, nil); err != nil {
		return Message{}, err
	}
	if err := c.store.RecordSent(SentRecord{Msg: m, To: peer.Identity.String(), Peer: peer.Name}); err != nil {
		return m, fmt.Errorf("sent, but could not record it: %v", err)
	}
	return m, nil
}

// Reply answers a received message, in its thread, to its sender, labeled
// for the agent that wrote it.
func (c *Client) Reply(ctx context.Context, ref string, o Outgoing) (Message, error) {
	e, err := c.store.Find(ref)
	if err != nil {
		return Message{}, err
	}
	from, err := ParseIdentity(e.From)
	if err != nil {
		return Message{}, err
	}
	peer, ok := c.cfg.Lookup(from)
	if !ok {
		return Message{}, fmt.Errorf("the sender of %s is no longer in peers", e.Msg.ID[:8])
	}
	o.To, o.Thread, o.ReplyTo = peer, e.Msg.Thread, e.Msg.ID
	if o.Agent == "" {
		o.Agent = e.Msg.FromAgent
	}
	return c.Send(ctx, o)
}

// SyncResult says what one sync did.
type SyncResult struct {
	// New is messages stored; Held is how many of them wait for the person.
	New  int
	Held int
	// Pending is boxes kept for senders not yet in peers.
	Pending int
	// Rejected counts refused boxes by reason.
	Rejected map[string]int
}

type fetchedBox struct {
	ID         string    `json:"id"`
	From       string    `json:"from"`
	ReceivedAt time.Time `json:"received_at"`
	Box        []byte    `json:"box"`
}

// Sync fetches mail from the relay, waiting up to wait for some to arrive,
// opens and stores it, and acks it. Boxes kept from senders not in peers are
// tried again first.
func (c *Client) Sync(ctx context.Context, wait time.Duration) (SyncResult, error) {
	res := SyncResult{Rejected: map[string]int{}}
	if !c.gcRan {
		c.store.GC()
		c.gcRan = true
	}
	for _, p := range c.store.Pending() {
		from, err := ParseIdentity(p.From)
		if err != nil {
			c.store.RemovePending(p.RelayID)
			continue
		}
		if c.accept(fetchedBox{ID: p.RelayID, From: p.From, ReceivedAt: p.ReceivedAt, Box: p.Box}, from, &res, true) {
			c.store.RemovePending(p.RelayID)
		}
	}

	secs := int(wait / time.Second)
	rctx, cancel := context.WithTimeout(ctx, time.Duration(secs)*time.Second+requestSlack)
	defer cancel()
	var got struct {
		Messages []fetchedBox `json:"messages"`
	}
	if err := c.do(rctx, http.MethodGet, fmt.Sprintf("/v1/mail?wait=%d", secs), nil, &got); err != nil {
		res.Pending = len(c.store.Pending())
		return res, err
	}
	var acks []string
	for _, fb := range got.Messages {
		if !ValidID(fb.ID) {
			continue
		}
		from, err := ParseIdentity(fb.From)
		if err != nil {
			res.Rejected[ReasonInvalid]++
			acks = append(acks, fb.ID)
			continue
		}
		c.accept(fb, from, &res, false)
		acks = append(acks, fb.ID)
	}
	res.Pending = len(c.store.Pending())
	for len(acks) > 0 {
		n := min(len(acks), 256)
		body, _ := json.Marshal(map[string][]string{"ids": acks[:n]})
		actx, acancel := context.WithTimeout(ctx, 30*time.Second)
		err := c.do(actx, http.MethodPost, "/v1/ack", body, nil)
		acancel()
		if err != nil {
			// Not acked is fetched again, and the store drops the repeat.
			return res, err
		}
		acks = acks[n:]
	}
	return res, nil
}

// accept opens one box and stores it. It reports whether the box is finished
// with: stored, refused for good, or a duplicate. A box from a sender not in
// peers is kept (when it is not already a kept one) and is not finished.
func (c *Client) accept(fb fetchedBox, relayFrom Identity, res *SyncResult, fromPending bool) bool {
	op, err := Open(c.keys, fb.Box, c.cfg.Lookup)
	if reason := RejectReason(err); reason == ReasonUnknownSender {
		if !fromPending {
			if err := c.store.SavePending(fb.ID, relayFrom, fb.Box, c.now()); err != nil {
				res.Rejected[ReasonUnknownSender]++
			}
		}
		return false
	} else if err != nil {
		if reason == "" {
			reason = ReasonUndecryptable
		}
		res.Rejected[reason]++
		return true
	}
	if !op.From.Equal(relayFrom) {
		res.Rejected[ReasonFromMismatch]++
		return true
	}
	now := c.now()
	if age := now.Sub(op.Msg.SentAt); age > MaxMessageAge || age < -maxClockAhead {
		res.Rejected[ReasonStale]++
		return true
	}
	rec := Record{Msg: op.Msg, From: op.From.String(), Peer: op.Peer, RelayID: fb.ID, ReceivedAt: now.UTC()}
	added, err := c.store.Put(rec)
	if err != nil || !added {
		return err == nil
	}
	res.New++
	if c.releases(op) {
		_ = c.store.Release(rec.Key())
	} else {
		res.Held++
	}
	return true
}

// releases decides whether new mail goes straight to the agents.
func (c *Client) releases(op Opened) bool {
	if c.cfg.Peers[op.Peer].Release == ReleaseAuto {
		return true
	}
	return c.cfg.ReplyRelease == ReleaseAuto && op.Msg.Thread != op.Msg.ID && c.store.SentInto(op.Msg.Thread, op.From)
}

// Wait returns the deliverable mail for f, syncing until some arrives or
// timeout passes. It does not mark anything read: hand the result to
// Store.Deliver. A relay that cannot be reached is retried until the timeout;
// the last failure is joined to ErrWaitTimeout.
func (c *Client) Wait(ctx context.Context, f Filter, timeout time.Duration) ([]Entry, error) {
	deadline := time.Now().Add(timeout)
	var lastErr error
	for {
		if got := c.store.Deliverable(f); len(got) > 0 {
			return got, nil
		}
		left := time.Until(deadline)
		if left <= 0 || ctx.Err() != nil {
			return nil, errors.Join(ErrWaitTimeout, lastErr, ctx.Err())
		}
		wait := min(left.Truncate(time.Second), syncWait)
		sctx, cancel := context.WithDeadline(ctx, deadline)
		_, err := c.Sync(sctx, wait)
		cancel()
		if err == nil {
			lastErr = nil
			if wait < time.Second {
				// Less than a second left: nothing can be asked to wait.
				// One last look at the store, then give up.
				if got := c.store.Deliverable(f); len(got) > 0 {
					return got, nil
				}
				return nil, ErrWaitTimeout
			}
			continue
		}
		lastErr = err
		select {
		case <-ctx.Done():
		case <-time.After(min(2*time.Second, max(time.Until(deadline), 0))):
		}
	}
}
