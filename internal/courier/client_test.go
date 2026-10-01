package courier_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/courier"
	"github.com/Gaurav-Gosain/tuios/internal/courier/relay"
)

// person is one side of a conversation: keys, config, store and client.
type person struct {
	name   string
	keys   *courier.Keys
	cfg    *courier.Config
	store  *courier.Store
	client *courier.Client
}

type world struct {
	t      *testing.T
	relay  *relay.Server
	srv    *httptest.Server
	people map[string]*person
	// boxes records every box the relay was handed, to check it never saw
	// plaintext.
	boxes [][]byte
}

func newWorld(t *testing.T, names ...string) *world {
	t.Helper()
	w := &world{t: t, people: map[string]*person{}}
	var roster strings.Builder
	for _, n := range names {
		k, err := courier.GenerateKeys()
		if err != nil {
			t.Fatal(err)
		}
		w.people[n] = &person{name: n, keys: k}
		fmt.Fprintf(&roster, "%s %s\n", n, k.Identity())
	}
	rosterPath := filepath.Join(t.TempDir(), "roster")
	os.WriteFile(rosterPath, []byte(roster.String()), 0o600)
	rs, err := relay.New(relay.Options{Roster: rosterPath, MaxWait: 3 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	w.relay = rs
	inner := rs.Handler()
	w.srv = httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && strings.HasPrefix(r.URL.Path, "/v1/mail/") {
			body, _ := io.ReadAll(r.Body)
			w.boxes = append(w.boxes, body)
			r.Body = readCloser{strings.NewReader(string(body))}
		}
		inner.ServeHTTP(rw, r)
	}))
	t.Cleanup(w.srv.Close)
	for _, p := range w.people {
		p.cfg = &courier.Config{Name: p.name, Relay: w.srv.URL + "/", ReplyRelease: courier.ReleaseAuto, Peers: map[string]courier.Peer{}}
		st, err := courier.OpenStore(t.TempDir(), nil)
		if err != nil {
			t.Fatal(err)
		}
		p.store = st
		c, err := courier.NewClient(p.cfg, p.keys, st, courier.ClientOptions{})
		if err != nil {
			t.Fatal(err)
		}
		p.client = c
	}
	return w
}

type readCloser struct{ *strings.Reader }

func (readCloser) Close() error { return nil }

// introduce makes a and b peers of each other with the given release policy
// on b's side for a.
func (w *world) introduce(a, b, release string) {
	pa, pb := w.people[a], w.people[b]
	if err := pa.cfg.AddPeer(b, pb.keys.Identity(), courier.ReleaseHold); err != nil {
		w.t.Fatal(err)
	}
	if err := pb.cfg.AddPeer(a, pa.keys.Identity(), release); err != nil {
		w.t.Fatal(err)
	}
}

func ctx(t *testing.T) context.Context {
	c, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	t.Cleanup(cancel)
	return c
}

func bodies(es []courier.Entry) []string {
	var out []string
	for _, e := range es {
		out = append(out, e.Msg.Body)
	}
	return out
}

func TestClientConversation(t *testing.T) {
	w := newWorld(t, "ghaith", "gg")
	w.introduce("ghaith", "gg", courier.ReleaseHold)
	g, gg := w.people["ghaith"], w.people["gg"]

	q, err := g.client.Send(ctx(t), courier.Outgoing{To: "gg", Agent: "backend", FromAgent: "frontend", Subject: "orders", Body: "is POST /orders returning totals yet?"})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	for _, box := range w.boxes {
		if strings.Contains(string(box), "POST /orders") || strings.Contains(string(box), "frontend") {
			t.Fatal("the relay was handed plaintext")
		}
	}

	res, err := gg.client.Sync(ctx(t), 0)
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if res.New != 1 || res.Held != 1 || len(res.Rejected) != 0 {
		t.Fatalf("sync result: %+v", res)
	}
	if got := gg.store.Deliverable(courier.Filter{Agent: "backend"}); len(got) != 0 {
		t.Fatal("new mail from a hold peer is deliverable before release")
	}
	// The relay copy is gone: a second sync finds nothing new.
	if res, _ := gg.client.Sync(ctx(t), 0); res.New != 0 {
		t.Fatalf("mail was not acked: %+v", res)
	}
	held := gg.store.List()
	if len(held) != 1 || held[0].Peer != "ghaith" || held[0].Msg.FromAgent != "frontend" {
		t.Fatalf("held: %+v", held)
	}
	gg.store.Release(held[0].Key)
	if got := bodies(gg.store.Deliverable(courier.Filter{Agent: "backend"})); len(got) != 1 {
		t.Fatalf("released mail not deliverable: %v", got)
	}

	reply, err := gg.client.Reply(ctx(t), held[0].Key, courier.Outgoing{Body: "yes: total_cents", FromAgent: "backend"})
	if err != nil {
		t.Fatalf("Reply: %v", err)
	}
	if reply.Thread != q.Thread || reply.ReplyTo != q.ID || reply.Agent != "frontend" {
		t.Fatalf("reply %+v does not answer %+v", reply, q)
	}

	// The reply is in a thread ghaith sent into, from the peer it went to, so
	// it is released at once (reply_release = auto).
	got, err := g.client.Wait(ctx(t), courier.Filter{Agent: "frontend", Thread: q.Thread}, 5*time.Second)
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if len(got) != 1 || got[0].Msg.Body != "yes: total_cents" || got[0].Peer != "gg" {
		t.Fatalf("Wait returned %+v", got)
	}
}

func TestClientReplyReleaseNeedsTheRightPeer(t *testing.T) {
	w := newWorld(t, "ghaith", "gg", "zain")
	w.introduce("ghaith", "gg", courier.ReleaseHold)
	w.introduce("ghaith", "zain", courier.ReleaseHold)
	g, zain := w.people["ghaith"], w.people["zain"]
	q, _ := g.client.Send(ctx(t), courier.Outgoing{To: "gg", Body: "question for gg"})

	// zain learned the thread id and answers into it. Ghaith never sent into
	// that thread to zain, so it is held like any new mail from zain.
	if _, err := zain.client.Send(ctx(t), courier.Outgoing{To: "ghaith", Thread: q.Thread, ReplyTo: q.ID, Body: "I'm answering for gg"}); err != nil {
		t.Fatal(err)
	}
	res, _ := g.client.Sync(ctx(t), 0)
	if res.Held != 1 || len(g.store.Deliverable(courier.Filter{})) != 0 {
		t.Fatalf("a reply from the wrong peer was released: %+v", res)
	}
}

func TestClientAutoReleasePeer(t *testing.T) {
	w := newWorld(t, "ghaith", "gg")
	w.introduce("ghaith", "gg", courier.ReleaseAuto)
	w.people["ghaith"].client.Send(ctx(t), courier.Outgoing{To: "gg", Body: "trusted"})
	res, _ := w.people["gg"].client.Sync(ctx(t), 0)
	if res.Held != 0 || len(w.people["gg"].store.Deliverable(courier.Filter{})) != 1 {
		t.Fatalf("auto peer mail was held: %+v", res)
	}
}

func TestClientUnknownSenderIsKeptUntilAdded(t *testing.T) {
	w := newWorld(t, "ghaith", "gg")
	g, gg := w.people["ghaith"], w.people["gg"]
	// Ghaith adds gg and writes before gg has added ghaith.
	g.cfg.AddPeer("gg", gg.keys.Identity(), courier.ReleaseHold)
	if _, err := g.client.Send(ctx(t), courier.Outgoing{To: "gg", Body: "early"}); err != nil {
		t.Fatal(err)
	}
	res, err := gg.client.Sync(ctx(t), 0)
	if err != nil {
		t.Fatal(err)
	}
	if res.New != 0 || res.Pending != 1 {
		t.Fatalf("mail from an unknown sender: %+v", res)
	}
	gg.cfg.AddPeer("ghaith", g.keys.Identity(), courier.ReleaseAuto)
	res, _ = gg.client.Sync(ctx(t), 0)
	if res.New != 1 || res.Pending != 0 {
		t.Fatalf("after peers add: %+v", res)
	}
	if got := bodies(gg.store.Deliverable(courier.Filter{})); len(got) != 1 || got[0] != "early" {
		t.Fatalf("the kept mail did not arrive: %v", got)
	}
}

func TestClientRejectsMailFromOutsideTheRoster(t *testing.T) {
	w := newWorld(t, "ghaith", "gg")
	w.introduce("ghaith", "gg", courier.ReleaseAuto)
	// A sender the relay does not serve is refused at the relay.
	outsider, _ := courier.GenerateKeys()
	cfg := &courier.Config{Name: "x", Relay: w.srv.URL + "/", ReplyRelease: courier.ReleaseAuto, Peers: map[string]courier.Peer{}}
	cfg.AddPeer("gg", w.people["gg"].keys.Identity(), courier.ReleaseHold)
	st, _ := courier.OpenStore(t.TempDir(), nil)
	c, _ := courier.NewClient(cfg, outsider, st, courier.ClientOptions{})
	_, err := c.Send(ctx(t), courier.Outgoing{To: "gg", Body: "let me in"})
	var re *courier.RelayError
	if !errors.As(err, &re) || re.Code != "unauthorized" {
		t.Fatalf("send from outside the roster: %v", err)
	}
}

func TestClientWaitTimesOut(t *testing.T) {
	w := newWorld(t, "ghaith", "gg")
	w.introduce("ghaith", "gg", courier.ReleaseAuto)
	start := time.Now()
	_, err := w.people["gg"].client.Wait(ctx(t), courier.Filter{}, 1500*time.Millisecond)
	if !errors.Is(err, courier.ErrWaitTimeout) {
		t.Fatalf("Wait on nothing: %v", err)
	}
	if d := time.Since(start); d < time.Second || d > 6*time.Second {
		t.Fatalf("a 1.5 s wait took %v", d)
	}
}

func TestClientSendToUnknownPeer(t *testing.T) {
	w := newWorld(t, "ghaith", "gg")
	if _, err := w.people["ghaith"].client.Send(ctx(t), courier.Outgoing{To: "nobody", Body: "x"}); err == nil || !strings.Contains(err.Error(), "peers add") {
		t.Fatalf("send to a name not in peers: %v", err)
	}
}

func TestClientRelayDown(t *testing.T) {
	w := newWorld(t, "ghaith", "gg")
	w.introduce("ghaith", "gg", courier.ReleaseAuto)
	w.srv.Close()
	c, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	start := time.Now()
	if _, err := w.people["gg"].client.Sync(c, 0); err == nil {
		t.Fatal("Sync against a closed relay succeeded")
	}
	if time.Since(start) > 3500*time.Millisecond {
		t.Fatal("Sync ignored its context deadline")
	}
}

func TestClientUsesProxyFromEnvironment(t *testing.T) {
	if !courier.UsesEnvironmentProxy(courier.DefaultHTTPClient()) {
		t.Fatal("the default client does not honour HTTPS_PROXY")
	}
}
