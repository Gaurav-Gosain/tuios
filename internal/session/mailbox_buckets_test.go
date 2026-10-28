package session

import (
	"strconv"
	"testing"
	"time"
)

// TestSendBucketsStayBounded covers the memory a linked machine can make the
// daemon hold by claiming a new sender name on every send.
func TestSendBucketsStayBounded(t *testing.T) {
	const bound = 256
	t0 := time.Unix(1_700_000_000, 0)
	fresh := func() *agentMailbox { return &agentMailbox{buckets: map[string]*sendBucket{}} }
	// send fails the test the moment the table holds more than the bound.
	send := func(t *testing.T, mb *agentMailbox, key string, now time.Time) bool {
		t.Helper()
		ok := mb.allow(key, now)
		if n := len(mb.buckets); n > bound {
			t.Fatalf("after a send from %q the mailbox holds %d buckets, want at most %d", key, n, bound)
		}
		return ok
	}
	spend := func(t *testing.T, mb *agentMailbox, key string, n int, now time.Time) {
		t.Helper()
		for i := range n {
			if !send(t, mb, key, now) {
				t.Fatalf("send %d from %q was refused inside its burst", i, key)
			}
		}
	}
	tokens := func(t *testing.T, mb *agentMailbox, key string) float64 {
		t.Helper()
		bk := mb.buckets[key]
		if bk == nil {
			t.Fatalf("no bucket for %q", key)
		}
		return bk.tokens
	}
	// fill sends new link keys until the table holds the bound, and fails
	// rather than spinning when it never gets there.
	fill := func(t *testing.T, mb *agentMailbox, prefix string, now time.Time) {
		t.Helper()
		for i := 0; len(mb.buckets) < bound; i++ {
			if i >= 2*bound {
				t.Fatalf("the table holds %d buckets after %d new link keys, want %d", len(mb.buckets), i, bound)
			}
			send(t, mb, "link:"+prefix+strconv.Itoa(i)+":n", now)
		}
	}

	t.Run("a link that claims a new name per send", func(t *testing.T) {
		mb := fresh()
		allowed := 0
		for i := range 200_000 {
			key := "link:h" + strconv.Itoa(i) + ":n" + strconv.Itoa(i)
			if send(t, mb, key, t0.Add(time.Duration(i)*time.Millisecond)) {
				allowed++
			}
		}
		t.Logf("200000 claimed link senders: %d buckets, %d sends allowed", len(mb.buckets), allowed)
	})

	t.Run("a link flood keeps a partly spent local bucket", func(t *testing.T) {
		mb := fresh()
		spend(t, mb, "w0", 5, t0)
		for i := range 300 {
			send(t, mb, "link:h"+strconv.Itoa(i)+":n"+strconv.Itoa(i), t0)
		}
		if got := tokens(t, mb, "w0"); got != 5 {
			t.Fatalf("w0 holds %v tokens after the flood, want 5", got)
		}
		if !send(t, mb, "w0", t0) {
			t.Fatal("w0's send after the flood was refused")
		}
		if got := tokens(t, mb, "w0"); got != 4 {
			t.Errorf("w0 holds %v tokens after its send, want 4", got)
		}
	})

	t.Run("past the bound new link names share one bucket", func(t *testing.T) {
		mb := fresh()
		fill(t, mb, "g", t0)
		for k := range 11 {
			ok := send(t, mb, "link:extra"+strconv.Itoa(k)+":n", t0)
			switch {
			case k == 0:
				if !ok {
					t.Fatal("the first link send past the bound was refused")
				}
				if got := tokens(t, mb, "link:*"); got != 9 {
					t.Fatalf("the shared link bucket holds %v tokens after one send, want 9", got)
				}
			case k < 10:
				if !ok {
					t.Fatalf("link send %d past the bound was refused, want allowed", k+1)
				}
			default:
				if ok {
					t.Error("the 11th link send past the bound was allowed, want refused")
				}
			}
		}
	})

	t.Run("a refilled flood makes room for a later link sender", func(t *testing.T) {
		mb := fresh()
		fill(t, mb, "f", t0)
		send(t, mb, "link:late:x", t0.Add(3*time.Second))
		if mb.buckets["link:late:x"] == nil {
			t.Error("link:late:x has no bucket of its own after the flood refilled")
		}
		if mb.buckets["link:*"] != nil {
			t.Error("the shared link bucket exists, want absent")
		}
	})
}

// TestALinkFloodSparesTheLocalBucket covers the local senders a link flood
// must not starve: the anonymous bucket and every window's own.
func TestALinkFloodSparesTheLocalBucket(t *testing.T) {
	const bound = 256
	t0 := time.Unix(1_700_000_000, 0)
	fresh := func() *agentMailbox { return &agentMailbox{buckets: map[string]*sendBucket{}} }
	// send fails the test the moment the table holds more than the bound.
	send := func(t *testing.T, mb *agentMailbox, key string, now time.Time) bool {
		t.Helper()
		ok := mb.allow(key, now)
		if n := len(mb.buckets); n > bound {
			t.Fatalf("after a send from %q the mailbox holds %d buckets, want at most %d", key, n, bound)
		}
		return ok
	}
	spend := func(t *testing.T, mb *agentMailbox, key string, n int, now time.Time) {
		t.Helper()
		for i := range n {
			if !send(t, mb, key, now) {
				t.Fatalf("send %d from %q was refused inside its burst", i, key)
			}
		}
	}
	tokens := func(t *testing.T, mb *agentMailbox, key string) float64 {
		t.Helper()
		bk := mb.buckets[key]
		if bk == nil {
			t.Fatalf("no bucket for %q", key)
		}
		return bk.tokens
	}
	// partlySpentLocals has "" spend 3 when anon is set, then w1..w255 spend
	// one each, all at t0, which fills the table with local buckets.
	partlySpentLocals := func(t *testing.T, mb *agentMailbox, anon bool) {
		t.Helper()
		if anon {
			spend(t, mb, "", 3, t0)
		}
		for j := 1; j <= 255; j++ {
			spend(t, mb, "w"+strconv.Itoa(j), 1, t0)
		}
	}
	wantLocals := func(t *testing.T, mb *agentMailbox) {
		t.Helper()
		for j := 1; j <= 255; j++ {
			if mb.buckets["w"+strconv.Itoa(j)] == nil {
				t.Errorf("w%d's bucket is gone", j)
			}
		}
	}

	t.Run("local senders after a link flood each get a send", func(t *testing.T) {
		mb := fresh()
		for i := range 1024 {
			send(t, mb, "link:h"+strconv.Itoa(i)+":n"+strconv.Itoa(i), t0.Add(time.Duration(i)*time.Millisecond))
		}
		end := t0.Add(1023 * time.Millisecond)
		for j := 1; j <= 300; j++ {
			if !send(t, mb, "w"+strconv.Itoa(j), end) {
				t.Errorf("w%d's first send was refused", j)
			}
		}
	})

	t.Run("a link flood does not charge the anonymous bucket", func(t *testing.T) {
		mb := fresh()
		spend(t, mb, "", 3, t0)
		for i := range 1024 {
			send(t, mb, "link:h"+strconv.Itoa(i)+":n"+strconv.Itoa(i), t0)
		}
		if got := tokens(t, mb, ""); got != 7 {
			t.Errorf("the anonymous bucket holds %v tokens after the flood, want 7", got)
		}
	})

	t.Run("a new local sender evicts a link bucket", func(t *testing.T) {
		mb := fresh()
		spend(t, mb, "", 3, t0)
		spend(t, mb, "w0", 5, t0)
		for i := range 254 {
			send(t, mb, "link:h"+strconv.Itoa(i)+":n"+strconv.Itoa(i), t0)
		}
		if n := len(mb.buckets); n != bound {
			t.Fatalf("the table holds %d buckets before w1, want %d", n, bound)
		}
		if !send(t, mb, "w1", t0) {
			t.Error("w1's send was refused")
		}
		if got := tokens(t, mb, "w0"); got != 5 {
			t.Errorf("w0 holds %v tokens, want 5", got)
		}
		if got := tokens(t, mb, ""); got != 7 {
			t.Errorf("the anonymous bucket holds %v tokens, want 7", got)
		}
	})

	t.Run("the anonymous bucket is never evicted", func(t *testing.T) {
		mb := fresh()
		partlySpentLocals(t, mb, true)
		for k := 1; k <= 2560; k++ {
			key := "x" + strconv.Itoa(k)
			if !send(t, mb, key, t0) {
				t.Fatalf("%s's send was refused", key)
			}
			if got := tokens(t, mb, ""); got != 7 {
				t.Fatalf("after %s the anonymous bucket holds %v tokens, want 7", key, got)
			}
			if n := len(mb.buckets); n != bound {
				t.Fatalf("after %s the table holds %d buckets, want %d", key, n, bound)
			}
		}
	})

	t.Run("a new local sender evicts the shared link bucket", func(t *testing.T) {
		mb := fresh()
		partlySpentLocals(t, mb, false)
		send(t, mb, "link:a:1", t0)
		send(t, mb, "link:b:2", t0)
		if mb.buckets["link:*"] == nil || mb.buckets["link:a:1"] != nil {
			t.Fatal("the shared link bucket is not the only link bucket before wN")
		}
		if !send(t, mb, "wN", t0) {
			t.Error("wN's send was refused")
		}
		if mb.buckets["link:*"] != nil {
			t.Error("the shared link bucket is still there, want evicted")
		}
		wantLocals(t, mb)
	})

	t.Run("a link send never evicts a local bucket", func(t *testing.T) {
		mb := fresh()
		partlySpentLocals(t, mb, true)
		if send(t, mb, "link:j:1", t0) {
			t.Error("link:j:1 was allowed into a table full of local buckets, want refused")
		}
		if mb.buckets["link:*"] != nil {
			t.Error("the shared link bucket exists, want absent")
		}
		wantLocals(t, mb)
		if got := tokens(t, mb, ""); got != 7 {
			t.Errorf("the anonymous bucket holds %v tokens, want 7", got)
		}
		if n := len(mb.buckets); n != bound {
			t.Errorf("the table holds %d buckets, want %d", n, bound)
		}
		if !send(t, mb, "wN", t0) {
			t.Error("wN's send was refused")
		}
		if n := len(mb.buckets); n != bound {
			t.Errorf("the table holds %d buckets after wN, want %d", n, bound)
		}
		if mb.buckets[""] == nil {
			t.Error("the anonymous bucket is gone after wN")
		}
	})
}
