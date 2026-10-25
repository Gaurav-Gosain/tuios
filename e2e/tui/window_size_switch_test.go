package tuie2e

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// The latest policy across a session switch: a person at one client leaves
// the session for another and comes back, while a second client stays on the
// first session and nobody touches it.

const wsOther = "other"

// switchClient moves the client running in term to the session, through
// switch-session as the tower and the session switcher do, and waits for the
// daemon to list it there.
func switchClient(t *testing.T, base string, term *tuitest.Terminal, session string) {
	t.Helper()
	id := ""
	deadline := time.Now().Add(uiTimeout)
	for time.Now().Before(deadline) {
		out, err := tuiosCLI(t, base, "list-clients", "--json")
		if err != nil {
			t.Fatalf("list-clients: %v\n%s", err, out)
		}
		var clients []struct {
			ID      string `json:"client_id"`
			PID     int    `json:"pid"`
			Session string `json:"session"`
		}
		if err := json.Unmarshal([]byte(out), &clients); err != nil {
			t.Fatalf("list-clients --json printed %q: %v", out, err)
		}
		for _, c := range clients {
			if c.PID != term.Pid() || c.Session == "" {
				continue
			}
			if id == "" {
				id = c.ID
				if c.Session == session {
					return
				}
				if out, err := tuiosCLI(t, base, "switch-session", "--client", id, session); err != nil {
					t.Fatalf("switch-session: %v\n%s", err, out)
				}
			} else if c.ID == id && c.Session == session {
				// The switch re-arms input handling, and the first keys after
				// it are dropped without the wait.
				time.Sleep(insertGuard + 150*time.Millisecond)
				return
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("the client (pid %d) never showed %s", term.Pid(), session)
}

// wsSizes samples the size the daemon settles ws on until stop closes, and
// sends every size it saw, sorted.
func wsSizes(t *testing.T, base string, stop <-chan struct{}) <-chan string {
	sizes := make(chan string, 1)
	go func() {
		seen := map[string]bool{}
		for {
			select {
			case <-stop:
				list := make([]string, 0, len(seen))
				for s := range seen {
					list = append(list, s)
				}
				slices.Sort(list)
				sizes <- strings.Join(list, " ")
				return
			default:
			}
			if out, err := tuiosCLI(t, base, "session-info", "-s", wsSession, "--json"); err == nil {
				var res struct {
					W int `json:"session_width"`
					H int `json:"session_height"`
				}
				if json.Unmarshal([]byte(out), &res) == nil {
					seen[fmt.Sprintf("%dx%d", res.W, res.H)] = true
				}
			}
			time.Sleep(50 * time.Millisecond)
		}
	}()
	return sizes
}

// TestWindowSizeLatestRoundTripKeepsSize is the report: the big client types
// in ws, switches to another session and back. The small client stayed on ws
// with no input, so ws must hold the big client's size the whole way, rather
// than shrink to the small client while the big one is away and be drawn at
// that size on its return until its next key.
//
// NEGATIVE CONTROL: with pickLatest handing ws to the small client when the
// big one leaves, the sampler sees 80x24 and the test fails.
func TestWindowSizeLatestRoundTripKeepsSize(t *testing.T) {
	big, small, base := windowSizePair(t, "latest", "", nil)
	if out, err := tuiosCLI(t, base, "new", wsOther, "--detach"); err != nil {
		t.Fatalf("create session %s: %v: %s", wsOther, err, out)
	}
	waitWSSize(t, base, wsBigCols, wsBigRows, "with the big client latest")
	activity(t, big)
	time.Sleep(wsHoldGap)

	stop := make(chan struct{})
	sizes := wsSizes(t, base, stop)
	switchClient(t, base, big, wsOther)
	// Past the hold, so a hand-over to the small client would have landed.
	time.Sleep(2 * wsHoldGap)
	switchClient(t, base, big, wsSession)
	time.Sleep(wsHoldGap)
	close(stop)
	if got, want := <-sizes, fmt.Sprintf("%dx%d", wsBigCols, wsBigRows); got != want {
		t.Fatalf("over the round trip ws was %s, want only %s", got, want)
	}

	// The small client takes it with its first key, as before.
	activity(t, small)
	waitWSSize(t, base, wsSmallCols, wsSmallRows, "after input in the small client")
}

// TestWindowSizeLatestReturnKeepsItsInput: the small client typed, then the
// big client typed after it and took ws. The big client leaves for another
// session and comes back. It is the client ws kept its size for, so it comes
// back with its input, still newer than the small client's, and ws stays its
// size the whole way.
//
// NEGATIVE CONTROL: with handleAttach clearing the input of every client that
// attaches, the big client comes back as one that never typed, the small
// client's older input wins once the hold is over, and the sampler sees 80x24.
func TestWindowSizeLatestReturnKeepsItsInput(t *testing.T) {
	big, small, base := windowSizePair(t, "latest", "", nil)
	if out, err := tuiosCLI(t, base, "new", wsOther, "--detach"); err != nil {
		t.Fatalf("create session %s: %v: %s", wsOther, err, out)
	}
	waitWSSize(t, base, wsBigCols, wsBigRows, "with the big client latest")
	time.Sleep(wsHoldGap)
	activity(t, small)
	waitWSSize(t, base, wsSmallCols, wsSmallRows, "after input in the small client")
	time.Sleep(wsHoldGap)
	activity(t, big)
	waitWSSize(t, base, wsBigCols, wsBigRows, "after input in the big client")
	time.Sleep(wsHoldGap)

	stop := make(chan struct{})
	sizes := wsSizes(t, base, stop)
	switchClient(t, base, big, wsOther)
	time.Sleep(wsHoldGap)
	switchClient(t, base, big, wsSession)
	// Past the hold, so a hand-over to the small client would have landed.
	time.Sleep(2 * wsHoldGap)
	close(stop)
	if got, want := <-sizes, fmt.Sprintf("%dx%d", wsBigCols, wsBigRows); got != want {
		t.Fatalf("over the round trip ws was %s, want only %s", got, want)
	}
	waitMark(t, small, true, "the small client, with the big one back")
}

// TestWindowSizeLatestLeftAloneNewClientTakesItsSize: the big client types in
// ws and leaves it, by a switch or for good, and nobody else is attached. A
// small terminal that attaches to ws on its own takes its own size at once,
// as it does when nobody typed: ws keeps a size only for the clients that
// were there when its latest client left.
//
// NEGATIVE CONTROL: with pickLatest keeping the size whoever is attached
// (the anyAttachedBy check removed), ws stays 200x50 under the small client
// until it types, and waitWSSize fails.
func TestWindowSizeLatestLeftAloneNewClientTakesItsSize(t *testing.T) {
	for _, tc := range []struct {
		name  string
		leave func(t *testing.T, base string, big *tuitest.Terminal)
	}{
		{"switch", func(t *testing.T, base string, big *tuitest.Terminal) {
			switchClient(t, base, big, wsOther)
		}},
		{"close", func(t *testing.T, base string, big *tuitest.Terminal) {
			if err := big.Close(); err != nil {
				t.Logf("close the big client: %v", err)
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			base := t.TempDir()
			writeConfig(t, base, "[daemon]\nwindow_size = \"latest\"\n")
			killDaemon(t, base)
			for _, name := range []string{wsSession, wsOther} {
				if out, err := tuiosCLI(t, base, "new", name, "--detach"); err != nil {
					t.Fatalf("create session %s: %v: %s", name, err, out)
				}
			}
			big := attachIn(t, base, wsSession, startOpts{cols: wsBigCols, rows: wsBigRows})
			waitWSSize(t, base, wsBigCols, wsBigRows, "with the big client alone")
			activity(t, big)
			time.Sleep(wsHoldGap)

			tc.leave(t, base, big)
			waitNoClientOn(t, base, wsSession)
			small := attachSmall(t, base, wsSession, startOpts{cols: wsSmallCols, rows: wsSmallRows})
			waitWSSize(t, base, wsSmallCols, wsSmallRows, "with a small client attached alone, no key pressed")
			waitMark(t, small, false, "the small client, alone in ws")
		})
	}
}
