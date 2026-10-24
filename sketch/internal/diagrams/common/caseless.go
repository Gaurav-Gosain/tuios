// Ported from pele src/diagrams/common/caseless.ts at e7f092a4de147cc32210913c20e36eaa5fcd0ef1.

package common

// WordAt reports whether `word`, written in lower case, is at `at`. Only ASCII letters match in either case,
// as with the `i` flag on a regular expression without the `u` flag.
//
// The port compares bytes: word is ASCII, and no byte of a character outside ASCII equals one.
func WordAt(src string, at int, word string) bool {
	if at < 0 || at+len(word) > len(src) {
		return false
	}
	for k := 0; k < len(word); k++ {
		c := src[at+k]
		w := word[k]
		if c != w && !(w >= 97 && w <= 122 && c == w-32) {
			return false
		}
	}
	return true
}
