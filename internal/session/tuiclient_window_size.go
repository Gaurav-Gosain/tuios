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
