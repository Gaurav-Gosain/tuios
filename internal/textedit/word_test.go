package textedit

import "testing"

// How these tests could pass wrongly, written down first:
//   - The edit could cut bytes, not runes. "déjà/café" and "日本語/テスト" hold
//     multi-byte letters, and a cut in the middle of one is invalid UTF-8.
//   - A word could stop at blanks only, as the rule did before. The shell's
//     Option+Backspace (readline's backward-kill-word) also stops at "/" "."
//     and "-", while "_" stays inside a word.
//   - tmux's command prompt and nvim's Ctrl-W take a run of punctuation as a
//     word of its own, so "src/dir1/" goes to "src/dir1". This rule skips the
//     punctuation and takes the word, so "src/dir1/" goes to "src/".
//   - A mark that attaches to a letter (the acute accent in a decomposed é)
//     must go with its letter, or a word key leaves a lone accent behind.

func TestDropLastWord(t *testing.T) {
	for _, tc := range []struct{ name, in, want string }{
		{"a slash ends a word", "word1/word2", "word1/"},
		{"a path loses its last part", "src/dir1/dir2", "src/dir1/"},
		{"the slash then the word before it", "word1/", ""},
		{"a dot ends a word", "my notes.txt", "my notes."},
		{"the dot and the word before it", "my notes.", "my "},
		{"the blank and the word before it", "my ", ""},
		{"an underscore is part of a word", "foo_bar", ""},
		{"a hyphen ends a word", "foo_bar-baz", "foo_bar-"},
		{"a branch", "feat/my-fix", "feat/my-"},
		{"a path", "src/components/Button.tsx", "src/components/Button."},
		{"digits are word characters", "v2/release9", "v2/"},
		{"blanks that end the text", "one two  ", "one "},
		{"only separators", "///", ""},
		{"only blanks", "   ", ""},
		{"one letter", "a", ""},
		{"empty", "", ""},
		{"multi-byte letters", "déjà/café", "déjà/"},
		{"letters that are not Latin", "日本語/テスト", "日本語/"},
		{"a mark goes with its letter", "x cafe\u0301", "x "},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := DropLastWord(tc.in); got != tc.want {
				t.Errorf("DropLastWord(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}

	// Pressed again and again the key walks a path back one part at a time:
	// src/dir1/dir2 becomes src/dir1/, then src/, then nothing.
	s, presses := "src/dir1/dir2", 0
	for _, want := range []string{"src/dir1/", "src/", ""} {
		s = DropLastWord(s)
		presses++
		if s != want {
			t.Fatalf("after %d presses the field is %q, want %q", presses, s, want)
		}
	}

	// A name with an extension walks back the same way.
	s, presses = "src/components/Button.tsx", 0
	steps := []string{"src/components/Button.", "src/components/", "src/", ""}
	for _, want := range steps {
		s = DropLastWord(s)
		presses++
		if s != want {
			t.Fatalf("after %d presses the field is %q, want %q", presses, s, want)
		}
	}
}
