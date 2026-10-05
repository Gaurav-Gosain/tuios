// Package plural writes a count with its noun in the right number, so the
// output says "1 session" and "2 sessions" instead of "1 session(s)".
package plural

import "fmt"

// Integer is any count.
type Integer interface {
	~int | ~int8 | ~int16 | ~int32 | ~int64 | ~uint | ~uint8 | ~uint16 | ~uint32 | ~uint64
}

// Count returns n and the noun, with an s added when n is not 1. The noun is
// given in the singular.
func Count[N Integer](n N, noun string) string {
	return CountAs(n, noun, noun+"s")
}

// CountAs returns n and one or many, for a noun whose plural is not a plain s.
func CountAs[N Integer](n N, one, many string) string {
	return fmt.Sprint(n) + " " + Word(n, one, many)
}

// Word returns one when n is 1 and many otherwise, for a word that agrees with
// a count: "1 message waits", "2 messages wait".
func Word[N Integer](n N, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}
