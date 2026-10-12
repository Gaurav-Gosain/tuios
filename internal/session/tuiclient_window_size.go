package session

import (
	"os"
	"time"
)

// legacyWindowSize makes this client behave as a build from before the
// window_size policy: its hello leaves WindowSize out, it reports no
// activity, and it never draws a session larger than its own terminal. It is
// TUIOS_WINDOW_SIZE_LEGACY=1, and it exists so the mixed-version promise can
// be tested without an old build. See window_size.go.
func legacyWindowSize() bool {
	return os.Getenv("TUIOS_WINDOW_SIZE_LEGACY") == "1"
}

// legacyViewChrome makes this client behave as a build from before
// HelloPayload.ViewChrome: its hello leaves ViewChrome out, and it lays the
// panes out around its own chrome whatever the session agreed. It is
// TUIOS_VIEW_CHROME_LEGACY=1, so the mixed-version case can be tested without
// an old build.
func legacyViewChrome() bool {
	return os.Getenv("TUIOS_VIEW_CHROME_LEGACY") == "1"
}

// ViewChrome reports whether this client's hello offered ViewChrome: it may
// lay the panes out around an agreed reserve smaller than its own chrome.
func (c *TUIClient) ViewChrome() bool {
	return c != nil && c.viewChrome
}

// activityInterval is the shortest gap between two MsgClientActivity sends.
// The daemon holds the latest client for a second after its last input (see
// latestHold), so a report at most this stale costs nothing, and a person
// typing sends a few small messages a second rather than one per key.
const activityInterval = 100 * time.Millisecond

// WindowSizeAware reports whether the daemon sizes the session by its
// window_size policy and reads activity, so this client may be handed a
// session larger than its terminal and must draw it as a view.
func (c *TUIClient) WindowSizeAware() bool {
	return c != nil && c.windowSize
}

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

// sessionSizer is the client whose size the session took: its machine and its
// connection ID.
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

// noteSizePolicy records the session's window_size policy, from an attach
// reply. Empty, from a daemon that does not say it, records it as unknown.
func (c *TUIClient) noteSizePolicy(policy string) {
	if policy == "" {
		c.sizePolicy.Store(nil)
		return
	}
	c.sizePolicy.Store(&policy)
}

// ReportActivity tells the daemon the person at this client gave input. It
// sends nothing to a daemon that did not offer WindowSize, and at most one
// message per activityInterval: the first input of a burst goes at once, so
// a switch is never delayed, and the rest of the burst is folded into it. It
// arms no timer.
func (c *TUIClient) ReportActivity(now time.Time) {
	if !c.WindowSizeAware() {
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

// viewOnly is what the attach says about the client's input: ViewOnly, or
// TUIOS_VIEW_ONLY=1, which lets a test attach a native client as a viewer.
func (c *TUIClient) viewOnly() bool {
	return c.ViewOnly || os.Getenv("TUIOS_VIEW_ONLY") == "1"
}
