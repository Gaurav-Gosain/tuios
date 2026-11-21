package textedit

import "testing"

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

	s, presses := "src/dir1/dir2", 0
	for _, want := range []string{"src/dir1/", "src/", ""} {
		s = DropLastWord(s)
		presses++
		if s != want {
			t.Fatalf("after %d presses the field is %q, want %q", presses, s, want)
		}
	}

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
