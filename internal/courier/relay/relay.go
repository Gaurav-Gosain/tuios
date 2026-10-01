// Package relay is the tuios-courier relay: an HTTPS mailbox that holds sealed
// mail for the identities on its roster until they fetch it.
//
// It cannot read what it holds. What it knows is who sent each box to whom and
// when, because every request is signed by the identity making it. It serves
// the roster and nobody else: an identity not on it can neither send, nor
// fetch, nor be sent to.
package relay

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/courier"
	"github.com/Gaurav-Gosain/tuios/internal/netutil"
)

// Defaults for Options.
const (
	DefaultTTL             = courier.MaxRelayTTL
	DefaultMaxBoxBytes     = 192 << 10
	DefaultMaxPerMailbox   = 512
	DefaultMaxMailboxBytes = 32 << 20
	DefaultMaxTotalBytes   = 1 << 30
	DefaultSendsPerMinute  = 60
	// DefaultMaxWait is the longest a fetch waits for mail. It stays under
	// the sixty seconds an nginx ingress allows a response by default; the
	// client asks for less and asks again.
	DefaultMaxWait = 50 * time.Second
)

const (
	// maxFetch is how many messages one fetch returns.
	maxFetch = 64
	// maxAckIDs is how many ids one ack may name.
	maxAckIDs = 256
	// maxAckBody bounds the ack request body.
	maxAckBody = 64 << 10
	// rosterCheckEvery is how often the roster file's mtime is looked at.
	rosterCheckEvery = 10 * time.Second
)

// Options configure a relay.
type Options struct {
	// Roster is the file of identities the relay serves. Required.
	Roster string
	// DataDir keeps mail across restarts. Empty keeps it in memory only.
	DataDir string
	// Prefix is a path the relay is served under, for an ingress that does
	// not strip it. Requests are signed over the path after it.
	Prefix string

	TTL             time.Duration
	MaxBoxBytes     int
	MaxPerMailbox   int
	MaxMailboxBytes int64
	MaxTotalBytes   int64
	SendsPerMinute  int
	MaxWait         time.Duration

	// Now is the clock. Nil is time.Now.
	Now func() time.Time
	// Logf reports what an operator should see. Nil discards it.
	Logf func(format string, args ...any)
}

func (o *Options) fill() error {
	if o.Roster == "" {
		return errors.New("a relay needs a roster file (--roster)")
	}
	if o.TTL == 0 {
		o.TTL = DefaultTTL
	}
	if o.TTL < 0 || o.TTL > courier.MaxRelayTTL {
		return fmt.Errorf("ttl %v is outside 0 to %v: clients refuse mail older than %v", o.TTL, courier.MaxRelayTTL, courier.MaxMessageAge)
	}
	if o.MaxBoxBytes <= 0 {
		o.MaxBoxBytes = DefaultMaxBoxBytes
	}
	if o.MaxPerMailbox <= 0 {
		o.MaxPerMailbox = DefaultMaxPerMailbox
	}
	if o.MaxMailboxBytes <= 0 {
		o.MaxMailboxBytes = DefaultMaxMailboxBytes
	}
	if o.MaxTotalBytes <= 0 {
		o.MaxTotalBytes = DefaultMaxTotalBytes
	}
	if o.SendsPerMinute <= 0 {
		o.SendsPerMinute = DefaultSendsPerMinute
	}
	if o.MaxWait <= 0 {
		o.MaxWait = DefaultMaxWait
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.Logf == nil {
		o.Logf = func(string, ...any) {}
	}
	o.Prefix = strings.TrimSuffix(o.Prefix, "/")
	if o.Prefix != "" && !strings.HasPrefix(o.Prefix, "/") {
		o.Prefix = "/" + o.Prefix
	}
	return nil
}

// Server is a relay.
type Server struct {
	opts     Options
	verifier *courier.Verifier
	roster   *roster
	store    *store

	rateMu sync.Mutex
	sends  map[courier.Identity][]time.Time
}

// New makes a relay, reading its roster and any mail in DataDir.
func New(opts Options) (*Server, error) {
	if err := opts.fill(); err != nil {
		return nil, err
	}
	ro, err := loadRoster(opts.Roster, opts.Now)
	if err != nil {
		return nil, err
	}
	st, err := openStore(opts)
	if err != nil {
		return nil, err
	}
	return &Server{
		opts:     opts,
		verifier: courier.NewVerifier(opts.Now),
		roster:   ro,
		store:    st,
		sends:    map[courier.Identity][]time.Time{},
	}, nil
}

// Sweep drops expired mail and forgotten nonces. Run calls it every minute.
func (s *Server) Sweep() {
	if n := s.store.expire(s.opts.Now()); n > 0 {
		s.opts.Logf("expired %d message(s)", n)
	}
	s.verifier.Prune()
	s.rateMu.Lock()
	cutoff := s.opts.Now().Add(-time.Minute)
	for id, ts := range s.sends {
		if len(ts) == 0 || ts[len(ts)-1].Before(cutoff) {
			delete(s.sends, id)
		}
	}
	s.rateMu.Unlock()
}

func (s *Server) stored() int { return s.store.count() }

// CheckBind refuses clear text on a network address unless the operator said
// TLS is terminated in front of the relay. It is the rule tuios-web applies.
func CheckBind(addr string, tls, insecure bool) error {
	host := addr
	if h, _, err := net.SplitHostPort(addr); err == nil {
		host = h
	}
	if tls || insecure || netutil.IsLoopbackHost(host) {
		return nil
	}
	return fmt.Errorf("%s is reachable from the network and has no TLS: pass --tls-cert and --tls-key, or --insecure when an ingress terminates TLS in front of the relay", addr)
}

// HTTPServer is an http.Server for the relay with timeouts that bound every
// phase of a request. WriteTimeout sits above the longest wait.
func (s *Server) HTTPServer(addr string) *http.Server {
	return &http.Server{
		Addr:              addr,
		Handler:           s.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      s.opts.MaxWait + 20*time.Second,
		IdleTimeout:       120 * time.Second,
		MaxHeaderBytes:    16 << 10,
	}
}

// Handler serves the relay's API.
func (s *Server) Handler() http.Handler { return http.HandlerFunc(s.serve) }

type apiError struct {
	status int
	code   string
	msg    string
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, e apiError) {
	writeJSON(w, e.status, map[string]string{"error": e.code, "message": e.msg})
}

var (
	errNotFound     = apiError{http.StatusNotFound, "not_found", "no such endpoint"}
	errMethod       = apiError{http.StatusMethodNotAllowed, "bad_request", "method not allowed"}
	errUnauthorized = apiError{http.StatusUnauthorized, "unauthorized", "the request is not signed by an identity this relay serves"}
)

func (s *Server) serve(w http.ResponseWriter, r *http.Request) {
	path, ok := strings.CutPrefix(r.URL.EscapedPath(), s.opts.Prefix)
	if !ok || !strings.HasPrefix(path, "/") {
		writeError(w, errNotFound)
		return
	}
	if path == "/v1/healthz" {
		_, _ = io.WriteString(w, "ok\n")
		return
	}
	if !strings.HasPrefix(path, "/v1/") {
		writeError(w, errNotFound)
		return
	}

	limit := int64(0)
	if r.Method == http.MethodPost {
		limit = int64(s.opts.MaxBoxBytes)
		if path == "/v1/ack" {
			limit = maxAckBody
		}
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, limit+1))
	if err != nil {
		writeError(w, apiError{http.StatusBadRequest, "bad_request", "could not read the body"})
		return
	}
	if int64(len(body)) > limit {
		writeError(w, apiError{http.StatusRequestEntityTooLarge, "too_large", fmt.Sprintf("the body is over %d bytes", limit)})
		return
	}

	signed := path
	if r.URL.RawQuery != "" {
		signed += "?" + r.URL.RawQuery
	}
	ro := s.roster.current()
	caller, err := s.verifier.Verify(r.Header.Get("Authorization"), r.Method, signed, body, ro.has)
	if err != nil {
		writeError(w, errUnauthorized)
		return
	}

	switch {
	case path == "/v1/mail":
		if r.Method != http.MethodGet {
			writeError(w, errMethod)
			return
		}
		s.fetch(w, r, caller)
	case path == "/v1/ack":
		if r.Method != http.MethodPost {
			writeError(w, errMethod)
			return
		}
		s.ack(w, caller, body)
	case strings.HasPrefix(path, "/v1/mail/"):
		if r.Method != http.MethodPost {
			writeError(w, errMethod)
			return
		}
		to, ok := ro.byMailbox(strings.TrimPrefix(path, "/v1/mail/"))
		if !ok {
			writeError(w, apiError{http.StatusNotFound, "unknown_recipient", "no identity on this relay has that mailbox"})
			return
		}
		s.send(w, caller, to, body)
	default:
		writeError(w, errNotFound)
	}
}

func (s *Server) allowSend(from courier.Identity) bool {
	s.rateMu.Lock()
	defer s.rateMu.Unlock()
	now := s.opts.Now()
	cutoff := now.Add(-time.Minute)
	ts := s.sends[from]
	keep := ts[:0]
	for _, t := range ts {
		if t.After(cutoff) {
			keep = append(keep, t)
		}
	}
	if len(keep) >= s.opts.SendsPerMinute {
		s.sends[from] = keep
		return false
	}
	s.sends[from] = append(keep, now)
	return true
}

func (s *Server) send(w http.ResponseWriter, from, to courier.Identity, box []byte) {
	if len(box) == 0 {
		writeError(w, apiError{http.StatusBadRequest, "bad_request", "the box is empty"})
		return
	}
	if !s.allowSend(from) {
		writeError(w, apiError{http.StatusTooManyRequests, "rate_limited", fmt.Sprintf("at most %d sends a minute", s.opts.SendsPerMinute)})
		return
	}
	m, err := s.store.put(to.MailboxID(), from, box, s.opts.Now())
	if err != nil {
		var full errFull
		if errors.As(err, &full) {
			writeError(w, apiError{http.StatusInsufficientStorage, "mailbox_full", string(full)})
			return
		}
		s.opts.Logf("store: %v", err)
		writeError(w, apiError{http.StatusInternalServerError, "internal", "could not store the message"})
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"id": m.ID, "expires_at": m.ExpiresAt})
}

type fetchedMessage struct {
	ID         string    `json:"id"`
	From       string    `json:"from"`
	ReceivedAt time.Time `json:"received_at"`
	Box        []byte    `json:"box"`
}

func (s *Server) fetch(w http.ResponseWriter, r *http.Request, caller courier.Identity) {
	wait := time.Duration(0)
	if q := r.URL.Query().Get("wait"); q != "" {
		n, err := strconv.Atoi(q)
		if err != nil || n < 0 {
			writeError(w, apiError{http.StatusBadRequest, "bad_request", "wait is a number of seconds"})
			return
		}
		wait = min(time.Duration(n)*time.Second, s.opts.MaxWait)
	}
	mailbox := caller.MailboxID()
	deadline := time.NewTimer(wait)
	defer deadline.Stop()
	for {
		msgs, changed := s.store.list(mailbox, s.opts.Now(), maxFetch)
		if len(msgs) > 0 || wait == 0 {
			out := make([]fetchedMessage, len(msgs))
			for i, m := range msgs {
				out[i] = fetchedMessage{ID: m.ID, From: m.From.String(), ReceivedAt: m.ReceivedAt, Box: m.Box}
			}
			writeJSON(w, http.StatusOK, map[string]any{"messages": out})
			return
		}
		select {
		case <-changed:
		case <-deadline.C:
			wait = 0
		case <-r.Context().Done():
			return
		}
	}
}

func (s *Server) ack(w http.ResponseWriter, caller courier.Identity, body []byte) {
	var req struct {
		IDs []string `json:"ids"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		writeError(w, apiError{http.StatusBadRequest, "bad_request", "the body is not {\"ids\": [...]}"})
		return
	}
	if len(req.IDs) > maxAckIDs {
		writeError(w, apiError{http.StatusBadRequest, "bad_request", fmt.Sprintf("at most %d ids", maxAckIDs)})
		return
	}
	for _, id := range req.IDs {
		if !courier.ValidID(id) {
			writeError(w, apiError{http.StatusBadRequest, "bad_request", "an id is not 32 lowercase hex"})
			return
		}
	}
	n := s.store.remove(caller.MailboxID(), req.IDs)
	writeJSON(w, http.StatusOK, map[string]int{"acked": n})
}
