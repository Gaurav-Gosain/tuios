package federation

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"
)

// What is proved here is the reliability of a link that has already worked:
// that an idle one is held open, that a dropped one says why it dropped and
// reads as reconnecting rather than as an offline machine, and that the things
// which are merely slow (a listing, a reader that fell behind) do not end it.
//
// The failure these exist for is a specific one. A session on a cloud host kept
// being taken away from the person using it, and the three causes below all
// presented as the same sentence: "the link closed".

// TestTwoListingsAtOnceDoNotReadEachOthersAnswers is a correctness bug the
// reliability work turned up.
//
// The control stream is one line of request and one of reply with nothing
// pairing them. Two clients polling the rail is enough to put two calls on it
// at once. What came back was not merely one host's answer under another
// host's name: both callers read the same bufio.Reader, and the daemon
// panicked with a slice bounds error inside bufio.ReadSlice, taking every
// session on that machine with it.
//
// NEGATIVE CONTROL, failed to fail: removing the lock this pins makes the test
// panic rather than report an assertion, because there is no way to run two
// calls on one reader that is merely wrong instead of fatal. The panic is the
// shipped behaviour at 3aebb8da and was reproduced there.
func TestTwoListingsAtOnceDoNotReadEachOthersAnswers(t *testing.T) {
	stub := startStubDaemon(t, func(verb string, params json.RawMessage) (any, *RemoteError) {
		if verb == "hello" {
			return Handshake{Protocol: 1, MinProtocol: 1, DaemonVersion: "1.0.0"}, nil
		}
		var p struct {
			Mark string `json:"mark"`
		}
		_ = json.Unmarshal(params, &p)
		// A little work, so two calls really do overlap.
		time.Sleep(5 * time.Millisecond)
		return map[string]any{"mark": p.Mark}, nil
	})
	m := managerFor(t, testOptions(proxyDialer(t, stub)), Host{Name: "build", Addr: "unused"})
	waitStatus(t, m, StatusUp)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	const callers = 8
	const rounds = 6
	var wg sync.WaitGroup
	bad := make(chan string, callers*rounds)
	for c := range callers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for r := range rounds {
				mark := "c" + string(rune('a'+c)) + "r" + string(rune('0'+r))
				raw, err := m.Call(ctx, "build", "echo", map[string]any{"mark": mark})
				if err != nil {
					bad <- "call failed: " + err.Error()
					return
				}
				var got struct {
					Mark string `json:"mark"`
				}
				if json.Unmarshal(raw, &got) != nil || got.Mark != mark {
					bad <- "asked for " + mark + " and was answered " + got.Mark
					return
				}
			}
		}()
	}
	wg.Wait()
	close(bad)
	if msg, ok := <-bad; ok {
		t.Errorf("ASSERTION: two calls on one control stream crossed answers: %s", msg)
	}
}

// TestAHostsOwnSSHOptionsBeatTheKeepaliveDefaults pins the placement.
//
// ssh takes the first value it obtains for a keyword, so an option this code
// wants to be a default has to come after the host's own. A person who set
// ServerAliveInterval themselves, because their network needs a different
// number, must keep it.
func TestAHostsOwnSSHOptionsBeatTheKeepaliveDefaults(t *testing.T) {
	h := Host{Name: "build", Addr: "me@buildbox", SSHOptions: []string{"-o", "ServerAliveInterval=5"}}
	args := linkArgs(h)
	mine, theirs := -1, -1
	for i, a := range args {
		switch a {
		case "ServerAliveInterval=5":
			theirs = i
		case "ServerAliveInterval=15":
			mine = i
		}
	}
	if theirs < 0 {
		t.Fatalf("the host's own option was dropped: %v", args)
	}
	if mine >= 0 && mine < theirs {
		t.Errorf("ASSERTION: the built-in keepalive at %d comes before the host's own at %d, so ssh takes the built-in and the user's setting is ignored: %v", mine, theirs, args)
	}
}

// TestAQuietLinkIsTornDownWhenAListingFails is the other side of keeping a link
// through a slow listing. A pipe that has carried nothing at all is not slow, it
// is gone, and the link must be redialed rather than reported up forever.
func TestAQuietLinkIsTornDownWhenAListingFails(t *testing.T) {
	hang := make(chan struct{})
	stub := startStubDaemon(t, func(verb string, _ json.RawMessage) (any, *RemoteError) {
		if verb == "hello" {
			return Handshake{Protocol: 1, MinProtocol: 1, DaemonVersion: "1.0.0"}, nil
		}
		<-hang
		return nil, &RemoteError{Code: "internal", Message: "test over"}
	})
	t.Cleanup(func() { close(hang) })
	opts := testOptions(proxyDialer(t, stub))
	opts.CallTimeout = 200 * time.Millisecond
	// The pipe is quiet the moment the handshake is done, so the first failed
	// listing is enough.
	opts.linkQuietLimit = time.Millisecond
	opts.InitialBackoff = 30 * time.Second
	opts.MaxBackoff = 30 * time.Second
	m := managerFor(t, opts, Host{Name: "build", Addr: "unused"})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if r := m.Reports(ctx)[0]; r.Status != StatusUp {
		t.Fatalf("status is %q, want up", r.Status)
	}
	// Give the handshake's own frames time to age past the quiet limit.
	time.Sleep(20 * time.Millisecond)
	if _, err := m.Call(ctx, "build", "list-sessions", nil); err == nil {
		t.Fatal("the listing answered; the timeout cannot be forced")
	}
	if r := m.Reports(ctx)[0]; r.Status == StatusUp {
		t.Error("ASSERTION: a link whose pipe has gone silent still reads as up after a failed listing; a dead host is never redialed")
	}
}

// TestAnAttachStreamSurvivesLongerThanAListingWould pins the two stall limits
// apart.
//
// The reader at the end of a relayed connection is a person's terminal, and a
// terminal stops reading for a while now and then: a big paint, a client
// swapped out, a lid closed and opened. Ten seconds of that used to end the
// stream, which ended the session. The control stream keeps the short limit,
// because a listing that is not being read after ten seconds has nobody behind
// it at all.
func TestAnAttachStreamSurvivesLongerThanAListingWould(t *testing.T) {
	if connectionStallLimit <= defaultStallLimit {
		t.Errorf("ASSERTION: a relayed connection is dropped as fast as a listing (%v vs %v), so a client that paused for a moment loses its session",
			connectionStallLimit, defaultStallLimit)
	}
	stub := startStubDaemon(t, helloOK("1.2.3", 0))
	opts := testOptions(proxyDialer(t, stub))
	opts.stallLimit = 20 * time.Millisecond
	opts.connStallLimit = 5 * time.Second
	m := managerFor(t, opts, Host{Name: "build", Addr: "unused"})
	waitForStatus(t, m, "build", StatusUp)

	conn := openTo(t, m, "build")
	s, ok := conn.(*Stream)
	if !ok {
		t.Fatalf("a connection is %T, not a stream", conn)
	}
	if s.stall != opts.connStallLimit {
		t.Errorf("ASSERTION: a relayed connection carries the %v limit meant for a listing, not the %v meant for a session", s.stall, opts.connStallLimit)
	}
}
