// Package shquote writes a string as one word of a POSIX shell command line,
// so sh reads it back unchanged. Every place tuios builds a command for a
// shell, local or over ssh, quotes through it, so there is one rule for
// which characters need quotes.
package shquote

import "strings"

// Quote is s in single quotes, with each single quote inside closed, escaped
// and opened again. It always quotes, so the result is the same shape for
// every input.
func Quote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// Word is s as one shell word: as it is when sh passes every byte through,
// and Quote(s) otherwise. The empty string is ”.
//
// A byte passes through when it is an ASCII letter or digit or one of
// "@%+:,./_-=". A word that has the shape NAME=value is quoted, because as the
// first word of a command sh reads it as an assignment and not as a program.
func Word(s string) string {
	if s == "" || !plain(s) || assignment(s) {
		return Quote(s)
	}
	return s
}

func plain(s string) bool {
	for i := range len(s) {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		case strings.IndexByte("@%+:,./_-=", c) >= 0:
		default:
			return false
		}
	}
	return true
}

// assignment reports whether s starts with a shell variable name and "=".
func assignment(s string) bool {
	for i := range len(s) {
		c := s[i]
		switch {
		case c == '=':
			return i > 0
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c == '_':
		case c >= '0' && c <= '9' && i > 0:
		default:
			return false
		}
	}
	return false
}
