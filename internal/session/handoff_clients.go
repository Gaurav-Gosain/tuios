package session

import (
	"encoding/json"
	"errors"
	"sync"
	"time"
)

const handoffNoticeWait = 500 * time.Millisecond

var errVerbAnswered = errors.New("the call was answered by a live upgrade")

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

func (v *inflightVerb) end() bool {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.running = false
	return !v.answered
}

func (v *inflightVerb) take() (json.RawMessage, string, bool) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if !v.running || v.answered {
		return nil, "", false
	}
	v.answered = true
	return v.id, v.verb, true
}

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

func (d *Daemon) closeClientsForHandoff(version, binary string) {
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
				_ = d.sendMessage(cs, MsgDaemonUpgrading, &DaemonUpgradingPayload{Version: version, Binary: binary})
			}
			if id, verb, ok := cs.verbCall.take(); ok {
				_ = d.writeVerbError(cs, id, verb, hintedVerbError(ErrVerbDaemonUpgrading,
					"the daemon is being upgraded live, and this call ended with the image that was running it", &VerbHint{
						Detail: "Every pane keeps running. A call that reads or waits can be made again: the new image answers it. A call that changes something may have partly run, so check what it did before calling again.",
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
