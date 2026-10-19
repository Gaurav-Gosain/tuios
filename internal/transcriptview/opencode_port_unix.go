//go:build unix && !linux

package transcriptview

// portOwnedByMe cannot be shown from a socket table here, so the password
// is never sent and the export is read instead.
func portOwnedByMe(int) bool { return false }
