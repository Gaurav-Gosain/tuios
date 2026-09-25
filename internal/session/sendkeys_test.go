package session

import (
	"errors"
	"strings"
	"testing"
)

func TestParseSendKeysSpellings(t *testing.T) {
	tests := []struct {
		name      string
		keys      string
		appCursor bool
		want      string
	}{
		{name: "Up", keys: "Up", want: "\x1b[A"},
		{name: "lower case", keys: "up", want: "\x1b[A"},
		{name: "upper case", keys: "UP", want: "\x1b[A"},
		{name: "arrow-up", keys: "arrow-up", want: "\x1b[A"},
		{name: "ArrowUp", keys: "ArrowUp", want: "\x1b[A"},
		{name: "up-arrow", keys: "up-arrow", want: "\x1b[A"},
		{name: "curses KEY_UP", keys: "KEY_UP", want: "\x1b[A"},
		{name: "vim <Up>", keys: "<Up>", want: "\x1b[A"},
		{name: "escape written \\e", keys: `\e[A`, want: "\x1b[A"},
		{name: "escape written \\x1b", keys: `\x1b[A`, want: "\x1b[A"},
		{name: "escape written \\033", keys: `\033[A`, want: "\x1b[A"},
		{name: "escape written ^[", keys: `^[[A`, want: "\x1b[A"},
		{name: "escape byte itself", keys: "\x1b[A", want: "\x1b[A"},
		{name: "Down in application cursor mode", keys: "Down", appCursor: true, want: "\x1bOB"},
		{name: "Home in application cursor mode", keys: "Home", appCursor: true, want: "\x1bOH"},
		{name: "PageDown", keys: "PageDown", want: "\x1b[6~"},
		{name: "PgDn", keys: "PgDn", want: "\x1b[6~"},
		{name: "Page_Down", keys: "Page_Down", want: "\x1b[6~"},
		{name: "KEY_NPAGE", keys: "KEY_NPAGE", want: "\x1b[6~"},
		{name: "PgUp", keys: "PgUp", want: "\x1b[5~"},
		{name: "page-up", keys: "page-up", want: "\x1b[5~"},
		{name: "Enter", keys: "Enter", want: "\r"},
		{name: "Return", keys: "Return", want: "\r"},
		{name: "Esc", keys: "Esc", want: "\x1b"},
		{name: "BSpace", keys: "BSpace", want: "\x7f"},
		{name: "BTab", keys: "BTab", want: "\x1b[Z"},
		{name: "shift+Tab", keys: "shift+Tab", want: "\x1b[Z"},
		{name: "F1", keys: "F1", want: "\x1bOP"},
		{name: "f5", keys: "f5", want: "\x1b[15~"},
		{name: "ctrl+c", keys: "ctrl+c", want: "\x03"},
		{name: "Ctrl+C", keys: "Ctrl+C", want: "\x03"},
		{name: "tmux C-c", keys: "C-c", want: "\x03"},
		{name: "caret ^C", keys: "^C", want: "\x03"},
		{name: "ctrl+b is a byte for the window", keys: "ctrl+b", want: "\x02"},
		{name: "alt+b", keys: "alt+b", want: "\x1bb"},
		{name: "tmux M-b", keys: "M-b", want: "\x1bb"},
		{name: "shift+a", keys: "shift+a", want: "A"},
		{name: "ctrl+Up", keys: "ctrl+Up", want: "\x1b[1;5A"},
		{name: "shift+Down", keys: "shift+Down", want: "\x1b[1;2B"},
		{name: "alt+PageDown", keys: "alt+PageDown", want: "\x1b[6;3~"},
		{name: "ctrl+Space", keys: "ctrl+Space", want: "\x00"},
		{name: "sequence with spaces and commas", keys: "Down Down,PageDown", want: "\x1b[B\x1b[B\x1b[6~"},
		{name: "single characters", keys: "q j G /", want: "qjG/"},
		{name: "a lower-case word is typed", keys: "ls,Enter", want: "ls\r"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			keys, err := parseSendKeys(tc.keys, 1)
			if err != nil {
				t.Fatalf("parseSendKeys(%q): %v", tc.keys, err)
			}
			got, err := sendKeysBytes(keys, tc.appCursor)
			if err != nil {
				t.Fatalf("sendKeysBytes(%q): %v", tc.keys, err)
			}
			if string(got) != tc.want {
				t.Errorf("%q gave %q, want %q", tc.keys, got, tc.want)
			}
		})
	}
}

func TestParseSendKeysRepeat(t *testing.T) {
	keys, err := parseSendKeys("Down Up", 3)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := sendKeysBytes(keys, false)
	if want := strings.Repeat("\x1b[B\x1b[A", 3); string(got) != want {
		t.Errorf("repeat 3 gave %q, want %q", got, want)
	}
	for _, n := range []int{-1, maxSendKeysRepeat + 1} {
		if _, err := parseSendKeys("Down", n); err == nil {
			t.Errorf("repeat %d was accepted", n)
		}
	}
}

func TestParseSendKeysRejectsWhatLooksLikeAKey(t *testing.T) {
	tests := []struct {
		keys, didYouMean string
	}{
		{keys: "Dwon", didYouMean: "Down"},
		{keys: "KEY_FOO"},
		{keys: "arrow-diagonal"},
		{keys: "<Upp>", didYouMean: "Up"},
		{keys: "F13", didYouMean: "F1"},
		{keys: "ctrl+Uup", didYouMean: "Up"},
		{keys: "Pagedwn", didYouMean: "PageDown"},
	}
	for _, tc := range tests {
		t.Run(tc.keys, func(t *testing.T) {
			_, err := parseSendKeys("Down "+tc.keys, 1)
			var unknown errUnknownKey
			if !errors.As(err, &unknown) {
				t.Fatalf("%q: got %v, want an unknown key error", tc.keys, err)
			}
			if unknown.didYouMean != tc.didYouMean {
				t.Errorf("%q: did you mean %q, want %q", tc.keys, unknown.didYouMean, tc.didYouMean)
			}
		})
	}
	for _, bad := range []string{"ctrl+shift", "ctrl+1", "ctrl+"} {
		if _, err := parseSendKeys(bad, 1); err == nil {
			t.Errorf("%q was accepted", bad)
		}
	}
}

func TestSendKeysCanonicalForTheClient(t *testing.T) {
	keys, err := parseSendKeys("arrow-down KEY_UP PgDn C-c BTab PREFIX q ls", 1)
	if err != nil {
		t.Fatal(err)
	}
	got, err := sendKeysCanonical(keys)
	if err != nil {
		t.Fatal(err)
	}
	if want := "Down Up PageDown ctrl+c shift+Tab PREFIX q ls"; got != want {
		t.Errorf("canonical %q, want %q", got, want)
	}
	escape, _ := parseSendKeys(`\e[A`, 1)
	if _, err := sendKeysCanonical(escape); err == nil {
		t.Error("an escape sequence was handed to the client")
	}
}
