package relay

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/courier"
)

type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *fakeClock) Now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

type rig struct {
	t      *testing.T
	clock  *fakeClock
	srv    *Server
	http   *httptest.Server
	roster string
	prefix string
	keys   map[string]*courier.Keys
}

func mustKeys(t *testing.T) *courier.Keys {
	t.Helper()
	k, err := courier.GenerateKeys()
	if err != nil {
		t.Fatal(err)
	}
	return k
}

// writeRoster writes a roster of the named keys.
func writeRoster(t *testing.T, path string, keys map[string]*courier.Keys) {
	t.Helper()
	var b strings.Builder
	b.WriteString("# the team\n\n")
	for name, k := range keys {
		fmt.Fprintf(&b, "%s %s  # %s\n", name, k.Identity(), name)
	}
	if err := os.WriteFile(path, []byte(b.String()), 0o600); err != nil {
		t.Fatal(err)
	}
}

func newRig(t *testing.T, mutate func(*Options), names ...string) *rig {
	t.Helper()
	if len(names) == 0 {
		names = []string{"ghaith", "gg"}
	}
	r := &rig{t: t, clock: &fakeClock{t: time.Unix(1_800_000_000, 0)}, keys: map[string]*courier.Keys{}}
	for _, n := range names {
		r.keys[n] = mustKeys(t)
	}
	r.roster = filepath.Join(t.TempDir(), "roster")
	writeRoster(t, r.roster, r.keys)
	opts := Options{Roster: r.roster, Now: r.clock.Now}
	if mutate != nil {
		mutate(&opts)
	}
	r.prefix = opts.Prefix
	srv, err := New(opts)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	r.srv = srv
	r.http = httptest.NewServer(srv.Handler())
	t.Cleanup(r.http.Close)
	return r
}

type reply struct {
	status int
	body   []byte
}

func (rp reply) errCode() string {
	var e struct {
		Error string `json:"error"`
	}
	_ = json.Unmarshal(rp.body, &e)
	return e.Error
}

// do makes a request signed by who (nil for none) to path under the prefix.
func (r *rig) do(who *courier.Keys, method, path string, body []byte) reply {
	r.t.Helper()
	req, err := http.NewRequest(method, r.http.URL+r.prefix+path, bytes.NewReader(body))
	if err != nil {
		r.t.Fatal(err)
	}
	if who != nil {
		req.Header.Set("Authorization", courier.SignRequest(who, method, path, body, r.clock.Now()))
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		r.t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return reply{resp.StatusCode, b}
}

func (r *rig) send(from, to string, box []byte) reply {
	return r.do(r.keys[from], "POST", "/v1/mail/"+r.keys[to].Identity().MailboxID(), box)
}

type fetched struct {
	Messages []struct {
		ID         string    `json:"id"`
		From       string    `json:"from"`
		ReceivedAt time.Time `json:"received_at"`
		Box        []byte    `json:"box"`
	} `json:"messages"`
}

func (r *rig) fetch(who string, wait int) fetched {
	r.t.Helper()
	rp := r.do(r.keys[who], "GET", fmt.Sprintf("/v1/mail?wait=%d", wait), nil)
	if rp.status != 200 {
		r.t.Fatalf("fetch for %s: %d %s", who, rp.status, rp.body)
	}
	var f fetched
	if err := json.Unmarshal(rp.body, &f); err != nil {
		r.t.Fatal(err)
	}
	return f
}

func (r *rig) ack(who string, ids ...string) reply {
	body, _ := json.Marshal(map[string][]string{"ids": ids})
	return r.do(r.keys[who], "POST", "/v1/ack", body)
}

func TestSendFetchAck(t *testing.T) {
	r := newRig(t, nil)
	if rp := r.send("ghaith", "gg", []byte("sealed-1")); rp.status != http.StatusAccepted {
		t.Fatalf("send: %d %s", rp.status, rp.body)
	}
	f := r.fetch("gg", 0)
	if len(f.Messages) != 1 || string(f.Messages[0].Box) != "sealed-1" || f.Messages[0].From != r.keys["ghaith"].Identity().String() {
		t.Fatalf("fetch: %+v", f)
	}
	if !courier.ValidID(f.Messages[0].ID) {
		t.Fatalf("relay id %q is not hex32", f.Messages[0].ID)
	}
	// Ghaith's own mailbox is empty: a fetch reads the caller's mailbox only.
	if f := r.fetch("ghaith", 0); len(f.Messages) != 0 {
		t.Fatalf("the sender's fetch saw %d messages", len(f.Messages))
	}
	// Ghaith cannot delete gg's mail by its id.
	r.ack("ghaith", f.Messages[0].ID)
	if f := r.fetch("gg", 0); len(f.Messages) != 1 {
		t.Fatal("another identity's ack deleted the message")
	}
	if rp := r.ack("gg", f.Messages[0].ID); rp.status != 200 {
		t.Fatalf("ack: %d %s", rp.status, rp.body)
	}
	if f := r.fetch("gg", 0); len(f.Messages) != 0 {
		t.Fatal("the acked message is still there")
	}
}

func TestFetchOrderAndLimit(t *testing.T) {
	r := newRig(t, func(o *Options) { o.SendsPerMinute = 1000 })
	for i := range maxFetch + 5 {
		r.clock.Advance(time.Millisecond)
		if rp := r.send("ghaith", "gg", fmt.Appendf(nil, "m%03d", i)); rp.status != http.StatusAccepted {
			t.Fatalf("send %d: %d", i, rp.status)
		}
	}
	f := r.fetch("gg", 0)
	if len(f.Messages) != maxFetch {
		t.Fatalf("fetch returned %d, want %d", len(f.Messages), maxFetch)
	}
	for i, m := range f.Messages {
		if want := fmt.Sprintf("m%03d", i); string(m.Box) != want {
			t.Fatalf("message %d is %q, want %q: oldest first", i, m.Box, want)
		}
	}
}

func TestAuthRequired(t *testing.T) {
	r := newRig(t, nil)
	outsider := mustKeys(t)
	gg := r.keys["gg"].Identity().MailboxID()
	if rp := r.do(nil, "GET", "/v1/mail", nil); rp.status != http.StatusUnauthorized {
		t.Fatalf("unsigned fetch: %d", rp.status)
	}
	if rp := r.do(outsider, "POST", "/v1/mail/"+gg, []byte("x")); rp.status != http.StatusUnauthorized || rp.errCode() != "unauthorized" {
		t.Fatalf("outsider send: %d %s", rp.status, rp.body)
	}
	if rp := r.do(r.keys["ghaith"], "POST", "/v1/mail/"+outsider.Identity().MailboxID(), []byte("x")); rp.status != http.StatusNotFound || rp.errCode() != "unknown_recipient" {
		t.Fatalf("send to outsider: %d %s", rp.status, rp.body)
	}
	if rp := r.do(nil, "GET", "/v1/healthz", nil); rp.status != 200 || strings.TrimSpace(string(rp.body)) != "ok" {
		t.Fatalf("healthz: %d %q", rp.status, rp.body)
	}
}

func TestBadRequests(t *testing.T) {
	r := newRig(t, nil)
	g := r.keys["ghaith"]
	for name, c := range map[string]struct {
		method, path string
		body         []byte
		status       int
	}{
		"traversal mailbox": {"POST", "/v1/mail/..%2f..%2fetc", []byte("x"), http.StatusNotFound},
		"short mailbox":     {"POST", "/v1/mail/abc", []byte("x"), http.StatusNotFound},
		"empty box":         {"POST", "/v1/mail/" + r.keys["gg"].Identity().MailboxID(), nil, http.StatusBadRequest},
		"traversal ack":     {"POST", "/v1/ack", []byte(`{"ids":["../../x"]}`), http.StatusBadRequest},
		"ack not json":      {"POST", "/v1/ack", []byte(`ids=1`), http.StatusBadRequest},
		"wait negative":     {"GET", "/v1/mail?wait=-1", nil, http.StatusBadRequest},
		"wait word":         {"GET", "/v1/mail?wait=soon", nil, http.StatusBadRequest},
		"unknown path":      {"GET", "/v1/nothing", nil, http.StatusNotFound},
		"wrong method":      {"DELETE", "/v1/mail", nil, http.StatusMethodNotAllowed},
	} {
		if rp := r.do(g, c.method, c.path, c.body); rp.status != c.status {
			t.Errorf("%s: %d %s, want %d", name, rp.status, rp.body, c.status)
		}
	}
	ids := make([]string, maxAckIDs+1)
	for i := range ids {
		ids[i] = courier.NewID()
	}
	if rp := r.ack("ghaith", ids...); rp.status != http.StatusBadRequest {
		t.Errorf("an ack of %d ids: %d", len(ids), rp.status)
	}
}

func TestLimits(t *testing.T) {
	r := newRig(t, func(o *Options) {
		o.MaxBoxBytes = 100
		o.MaxPerMailbox = 3
		o.SendsPerMinute = 5
	})
	if rp := r.send("ghaith", "gg", bytes.Repeat([]byte("x"), 101)); rp.status != http.StatusRequestEntityTooLarge || rp.errCode() != "too_large" {
		t.Fatalf("oversize: %d %s", rp.status, rp.body)
	}
	for i := range 3 {
		if rp := r.send("ghaith", "gg", []byte("ok")); rp.status != http.StatusAccepted {
			t.Fatalf("send %d: %d", i, rp.status)
		}
	}
	if rp := r.send("ghaith", "gg", []byte("ok")); rp.status != http.StatusInsufficientStorage || rp.errCode() != "mailbox_full" {
		t.Fatalf("full mailbox: %d %s", rp.status, rp.body)
	}
	// The fifth send in the minute is allowed and the sixth is not, whatever
	// the mailbox it is for.
	if rp := r.send("ghaith", "ghaith", []byte("ok")); rp.status != http.StatusAccepted {
		t.Fatalf("fifth send: %d %s", rp.status, rp.body)
	}
	if rp := r.send("ghaith", "ghaith", []byte("ok")); rp.status != http.StatusTooManyRequests || rp.errCode() != "rate_limited" {
		t.Fatalf("sixth send: %d %s", rp.status, rp.body)
	}
	r.clock.Advance(61 * time.Second)
	if rp := r.send("ghaith", "ghaith", []byte("ok")); rp.status != http.StatusAccepted {
		t.Fatalf("a send after the minute: %d %s", rp.status, rp.body)
	}
}

func TestTotalBytesLimit(t *testing.T) {
	r := newRig(t, func(o *Options) { o.MaxBoxBytes = 100; o.MaxTotalBytes = 250 }, "a", "b", "c")
	if rp := r.send("a", "b", bytes.Repeat([]byte("x"), 100)); rp.status != http.StatusAccepted {
		t.Fatal(rp.status)
	}
	if rp := r.send("a", "c", bytes.Repeat([]byte("x"), 100)); rp.status != http.StatusAccepted {
		t.Fatal(rp.status)
	}
	if rp := r.send("b", "a", bytes.Repeat([]byte("x"), 100)); rp.status != http.StatusInsufficientStorage {
		t.Fatalf("over the total: %d %s", rp.status, rp.body)
	}
}

func TestLongPoll(t *testing.T) {
	r := newRig(t, nil)
	done := make(chan fetched, 1)
	start := time.Now()
	go func() { done <- r.fetch("gg", 10) }()
	time.Sleep(150 * time.Millisecond)
	r.send("ghaith", "gg", []byte("wake"))
	select {
	case f := <-done:
		if len(f.Messages) != 1 {
			t.Fatalf("woke with %d messages", len(f.Messages))
		}
		if time.Since(start) > 5*time.Second {
			t.Fatal("the long poll did not wake on arrival")
		}
	case <-time.After(8 * time.Second):
		t.Fatal("the long poll never returned")
	}

	start = time.Now()
	if f := r.fetch("ghaith", 1); len(f.Messages) != 0 {
		t.Fatal("an empty mailbox returned mail")
	}
	if d := time.Since(start); d < 900*time.Millisecond || d > 3*time.Second {
		t.Fatalf("a wait of 1 s took %v", d)
	}
}

func TestWaitIsCapped(t *testing.T) {
	r := newRig(t, func(o *Options) { o.MaxWait = time.Second })
	start := time.Now()
	r.fetch("gg", 50)
	if d := time.Since(start); d > 3*time.Second {
		t.Fatalf("wait=50 with a 1 s cap took %v", d)
	}
}

func TestTTL(t *testing.T) {
	r := newRig(t, func(o *Options) { o.TTL = time.Hour })
	r.send("ghaith", "gg", []byte("old"))
	r.clock.Advance(time.Hour + time.Second)
	if f := r.fetch("gg", 0); len(f.Messages) != 0 {
		t.Fatal("an expired message was handed out")
	}
	r.srv.Sweep()
	if n := r.srv.stored(); n != 0 {
		t.Fatalf("%d expired messages kept after a sweep", n)
	}
}

func TestTTLIsCapped(t *testing.T) {
	_, err := New(Options{Roster: writeTempRoster(t), TTL: courier.MaxRelayTTL + time.Hour})
	if err == nil {
		t.Fatal("a TTL above MaxRelayTTL was accepted")
	}
}

func writeTempRoster(t *testing.T) string {
	path := filepath.Join(t.TempDir(), "roster")
	writeRoster(t, path, map[string]*courier.Keys{"a": mustKeys(t)})
	return path
}

func TestPersistence(t *testing.T) {
	data := t.TempDir()
	r := newRig(t, func(o *Options) { o.DataDir = data })
	r.send("ghaith", "gg", []byte("survives"))
	r.send("ghaith", "gg", []byte("acked"))
	f := r.fetch("gg", 0)
	r.ack("gg", f.Messages[1].ID)

	// Junk in the data directory is skipped, not fatal.
	mb := filepath.Join(data, r.keys["gg"].Identity().MailboxID())
	os.WriteFile(filepath.Join(mb, courier.NewID()+".msg"), []byte("{not json"), 0o600)
	os.WriteFile(filepath.Join(mb, "stray.txt"), []byte("x"), 0o600)
	os.MkdirAll(filepath.Join(data, "not-a-mailbox"), 0o700)

	again, err := New(Options{Roster: r.roster, DataDir: data, Now: r.clock.Now})
	if err != nil {
		t.Fatalf("New over the same data: %v", err)
	}
	r.http.Config.Handler = again.Handler()
	r.srv = again
	f = r.fetch("gg", 0)
	if len(f.Messages) != 1 || string(f.Messages[0].Box) != "survives" {
		t.Fatalf("after a restart: %+v", f)
	}
	r.ack("gg", f.Messages[0].ID)
	entries, _ := os.ReadDir(mb)
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".msg") && e.Name() != "stray.txt" {
			if b, _ := os.ReadFile(filepath.Join(mb, e.Name())); bytes.HasPrefix(b, []byte("{\"")) {
				t.Fatalf("an acked message file is still on disk: %s", e.Name())
			}
		}
	}
}

func TestPrefix(t *testing.T) {
	r := newRig(t, func(o *Options) { o.Prefix = "/courier" })
	if rp := r.send("ghaith", "gg", []byte("x")); rp.status != http.StatusAccepted {
		t.Fatalf("send under the prefix: %d %s", rp.status, rp.body)
	}
	r.prefix = ""
	if rp := r.do(r.keys["gg"], "GET", "/v1/mail", nil); rp.status != http.StatusNotFound {
		t.Fatalf("a request outside the prefix: %d", rp.status)
	}
}

func TestRosterReload(t *testing.T) {
	r := newRig(t, nil)
	newcomer := mustKeys(t)
	if rp := r.do(newcomer, "GET", "/v1/mail", nil); rp.status != http.StatusUnauthorized {
		t.Fatalf("before the roster names them: %d", rp.status)
	}
	keys := map[string]*courier.Keys{"ghaith": r.keys["ghaith"], "zain": newcomer}
	writeRoster(t, r.roster, keys)
	later := time.Now().Add(time.Minute)
	os.Chtimes(r.roster, later, later)
	r.clock.Advance(rosterCheckEvery + time.Second)
	if rp := r.do(newcomer, "GET", "/v1/mail", nil); rp.status != 200 {
		t.Fatalf("after the roster names them: %d %s", rp.status, rp.body)
	}
	// gg left the roster: they can no longer fetch, and nobody can mail them.
	if rp := r.do(r.keys["gg"], "GET", "/v1/mail", nil); rp.status != http.StatusUnauthorized {
		t.Fatalf("a removed identity fetched: %d", rp.status)
	}
	if rp := r.send("ghaith", "gg", []byte("x")); rp.status != http.StatusNotFound {
		t.Fatalf("mail to a removed identity: %d", rp.status)
	}
}

func TestRosterRejectsGarbage(t *testing.T) {
	path := filepath.Join(t.TempDir(), "roster")
	for _, body := range []string{"gg tc1.nope\n", "a b c\n", ""} {
		os.WriteFile(path, []byte(body), 0o600)
		if _, err := New(Options{Roster: path}); err == nil {
			t.Errorf("roster %q accepted", body)
		}
	}
	if _, err := New(Options{Roster: filepath.Join(t.TempDir(), "missing")}); err == nil {
		t.Error("a missing roster was accepted")
	}
}

func TestCheckBind(t *testing.T) {
	for _, c := range []struct {
		addr          string
		tls, insecure bool
		ok            bool
	}{
		{"127.0.0.1:8080", false, false, true},
		{"localhost:8080", false, false, true},
		{"[::1]:8080", false, false, true},
		{":8080", false, false, false},
		{"0.0.0.0:8080", false, false, false},
		{"0.0.0.0:8080", true, false, true},
		{"0.0.0.0:8080", false, true, true},
	} {
		if err := CheckBind(c.addr, c.tls, c.insecure); (err == nil) != c.ok {
			t.Errorf("CheckBind(%q, tls=%v, insecure=%v) = %v", c.addr, c.tls, c.insecure, err)
		}
	}
}
