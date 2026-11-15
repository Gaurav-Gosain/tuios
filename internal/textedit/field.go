package textedit

import (
	"strings"
	"unicode/utf8"
)

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
	// anchor is the other end of the selection, a byte offset, and sel
	// whether there is one. The selection is the text between anchor and
	// caret. Only a field whose panel draws it (LineOpts.Select) makes one.
	anchor int
	sel    bool
}

// LineOpts are what a field's panel decides about the keys.
type LineOpts struct {
	// AllowClear lets ctrl+u clear the field. A panel that gives ctrl+u
	// another meaning leaves it off, and the key is not consumed.
	AllowClear bool
	// Select lets shift with a move select text, and the select all keys
	// select the whole field. A field that does not draw a selection leaves
	// it off, and those keys are not consumed.
	Select bool
}

// String is the field's text.
func (l *Line) String() string { return l.text }

// Set replaces the text and puts the caret at its end.
func (l *Line) Set(s string) {
	l.text = s
	l.caret = len(s)
	l.sel = false
}

// Selection is the selected text as byte offsets [lo, hi). ok is false when
// nothing is selected.
func (l *Line) Selection() (lo, hi int, ok bool) {
	c := l.clampedCaret()
	if !l.sel {
		return 0, 0, false
	}
	a := min(max(l.anchor, 0), len(l.text))
	if a == c {
		return 0, 0, false
	}
	return min(a, c), max(a, c), true
}

// Caret is the caret's byte offset into the text.
func (l *Line) Caret() int { return l.clampedCaret() }

// SelectedText is the selected text, "" when nothing is selected.
func (l *Line) SelectedText() string {
	lo, hi, ok := l.Selection()
	if !ok {
		return ""
	}
	return l.text[lo:hi]
}

// SelectAll selects the whole text, with the caret at its end.
func (l *Line) SelectAll() {
	l.anchor, l.caret, l.sel = 0, len(l.text), l.text != ""
}

// deleteSelection takes the selected text out and puts the caret where it
// was. It reports whether there was a selection.
func (l *Line) deleteSelection() bool {
	lo, hi, ok := l.Selection()
	l.sel = false
	if !ok {
		return false
	}
	l.text = l.text[:lo] + l.text[hi:]
	l.caret = lo
	return true
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
	// Typed or pasted text takes the place of the selection.
	l.deleteSelection()
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
	if to, extend, ok := l.motion(key); ok {
		if extend {
			if !o.Select {
				return false, false
			}
			if !l.sel {
				l.anchor, l.sel = l.clampedCaret(), true
			}
			l.caret = to
			return false, true
		}
		// A plain left or right with text selected goes to that end of it,
		// as a native field does. Every other move drops the selection.
		if lo, hi, ok := l.Selection(); ok && (key == "left" || key == "right") {
			to = lo
			if key == "right" {
				to = hi
			}
		}
		l.caret, l.sel = to, false
		return false, true
	}
	if o.Select && (key == "super+a" || key == "ctrl+shift+a") {
		l.SelectAll()
		return false, true
	}
	if _, _, ok := l.Selection(); ok {
		switch {
		case key == "backspace" || key == "ctrl+h" || key == "delete" || IsWordDeleteKey(key) ||
			key == "alt+d" || key == "alt+delete" || key == "ctrl+delete":
			return l.deleteSelection(), true
		case key == "ctrl+u" && o.AllowClear:
			l.sel = false
		}
	} else {
		l.sel = false
	}
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
	case key == "space":
		return l.Insert(" "), true
	case text != "":
		return l.Insert(text), true
	case len(key) == 1 && key[0] >= 32 && key[0] <= 126:
		return l.Insert(key), true
	}
	return false, false
}

// motion is where a move key puts the caret, and whether it extends the
// selection (the key holds shift). ok is false for a key that is not a move.
//
// A word move is the arrow with any of alt, ctrl or meta, because terminals
// spell Option or Alt with an arrow differently: xterm, kitty, WezTerm and
// Ghostty send alt, iTerm2 can send meta (CSI 1;9), and ctrl is the Linux
// and Windows key. Terminal.app and iTerm2's Esc+ send alt+b and alt+f. A
// move to the start or the end is home and end, ctrl+a and ctrl+e, and
// super with an arrow, which is Command on a Mac.
func (l *Line) motion(key string) (to int, extend, ok bool) {
	mods, base := splitKey(key)
	c := l.clampedCaret()
	before, after := l.text[:c], l.text[c:]
	shift := mods["shift"]
	delete(mods, "shift")
	word := len(mods) > 0
	for m := range mods {
		if m != "alt" && m != "ctrl" && m != "meta" {
			word = false
		}
	}
	switch {
	case base == "left" && len(mods) == 0:
		if before == "" {
			return c, shift, true
		}
		_, size := utf8.DecodeLastRuneInString(before)
		return c - size, shift, true
	case base == "right" && len(mods) == 0:
		if after == "" {
			return c, shift, true
		}
		_, size := utf8.DecodeRuneInString(after)
		return c + size, shift, true
	case base == "left" && word:
		return len(DropLastWord(before)), shift, true
	case base == "right" && word:
		return c + nextWordEnd(after), shift, true
	case !shift && key == "alt+b":
		return len(DropLastWord(before)), false, true
	case !shift && key == "alt+f":
		return c + nextWordEnd(after), false, true
	case (base == "home" && len(mods) == 0) || (base == "left" && len(mods) == 1 && mods["super"]) || (!shift && key == "ctrl+a"):
		return 0, shift, true
	case (base == "end" && len(mods) == 0) || (base == "right" && len(mods) == 1 && mods["super"]) || (!shift && key == "ctrl+e"):
		return len(l.text), shift, true
	}
	return 0, false, false
}

// splitKey splits a key name such as "ctrl+shift+left" into its modifiers
// and the key.
func splitKey(key string) (map[string]bool, string) {
	parts := strings.Split(key, "+")
	base := parts[len(parts)-1]
	parts = parts[:len(parts)-1]
	if base == "" && len(parts) > 0 {
		// The key is "+" itself, as in "ctrl++".
		base, parts = "+", parts[:len(parts)-1]
	}
	mods := make(map[string]bool, len(parts))
	for _, p := range parts {
		mods[p] = true
	}
	return mods, base
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
