package input

import (
	tea "charm.land/bubbletea/v2"

	"github.com/Gaurav-Gosain/tuios/internal/app"
)

// The screenshot chord over an open overlay.
//
// An overlay owns the keyboard while it is up, and several of them own the
// leader with it: the review in either mode, and in terminal mode every panel
// that is checked ahead of the leader. Left alone, the leader and the key after
// it go to the overlay, and the one screen a person wants to capture cannot be.
//
// So a leader pressed over an overlay is held for exactly one key. When that
// key is the screenshot key, capture mode opens over the overlay, which stays
// open and is part of what is captured. Any other key is not the chord, so the
// held leader and then the key are routed exactly as they would be without the
// hold, one after the other. An overlay therefore sees the same keys in the
// same order; the leader only reaches it one key later.
//
// Copy mode and the scrollback browser are left out (see OS.OverlayOnScreen).
// They are reading views over a pane, not panels, and both page up on ctrl+b,
// the default leader, which has to happen when it is pressed rather than one
// key later.

// isScreenshotKey reports whether msg is bound to the screenshot action in the
// prefix section, the key that follows the leader in the chord.
func isScreenshotKey(msg tea.KeyPressMsg, o *app.OS) bool {
	if o.KeybindRegistry == nil {
		return false
	}
	return lookupAction(o, msg, o.KeybindRegistry.GetPrefixAction) == "prefix_screenshot"
}

// routeOverlayScreenshot runs the screenshot chord over an open overlay. It
// reports whether it answered the key; when it did not, the caller routes the
// key as usual.
func routeOverlayScreenshot(msg tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd, bool) {
	if held := o.OverlayLeader; held != nil {
		o.OverlayLeader = nil
		if isScreenshotKey(msg, o) {
			o.NoteAction("prefix_screenshot")
			o.BeginCapture(false)
			return o, nil, true
		}
		// Not the chord: the leader goes where it would have gone, then this key.
		m, first := routeKey(*held, o)
		m, second := routeKey(msg, m)
		return m, tea.Batch(first, second), true
	}
	if o.PrefixActive || !isLeaderKey(msg, &o.Settings, o.HostBaseCode(msg)) || !o.OverlayOnScreen() {
		return o, nil, false
	}
	held := msg
	o.OverlayLeader = &held
	return o, nil, true
}
