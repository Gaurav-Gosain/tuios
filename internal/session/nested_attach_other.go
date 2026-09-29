//go:build !unix

package session

// stdinIsTerminal has no terminal path to compare on this platform, so the
// client check never refuses and the daemon decides.
func stdinIsTerminal(string) bool { return false }
