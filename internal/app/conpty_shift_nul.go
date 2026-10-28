package app

import (
	"runtime"
	"time"

	tea "charm.land/bubbletea/v2"
)

// A phantom Ctrl+Space before a shifted character on Windows (#1269).
//
// On Windows the input reader in ultraviolet reads console input records and
// turns them back into bytes for the decoder. With ENABLE_VIRTUAL_TERMINAL_INPUT
// on, which it always sets, it writes the record's character for every key
// down. A key down that carries no character, such as Shift, so writes a NUL
// byte, and the decoder reads NUL as Ctrl+Space.
//
// ConPTY makes such a record for every character that needs Shift: it wraps
// the character in a Shift down and a Shift up of its own. WezTerm on Windows
// hands its panes "U" as Shift down (character 0), U down, U up, Shift up, and
// the reader turns that into "\x00U". One uppercase letter reaches tuios as
// Ctrl+Space then U, and after the leader the Ctrl+Space closes the which-key
// menu before the letter arrives. Caps Lock does the same, because the
// character is what ConPTY wraps and not the key.
//
// The bytes alone cannot tell this NUL from a real Ctrl+Space, which is also a
// NUL. Time can: the wrapped records arrive in one read, so the letter follows
// its NUL at once, and no person types Ctrl+Space and then a character within
// a few milliseconds. dropConPTYShiftNUL holds a Ctrl+Space for conptyNULHold.
// A key with text inside the hold ends it, and the held Ctrl+Space is dropped.
// Anything else, or the end of the hold, gives the Ctrl+Space back first.
//
// The fix belongs in ultraviolet's serializeWin32InputRecords, which should
// skip a key down whose character is 0 in VT input mode. This filter goes when
// that lands.

// conptyNULHold is how long a Ctrl+Space waits for the character ConPTY
// wrapped. The pair is decoded from one read, so the wait only has to cover
// the event loop's own delay. A real Ctrl+Space reaches Update this much late.
const conptyNULHold = 25 * time.Millisecond

// conptyInput says whether this process reads its keys from a Windows console,
// where the phantom NUL comes from. A test sets it to run the filter elsewhere.
var conptyInput = runtime.GOOS == "windows"

// heldNUL is a Ctrl+Space waiting to learn whether ConPTY made it.
type heldNUL struct {
	// keys are the held presses, in order. ConPTY can wrap one character in
	// more than one key down without a character, as with AltGr.
	keys []tea.KeyPressMsg
	// gen tells the hold's own expiry from that of an earlier hold.
	gen uint64
}

// heldNULExpired ends the hold with generation gen. The filter consumes it, so
// Update never sees it.
type heldNULExpired struct{ gen uint64 }

// releasedNUL is a held Ctrl+Space on its way back through the filter. It
// carries the key past the hold, which would otherwise take it again.
type releasedNUL struct{ key tea.KeyPressMsg }

// isNULKey reports whether k is what the decoder makes of a NUL byte.
func isNULKey(k tea.KeyPressMsg) bool {
	return k.Code == tea.KeySpace && k.Mod == tea.ModCtrl && k.Text == ""
}

// dropConPTYShiftNUL removes the Ctrl+Space ConPTY puts before a shifted
// character on a local Windows client. It returns nil for a message it holds
// or drops, and msg unchanged for anything it does not act on.
func (m *OS) dropConPTYShiftNUL(msg tea.Msg) tea.Msg {
	// A remote client's keys come as bytes from a terminal on another
	// machine, where a NUL is always a real Ctrl+Space.
	if !conptyInput || m.RemoteClient {
		return msg
	}
	h := &m.conptyNUL
	switch e := msg.(type) {
	case releasedNUL:
		return e.key
	case heldNULExpired:
		if e.gen != h.gen || len(h.keys) == 0 {
			return nil
		}
		return h.giveBack(nil)
	case tea.KeyPressMsg:
		if isNULKey(e) {
			h.keys = append(h.keys, e)
			if len(h.keys) > 1 {
				return nil
			}
			h.gen++
			gen := h.gen
			return tea.BatchMsg{tea.Tick(conptyNULHold, func(time.Time) tea.Msg {
				return heldNULExpired{gen: gen}
			})}
		}
		if len(h.keys) == 0 {
			return msg
		}
		if e.Text != "" {
			// The character ConPTY wrapped. Its Ctrl+Space was never typed.
			h.keys = h.keys[:0]
			return msg
		}
		return h.giveBack(msg)
	}
	return msg
}

// giveBack ends the hold. The held Ctrl+Space presses are delivered in order,
// then msg when it is not nil.
func (h *heldNUL) giveBack(msg tea.Msg) tea.Msg {
	defer func() { h.keys = h.keys[:0] }()
	if msg == nil && len(h.keys) == 1 {
		return h.keys[0]
	}
	cmds := make([]tea.Cmd, 0, len(h.keys)+1)
	for _, k := range h.keys {
		cmds = append(cmds, func() tea.Msg { return releasedNUL{key: k} })
	}
	if msg != nil {
		cmds = append(cmds, func() tea.Msg { return msg })
	}
	return tea.Sequence(cmds...)()
}
