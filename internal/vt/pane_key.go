package vt

// EncodePaneKey encodes a key press in the keyboard mode a pane asked for, or
// returns "" when the key keeps its legacy encoding in that mode. kittyFlags is
// the pane's kitty keyboard flags and modifyOtherKeys its XTMODKEYS level.
//
// The kitty flags win when a pane set both: the protocol says so, and a pane
// with flags set has chosen CSI u. modifyOtherKeys is xterm's older way to ask
// for keys the legacy encoding cannot tell apart.
//
// Every route that writes a key press to a pane goes through here, so a key a
// person types and the same key sent with send-keys reach the pane as the same
// bytes.
func EncodePaneKey(key KeyPressEvent, kittyFlags, modifyOtherKeys int) string {
	if kittyFlags != 0 {
		return EncodeKeyCSIu(key, kittyFlags)
	}
	if modifyOtherKeys > 0 {
		return EncodeModifyOtherKeys(key, modifyOtherKeys)
	}
	return ""
}
