package session

import (
	"os"
	"sync"
	"time"
)

// The daemon's last word to its clients.
//
// A client that loses its daemon cannot tell a restart from a stop by the
// closed socket alone. Both look the same, and so does a crash. The client
// waits for a new daemon after a restart and after a crash, and must not wait
// after a stop: a person who ran kill-server wants the clients gone.
//
// So the daemon says why before it closes: "restart" when the stop is the
// first half of a restart, "stop" otherwise. A crash says nothing, and the
// client treats silence as a reason to wait. tuios kill-server --restart, which
// restart-server runs, leaves a mark beside the socket that the daemon reads
// on its way out.

// Goodbye reasons.
const (
	GoodbyeStop    = "stop"
	GoodbyeRestart = "restart"
)

// GoodbyePayload is the body of MsgGoodbye.
type GoodbyePayload struct {
	Reason string `json:"reason"`
}

// Goodbye is what a client heard last, and when.
type Goodbye struct {
	Reason string
	At     time.Time
}

// restartMarkMaxAge bounds how old a restart mark may be and still count. A
// mark left by a kill-server --restart that never reached the daemon must not
// turn a later plain stop into a restart.
const restartMarkMaxAge = 30 * time.Second

// goodbyeWrite bounds the goodbye to one client. A client that does not read
// is not waited for.
const goodbyeWrite = 500 * time.Millisecond

// restartMarkPath is the restart mark beside the socket.
func restartMarkPath(socketPath string) string { return socketPath + ".restart" }

// MarkRestart leaves the mark that makes the daemon's next stop a restart.
func MarkRestart() error {
	sock, err := GetSocketPath()
	if err != nil {
		return err
	}
	return os.WriteFile(restartMarkPath(sock), nil, 0o600)
}

// goodbyeReason reads and removes the restart mark.
func (d *Daemon) goodbyeReason() string {
	path := restartMarkPath(d.manager.SocketPath())
	fi, err := os.Stat(path)
	if err != nil {
		return GoodbyeStop
	}
	_ = os.Remove(path)
	if time.Since(fi.ModTime()) > restartMarkMaxAge {
		return GoodbyeStop
	}
	return GoodbyeRestart
}

// sayGoodbye tells every client that reads it why the daemon stops. The
// writes run at once and each has a short deadline, so a client that does not
// read holds the shutdown back by goodbyeWrite at most.
func (d *Daemon) sayGoodbye() {
	msg, err := NewMessage(MsgGoodbye, &GoodbyePayload{Reason: d.goodbyeReason()})
	if err != nil {
		return
	}
	var wg sync.WaitGroup
	d.clientsMu.RLock()
	for _, cs := range d.clients {
		cs.mu.Lock()
		reads := cs.goodbye
		cs.mu.Unlock()
		if !reads {
			continue
		}
		wg.Go(func() {
			cs.sendMu.Lock()
			defer cs.sendMu.Unlock()
			_ = cs.conn.SetWriteDeadline(time.Now().Add(goodbyeWrite))
			_ = WriteMessage(cs.conn, msg)
		})
	}
	d.clientsMu.RUnlock()
	wg.Wait()
}

// Goodbye is the daemon's last word to this client, or nil when it said none.
func (c *TUIClient) Goodbye() *Goodbye { return c.goodbye.Load() }
