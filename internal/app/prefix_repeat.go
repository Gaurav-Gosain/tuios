package app

import "time"

// The prefix repeat window: tmux's repeat-time.
//
// A prefix command that is worth pressing twice leaves the prefix armed for a
// moment, so ctrl+b then left left left walks three columns instead of one.
// Without it every step costs its own prefix press, which is the difference
// between navigating with the prefix and giving up on it. It matters most on
// macOS, where the direct chords are the ones a terminal is most likely to
// swallow, so the prefix is the path that always works.
//
// It is a deadline read on the next key rather than a timer. Nothing has to
// fire when the window closes: the next key either arrives inside it or does
// not, and a key arriving after it is an ordinary key. That also means an idle
// client arms no work at all.

// ArmPrefixRepeat keeps the prefix live for the configured window.
func (m *OS) ArmPrefixRepeat() {
	if m.Settings.PrefixRepeatTime <= 0 {
		return
	}
	m.prefixRepeatUntil = time.Now().Add(
		time.Duration(m.Settings.PrefixRepeatTime) * time.Millisecond)
}

// PrefixRepeatLive reports whether a key arriving now continues the last
// prefix command.
func (m *OS) PrefixRepeatLive() bool {
	if m.Settings.PrefixRepeatTime <= 0 || m.prefixRepeatUntil.IsZero() {
		return false
	}
	return time.Now().Before(m.prefixRepeatUntil)
}

// ClearPrefixRepeat closes the window.
//
// It is called on the first key that is not a repeat of the command, so a key
// struck just after the window for any other reason is not measured against a
// deadline that has already passed.
func (m *OS) ClearPrefixRepeat() {
	m.prefixRepeatUntil = time.Time{}
}
