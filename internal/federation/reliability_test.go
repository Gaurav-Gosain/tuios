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
