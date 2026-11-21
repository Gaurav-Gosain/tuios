package app

import "time"

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
