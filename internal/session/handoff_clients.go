package session

import (
	"encoding/json"
	"errors"
	"sync"
	"time"
)

// What a live upgrade tells the clients it is about to drop.
//
// A TUI client is sent MsgDaemonUpgrading, so it can say the daemon was
// upgraded and its programs are still running rather than that the daemon was
// lost. A JSON verb call still running or waiting (wait-for, a popup that
// waits, ask-agent) is answered with daemon_upgrading, which says to call
// again. Every connection is then closed, as shutdown closes them; callers
// that dial meanwhile wait in the backlog for the new image.

// handoffNoticeWait bounds how long the notices may take before the
// connections are closed anyway: a client that does not read is not waited
// for.
const handoffNoticeWait = 500 * time.Millisecond

// errVerbAnswered ends a verb connection whose call a live upgrade answered.
var errVerbAnswered = errors.New("the call was answered by a live upgrade")

// inflightVerb is the JSON verb call a connection is running.
type inflightVerb struct {
	mu       sync.Mutex
	running  bool
	answered bool
	id       json.RawMessage
	verb     string
}

func (v *inflightVerb) begin(id json.RawMessage, verb string) {
	v.mu.Lock()
	v.running, v.answered, v.id, v.verb = true, false, id, verb
	v.mu.Unlock()
}

// end reports whether the call is still the handler's to answer.
func (v *inflightVerb) end() bool {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.running = false
	return !v.answered
}

// take claims a running call for an answer of the caller's.
func (v *inflightVerb) take() (json.RawMessage, string, bool) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if !v.running || v.answered {
		return nil, "", false
	}
	v.answered = true
	return v.id, v.verb, true
}

// tuiClientCount is how many TUI clients are attached.
func (d *Daemon) tuiClientCount() int {
	d.clientsMu.RLock()
	defer d.clientsMu.RUnlock()
	n := 0
	for _, cs := range d.clients {
		cs.mu.Lock()
		if cs.isTUIClient && cs.attached {
			n++
		}
		cs.mu.Unlock()
	}
	return n
}

// closeClientsForHandoff tells every client what is happening, as far as it
// listens within handoffNoticeWait, and drops it.
func (d *Daemon) closeClientsForHandoff(version string) {
	d.clientsMu.Lock()
	clients := make([]*connState, 0, len(d.clients))
	for _, cs := range d.clients {
		clients = append(clients, cs)
	}
	d.clients = make(map[string]*connState)
	d.clientsMu.Unlock()

	var wg sync.WaitGroup
	for _, cs := range clients {
		cs.mu.Lock()
		tui := cs.isTUIClient
		cs.mu.Unlock()
		wg.Go(func() {
			if tui {
				_ = d.sendMessage(cs, MsgDaemonUpgrading, &DaemonUpgradingPayload{Version: version})
			}
			if id, verb, ok := cs.verbCall.take(); ok {
				_ = d.writeVerbError(cs, id, verb, hintedVerbError(ErrVerbDaemonUpgrading,
					"the daemon is being upgraded live, and this call ended with the image that was running it", &VerbHint{
						Detail: "Every pane keeps running. Call again: the new image answers it.",
					}))
			}
		})
	}
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(handoffNoticeWait):
	}
	for _, cs := range clients {
		cs.closeDone()
		_ = cs.conn.Close()
	}
}
