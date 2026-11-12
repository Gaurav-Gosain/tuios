package textedit

import "unicode/utf8"

// Line is a one-line text field: its text and a caret. Every short field that
// takes typing (a picker's filter, a name to save under) edits through it, so
// the same key does the same thing in each of them. The zero value is an empty
// field.
//
// The caret is a byte offset into the text that always sits on a rune
// boundary, so no edit ever cuts a character in two.
type Line struct {
	text  string
	caret int
}

// LineOpts are what a field's panel decides about the keys.
type LineOpts struct {
	// AllowClear lets ctrl+u clear the field. A panel that gives ctrl+u
	// another meaning leaves it off, and the key is not consumed.
	AllowClear bool
}

// String is the field's text.
func (l *Line) String() string { return l.text }

// Set replaces the text and puts the caret at its end.
func (l *Line) Set(s string) {
	l.text = s
	l.caret = len(s)
}

// Parts splits the text at the caret: the text before it, the character
// under it ("" at the end), and the text after that character. A field draws
// the caret on the middle part.
func (l *Line) Parts() (before, at, after string) {
	c := l.clampedCaret()
	before, rest := l.text[:c], l.text[c:]
	if rest == "" {
		return before, "", ""
	}
	_, size := utf8.DecodeRuneInString(rest)
	return before, rest[:size], rest[size:]
}

// Insert types s at the caret, as a paste does. The caller cleans s for a
// one-line field first (control characters out, line breaks as spaces). It
// reports whether the text changed.
func (l *Line) Insert(s string) bool {
	if s == "" {
		return false
	}
	c := l.clampedCaret()
	l.text = l.text[:c] + s + l.text[c:]
	l.caret = c + len(s)
	return true
}

// Edit applies one key press to the field, by the rules every short field
// shares:
//   - backspace and ctrl+h remove the character before the caret, and delete
//     the one under it.
//   - alt+backspace, ctrl+w and ctrl+backspace remove the word before the
//     caret, by the rule of DropLastWord.
//   - ctrl+u clears the field when the panel allows it.
//   - alt+d, alt+delete and ctrl+delete remove the word after the caret.
//   - ctrl+k removes the text from the caret to the end.
//   - left and right move the caret a character; alt+left, ctrl+left and
//     alt+b, and alt+right, ctrl+right and alt+f, a word; home and ctrl+a, and
//     end and ctrl+e, to the start and the end. A list reads home and end
//     before its filter does, so there they move the list.
//
// These are the composer's keys for the same moves, so no key that edits in
// an agent answer field does nothing in a dialog's field.
//   - space types a space, text types the text the key carries, and a single
//     printable ASCII key name types itself, for a synthesized key.
//
// key is the key press's name, as tea.KeyPressMsg.String gives it. consumed
// reports whether the key was a field key at all; changed reports whether the
// text changed, except that ctrl+u always reports a change, because a list
// refilters on it even when the field is empty.
func (l *Line) Edit(key, text string, o LineOpts) (changed, consumed bool) {
	c := l.clampedCaret()
	before, after := l.text[:c], l.text[c:]
	switch {
	case key == "backspace" || key == "ctrl+h":
		if before == "" {
			return false, true
		}
		_, size := utf8.DecodeLastRuneInString(before)
		l.text = before[:len(before)-size] + after
		l.caret = c - size
		return true, true
	case key == "delete":
		if after == "" {
			return false, true
		}
		_, size := utf8.DecodeRuneInString(after)
		l.text = before + after[size:]
		return true, true
	case IsWordDeleteKey(key):
		if before == "" {
			return false, true
		}
		kept := DropLastWord(before)
		l.text = kept + after
		l.caret = len(kept)
		return true, true
	case key == "alt+d" || key == "alt+delete" || key == "ctrl+delete":
		n := nextWordEnd(after)
		if n == 0 {
			return false, true
		}
		l.text = before + after[n:]
		return true, true
	case key == "ctrl+u":
		if !o.AllowClear {
			return false, false
		}
		l.text, l.caret = "", 0
		return true, true
	case key == "ctrl+k":
		if after == "" {
			return false, true
		}
		l.text = before
		return true, true
	case key == "left":
		if before != "" {
			_, size := utf8.DecodeLastRuneInString(before)
			l.caret = c - size
		}
		return false, true
	case key == "right":
		if after != "" {
			_, size := utf8.DecodeRuneInString(after)
			l.caret = c + size
		}
		return false, true
	case key == "alt+left" || key == "ctrl+left" || key == "alt+b":
		l.caret = len(DropLastWord(before))
		return false, true
	case key == "alt+right" || key == "ctrl+right" || key == "alt+f":
		l.caret = c + nextWordEnd(after)
		return false, true
	case key == "home" || key == "ctrl+a":
		l.caret = 0
		return false, true
	case key == "end" || key == "ctrl+e":
		l.caret = len(l.text)
		return false, true
	case key == "space":
		return l.Insert(" "), true
	case text != "":
		return l.Insert(text), true
	case len(key) == 1 && key[0] >= 32 && key[0] <= 126:
		return l.Insert(key), true
	}
	return false, false
}

// nextWordEnd is how many bytes of s a move to the end of the next word
// passes: the characters that are not part of a word, then the word, the
// mirror of DropLastWord.
func nextWordEnd(s string) int {
	i := 0
	for i < len(s) {
		r, size := utf8.DecodeRuneInString(s[i:])
		if isWordRune(r) {
			break
		}
		i += size
	}
	for i < len(s) {
		r, size := utf8.DecodeRuneInString(s[i:])
		if !isWordRune(r) {
			break
		}
		i += size
	}
	return i
}

// clampedCaret is the caret, kept inside the text and on a rune boundary.
func (l *Line) clampedCaret() int {
	c := min(max(l.caret, 0), len(l.text))
	for c > 0 && c < len(l.text) && !utf8.RuneStart(l.text[c]) {
		c--
	}
	l.caret = c
	return c
}
