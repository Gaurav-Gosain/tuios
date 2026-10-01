package courier

import (
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func newTestStore(t *testing.T) (*Store, *fakeClock) {
	t.Helper()
	clock := &fakeClock{t: time.Now().UTC().Truncate(time.Second)}
	s, err := OpenStore(t.TempDir(), clock.Now)
	if err != nil {
		t.Fatal(err)
	}
	return s, clock
}

func testRecord(t *testing.T, from *Keys, agent, body string, sentAt time.Time) Record {
	t.Helper()
	m := testMessage(body)
	m.Agent = agent
	m.SentAt = sentAt
	return Record{Msg: m, From: from.Identity().String(), Peer: "gg", RelayID: NewID(), ReceivedAt: sentAt}
}

func TestStorePutIsIdempotent(t *testing.T) {
	s, clock := newTestStore(t)
	gg := mustKeys(t)
	rec := testRecord(t, gg, "", "hi", clock.Now())
	added, err := s.Put(rec)
	if err != nil || !added {
		t.Fatalf("first Put: %v %v", added, err)
	}
	rec.Msg.Body = "a replay with other words"
	added, err = s.Put(rec)
	if err != nil || added {
		t.Fatalf("second Put of the same id: added=%v err=%v", added, err)
	}
	e, err := s.Get(rec.Key())
	if err != nil || e.Msg.Body != "hi" {
		t.Fatalf("the first copy did not win: %q %v", e.Msg.Body, err)
	}
	// The same message id from another sender is another message.
	other := rec
	other.From = mustKeys(t).Identity().String()
	if added, _ := s.Put(other); !added {
		t.Fatal("a different sender's message with the same id was taken for a duplicate")
	}
}

func TestStoreHeldReleasedRead(t *testing.T) {
	s, clock := newTestStore(t)
	rec := testRecord(t, mustKeys(t), "", "hi", clock.Now())
	s.Put(rec)
	if got := s.Deliverable(Filter{}); len(got) != 0 {
		t.Fatal("held mail is deliverable")
	}
	if got := s.List(); len(got) != 1 || got[0].Released || got[0].Read {
		t.Fatalf("List: %+v", got)
	}
	if err := s.Release(rec.Key()); err != nil {
		t.Fatal(err)
	}
	if err := s.Release(rec.Key()); err != nil {
		t.Fatalf("a second release: %v", err)
	}
	got := s.Deliverable(Filter{})
	if len(got) != 1 {
		t.Fatal("released mail is not deliverable")
	}
	var out []string
	n, err := s.Deliver(Filter{}, func(es []Entry) error {
		for _, e := range es {
			out = append(out, e.Msg.Body)
		}
		return nil
	})
	if err != nil || n != 1 || len(out) != 1 {
		t.Fatalf("Deliver: %d %v %v", n, out, err)
	}
	if got := s.Deliverable(Filter{}); len(got) != 0 {
		t.Fatal("read mail is deliverable again")
	}
	if e, _ := s.Get(rec.Key()); !e.Read {
		t.Fatal("delivered mail is not marked read")
	}
}

func TestStoreDeliverFailureKeepsMail(t *testing.T) {
	s, clock := newTestStore(t)
	rec := testRecord(t, mustKeys(t), "", "hi", clock.Now())
	s.Put(rec)
	s.Release(rec.Key())
	if _, err := s.Deliver(Filter{}, func([]Entry) error { return os.ErrClosed }); err == nil {
		t.Fatal("Deliver hid the write error")
	}
	if got := s.Deliverable(Filter{}); len(got) != 1 {
		t.Fatal("mail whose output failed was lost")
	}
}

func TestStoreStaleClaimIsRetaken(t *testing.T) {
	s, clock := newTestStore(t)
	rec := testRecord(t, mustKeys(t), "", "hi", clock.Now())
	s.Put(rec)
	s.Release(rec.Key())
	// A reader took the claim and died before marking it read.
	if !s.claim(rec.Key()) {
		t.Fatal("claim failed")
	}
	if got := s.Deliverable(Filter{}); len(got) != 0 {
		t.Fatal("mail under a fresh claim is deliverable")
	}
	clock.Advance(claimLease + time.Second)
	if got := s.Deliverable(Filter{}); len(got) != 1 {
		t.Fatal("mail under a stale claim is lost")
	}
	if n, _ := s.Deliver(Filter{}, func([]Entry) error { return nil }); n != 1 {
		t.Fatal("a stale claim could not be retaken")
	}
}

func TestStoreDeliversEachMessageOnce(t *testing.T) {
	s, clock := newTestStore(t)
	gg := mustKeys(t)
	const msgs, readers = 50, 16
	for range msgs {
		rec := testRecord(t, gg, "", "hi", clock.Now())
		s.Put(rec)
		s.Release(rec.Key())
	}
	var mu sync.Mutex
	seen := map[string]int{}
	var total atomic.Int64
	var wg sync.WaitGroup
	for range readers {
		wg.Go(func() {
			for {
				n, err := s.Deliver(Filter{}, func(es []Entry) error {
					mu.Lock()
					for _, e := range es {
						seen[e.Key]++
					}
					mu.Unlock()
					return nil
				})
				if err != nil {
					t.Error(err)
					return
				}
				total.Add(int64(n))
				if n == 0 {
					return
				}
			}
		})
	}
	wg.Wait()
	if len(seen) != msgs || total.Load() != msgs {
		t.Fatalf("delivered %d distinct, %d total, want %d", len(seen), total.Load(), msgs)
	}
	for k, n := range seen {
		if n != 1 {
			t.Fatalf("message %s delivered %d times", k, n)
		}
	}
}

func TestStoreFilters(t *testing.T) {
	s, clock := newTestStore(t)
	gg := mustKeys(t)
	backend := testRecord(t, gg, "backend", "for backend", clock.Now())
	anyAgent := testRecord(t, gg, "", "for anyone", clock.Now())
	frontend := testRecord(t, gg, "frontend", "for frontend", clock.Now())
	for _, r := range []Record{backend, anyAgent, frontend} {
		s.Put(r)
		s.Release(r.Key())
	}
	bodies := func(f Filter) map[string]bool {
		out := map[string]bool{}
		for _, e := range s.Deliverable(f) {
			out[e.Msg.Body] = true
		}
		return out
	}
	if got := bodies(Filter{Agent: "backend"}); len(got) != 2 || !got["for backend"] || !got["for anyone"] {
		t.Fatalf("agent backend sees %v", got)
	}
	if got := bodies(Filter{}); len(got) != 3 {
		t.Fatalf("an unlabeled reader sees %v", got)
	}
	if got := bodies(Filter{Thread: frontend.Msg.Thread}); len(got) != 1 || !got["for frontend"] {
		t.Fatalf("thread filter sees %v", got)
	}
}

func TestStoreDropLeavesTombstone(t *testing.T) {
	s, clock := newTestStore(t)
	rec := testRecord(t, mustKeys(t), "", "hi", clock.Now())
	s.Put(rec)
	s.Release(rec.Key())
	if err := s.Drop(rec.Key()); err != nil {
		t.Fatal(err)
	}
	if got := s.Deliverable(Filter{}); len(got) != 0 {
		t.Fatal("dropped mail is deliverable")
	}
	if got := s.List(); len(got) != 0 {
		t.Fatal("dropped mail is listed")
	}
	// A relay that hands the same message over again does not bring it back.
	if added, _ := s.Put(rec); added {
		t.Fatal("a replay of dropped mail was stored again")
	}
}

func TestStoreGC(t *testing.T) {
	s, clock := newTestStore(t)
	gg := mustKeys(t)
	old := testRecord(t, gg, "", "old", clock.Now())
	s.Put(old)
	s.RecordSent(SentRecord{Msg: testMessage("q"), To: gg.Identity().String(), Peer: "gg"})
	clock.Advance(MaxMessageAge + time.Hour)
	fresh := testRecord(t, gg, "", "fresh", clock.Now())
	s.Put(fresh)
	s.GC()
	if _, err := s.Get(old.Key()); err == nil {
		t.Fatal("GC kept a message past MaxMessageAge")
	}
	if _, err := s.Get(fresh.Key()); err != nil {
		t.Fatal("GC removed a fresh message")
	}
	entries, _ := os.ReadDir(filepath.Join(s.dir, "inbox"))
	for _, e := range entries {
		if filepath.Ext(e.Name()) != ".json" && !hasKeyPrefix(e.Name(), fresh.Key()) {
			t.Fatalf("GC left %s behind", e.Name())
		}
	}
}

func hasKeyPrefix(name, key string) bool { return len(name) >= len(key) && name[:len(key)] == key }

func TestStoreSentThreads(t *testing.T) {
	s, _ := newTestStore(t)
	gg, zain := mustKeys(t), mustKeys(t)
	q := testMessage("question")
	if err := s.RecordSent(SentRecord{Msg: q, To: gg.Identity().String(), Peer: "gg"}); err != nil {
		t.Fatal(err)
	}
	if !s.SentInto(q.Thread, gg.Identity()) {
		t.Fatal("a thread I sent into, from the peer I sent to, is not mine")
	}
	if s.SentInto(q.Thread, zain.Identity()) {
		t.Fatal("a thread I sent to gg counts as one I sent to zain")
	}
	if s.SentInto(NewID(), gg.Identity()) {
		t.Fatal("a thread I never sent into counts")
	}
}

func TestStoreFind(t *testing.T) {
	s, clock := newTestStore(t)
	rec := testRecord(t, mustKeys(t), "", "hi", clock.Now())
	s.Put(rec)
	for _, ref := range []string{rec.Msg.ID, rec.Msg.ID[:8], rec.Key()} {
		e, err := s.Find(ref)
		if err != nil || e.Key != rec.Key() {
			t.Fatalf("Find(%q): %v", ref, err)
		}
	}
	for _, ref := range []string{"", "abc", "zzzzzzzz", "../../etc"} {
		if _, err := s.Find(ref); err == nil {
			t.Fatalf("Find(%q) found something", ref)
		}
	}
}

func TestStorePending(t *testing.T) {
	s, clock := newTestStore(t)
	gg := mustKeys(t)
	for range maxPending {
		if err := s.SavePending(NewID(), gg.Identity(), []byte("box"), clock.Now()); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.SavePending(NewID(), gg.Identity(), []byte("box"), clock.Now()); err == nil {
		t.Fatal("pending is unbounded")
	}
	p := s.Pending()
	if len(p) != maxPending {
		t.Fatalf("%d pending, want %d", len(p), maxPending)
	}
	s.RemovePending(p[0].RelayID)
	if len(s.Pending()) != maxPending-1 {
		t.Fatal("RemovePending did not remove it")
	}
	clock.Advance(MaxRelayTTL + time.Hour)
	s.GC()
	if len(s.Pending()) != 0 {
		t.Fatal("GC kept pending boxes past the relay TTL")
	}
}
