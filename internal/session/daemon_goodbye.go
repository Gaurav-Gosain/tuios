package session

import (
	"os"
	"sync"
	"time"
)

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

const restartMarkMaxAge = 30 * time.Second

const goodbyeWrite = 500 * time.Millisecond

func restartMarkPath(socketPath string) string { return socketPath + ".restart" }

// MarkRestart leaves the mark that makes the daemon's next stop a restart.
func MarkRestart() error {
	sock, err := GetSocketPath()
	if err != nil {
		return err
	}
	return os.WriteFile(restartMarkPath(sock), nil, 0o600)
}

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
