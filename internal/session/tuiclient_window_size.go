package session

import (
	"os"
	"time"
)

const activityInterval = 100 * time.Millisecond

// SessionWindowSize is the window_size policy the session's size was last
// settled under, or "" when the daemon has not said.
func (c *TUIClient) SessionWindowSize() string {
	if c == nil {
		return ""
	}
	if p := c.sizePolicy.Load(); p != nil {
		return *p
	}
	return ""
}

type sessionSizer struct {
	machine, client, kind string
}

// SessionSizedBy is the machine of the client whose size the session took,
// or "" when the daemon has not said.
func (c *TUIClient) SessionSizedBy() string {
	if c == nil {
		return ""
	}
	if p := c.sizedBy.Load(); p != nil {
		return p.machine
	}
	return ""
}

// SessionSizedByKind is the ClientKind of the client whose size the session
// took: ClientKindSSH, ClientKindWeb, or "".
func (c *TUIClient) SessionSizedByKind() string {
	if c == nil {
		return ""
	}
	if p := c.sizedBy.Load(); p != nil {
		return p.kind
	}
	return ""
}

// Machine is the host name this client's hello gives the daemon.
func (c *TUIClient) Machine() string { return localHostLabel() }

// AttachClientCount is how many clients the session had with this one when
// the last attach reply was written, or 0 when the daemon did not say.
func (c *TUIClient) AttachClientCount() int {
	if c == nil {
		return 0
	}
	return int(c.attachClients.Load())
}

// SessionSizedByOther reports whether the daemon said that another client
// sets the session's size. It is false when this client sets it, and when
// the daemon has not said, as an older daemon never does.
func (c *TUIClient) SessionSizedByOther() bool {
	if c == nil {
		return false
	}
	owner, self := c.sizedBy.Load(), c.selfID.Load()
	return owner != nil && self != nil && owner.client != "" && *self != "" && owner.client != *self
}

// SessionSizedBySelf reports whether the daemon said that this client sets the
// session's size. It is false when another client sets it, when no one client
// does (under smallest, with one client setting the width and another the
// height), and when the daemon has not said.
func (c *TUIClient) SessionSizedBySelf() bool {
	if c == nil {
		return false
	}
	owner, self := c.sizedBy.Load(), c.selfID.Load()
	return owner != nil && self != nil && *self != "" && owner.client == *self
}

func (c *TUIClient) noteSizePolicy(policy string) {
	if policy == "" {
		c.sizePolicy.Store(nil)
		return
	}
	c.sizePolicy.Store(&policy)
}

// ReportActivity tells the daemon the person at this client gave input, at
// most one message per activityInterval: the first input of a burst goes at once, so
// a switch is never delayed, and the rest of the burst is folded into it. It
// arms no timer.
func (c *TUIClient) ReportActivity(now time.Time) {
	if c == nil {
		return
	}
	c.activityMu.Lock()
	if now.Sub(c.lastActivity) < activityInterval {
		c.activityMu.Unlock()
		return
	}
	c.lastActivity = now
	c.activityMu.Unlock()
	msg, err := NewMessage(MsgClientActivity, nil)
	if err != nil {
		return
	}
	_ = c.send(msg)
}

func (c *TUIClient) viewOnly() bool {
	return c.ViewOnly || os.Getenv("TUIOS_VIEW_ONLY") == "1"
}
