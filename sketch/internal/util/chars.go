// Ported from pele src/util/chars.ts at 8aff183be57021b533f319a29341a529be934b08.

// Package util holds pele's small shared helpers: character tests, the YAML
// subset for front matter, and URL sanitising.
package util

import "tuios.dev/sketch/internal/js"

// Tests on characters, as the lexers read them. Past the end of a string the
// code is -1, which is none of these.

// IsSpace is whitespace as `\s` sees it.
//
//go:noinline
func IsSpace(c rune) bool { return c >= 0 && js.IsSpace(c) }

func IsBlank(c rune) bool { return c == 32 || c == 9 }

// IsLineEnd is a line terminator: what `.` does not match.
func IsLineEnd(c rune) bool { return c == 10 || c == 13 || c == 0x2028 || c == 0x2029 }

//go:noinline
func IsDigit(c rune) bool { return c >= 48 && c <= 57 }

func IsLetter(c rune) bool { return (c >= 65 && c <= 90) || (c >= 97 && c <= 122) }

func IsHex(c rune) bool { return (c >= 48 && c <= 57) || (c >= 65 && c <= 70) || (c >= 97 && c <= 102) }

// IsWord is a character of `\w`.
//
//go:noinline
func IsWord(c rune) bool {
	return (c >= 48 && c <= 57) || (c >= 65 && c <= 90) || (c >= 97 && c <= 122) || c == 95
}
