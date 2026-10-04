package input

import (
	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
	"github.com/Gaurav-Gosain/tuios/internal/vt"
	"github.com/charmbracelet/x/ansi"
)

// paneKeyBytes encodes a key press for one pane from that pane's own kitty
// keyboard flags, and reports whether the bytes already hold its release.
//
// Each pane reads keys in the mode it asked for, so the focused pane and every
// multifocus peer get their own encoding: a shell peer gets CR for Enter while
// a kitty-protocol pane beside it gets CSI u.
//
// A pane that asked for event types gets a release for every press. The
// release is sent with the press when none will follow it:
//   - for the focused pane, when the host is not reporting releases (a host
//     without the protocol, or the moment after the pane takes the keyboard,
//     before the host has switched to the flags tuios asked for on its behalf)
//   - for a multifocus peer always, since releases go to the focused pane only
//
// Without it a compositor in the pane held the key down, and its client
// repeated it until the next key came.
func paneKeyBytes(host tea.KeyPressMsg, w *terminal.Window, o *app.OS, focused bool) (raw []byte, released bool) {
	flags := 0
	appCursorKeys := false
	if w.Terminal != nil {
		flags = w.Terminal.KittyKeyboardFlags()
		appCursorKeys = w.Terminal.ApplicationCursorKeys()
	}
	key := vtKeyFromBubbletea(host)
	if flags != 0 {
		if encoded := vt.EncodeKeyCSIu(key, flags); encoded != "" {
			raw = []byte(encoded)
		}
	}
	// modifyOtherKeys is xterm's older way to ask for keys the legacy encoding
	// cannot tell apart. The kitty flags win when a pane set both: the
	// protocol says so, and a pane with flags set has chosen CSI u.
	if len(raw) == 0 && flags == 0 && w.Terminal != nil {
		if level := w.Terminal.ModifyOtherKeys(); level > 0 {
			if encoded := vt.EncodeModifyOtherKeys(key, level); encoded != "" {
				raw = []byte(encoded)
			}
		}
	}
	if len(raw) == 0 {
		raw = getRawKeyBytesWithMode(host, appCursorKeys)
	}
	if len(raw) == 0 || flags&ansi.KittyReportEventTypes == 0 {
		return raw, false
	}
	if focused && o.HostReportsReleases() {
		return raw, false
	}
	release := vt.EncodeKeyReleaseCSIu(key, flags)
	if release == "" {
		// kitty sends no release for this key in this mode (Enter, Tab and
		// Backspace without report-all-keys): none is owed.
		return raw, false
	}
	return append(raw, release...), true
}
