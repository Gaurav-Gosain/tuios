//go:build !unix

package session

func stdinIsTerminal(string) bool { return false }
