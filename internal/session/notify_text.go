package session

// notifyTextCap bounds the notification text an event carries. A notification
// is a sentence; one carrying a screenful is not worth fanning out.
const notifyTextCap = 512

// capNotifyText trims a notification field to notifyTextCap bytes on a rune
// boundary.
func capNotifyText(s string) string {
	if len(s) <= notifyTextCap {
		return s
	}
	cut := notifyTextCap
	for cut > 0 && !runeStart(s[cut]) {
		cut--
	}
	return s[:cut]
}

// runeStart reports whether b begins a UTF-8 sequence.
func runeStart(b byte) bool { return b&0xC0 != 0x80 }
