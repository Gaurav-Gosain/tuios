// Package textedit holds the editing rules that tuios's text fields share, so a
// field in any package edits the way the others do.
package textedit

import (
	"unicode"
	"unicode/utf8"
)

// DropLastWord removes the last word of s the way the shell's Option+Backspace
// does, which is readline's backward-kill-word: skip the characters at the end
// of s that are not part of a word, then take the run of word characters before
// them. A word is letters, digits and "_". A blank, "/", "." or "-" ends one.
//
//	"src/dir1/dir2" -> "src/dir1/" -> "src/" -> ""
//	"my notes.txt"  -> "my notes." -> "my "  -> ""
//
// tmux's command prompt and nvim's Ctrl-W differ after the first press: they
// take a run of punctuation as a word of its own, so they go "src/dir1/",
// "src/dir1", "src/". This rule skips the punctuation and spends the press on
// the word, which reads better in a path.
//
// Every short text field of tuios that edits by words uses it: the rename
// dialog, the filtering overlays and pickers, the search boxes, and the file and
// worktree dialogs. The reply composer and loom's fields keep their own rule,
// which stops at white space only.
//
// It steps in runes, so a word of multi-byte text is cut whole and never into
// invalid UTF-8, and a mark that attaches to a letter goes with the letter.
func DropLastWord(s string) string {
	end := len(s)
	for end > 0 {
		r, size := utf8.DecodeLastRuneInString(s[:end])
		if isWordRune(r) {
			break
		}
		end -= size
	}
	for end > 0 {
		r, size := utf8.DecodeLastRuneInString(s[:end])
		if !isWordRune(r) {
			break
		}
		end -= size
	}
	return s[:end]
}

// isWordRune reports whether r is part of a word for DropLastWord.
func isWordRune(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r) || unicode.IsMark(r) || r == '_'
}

// IsWordDeleteKey reports whether key, as a key press names it, is one of the
// chords that delete the last word: Option+Backspace (alt+backspace, when the
// terminal sends Option as Alt), Ctrl+W and Ctrl+Backspace. A place that
// switches on a key's code must test it before backspace, since Option+Backspace
// has the code of backspace and would take one character.
func IsWordDeleteKey(key string) bool {
	return key == "alt+backspace" || key == "ctrl+w" || key == "ctrl+backspace"
}
