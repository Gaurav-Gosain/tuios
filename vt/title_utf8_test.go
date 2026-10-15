package vt

import (
	"testing"
	"unicode/utf8"
)

// TestTitleFromGuest drives OSC 2 through the emulator, which is the path a
// guest takes.
//
// A title is chrome: it is drawn in a rail row, a window frame and a session
// listing, and it is serialised into JSON. An invalid byte survives all of
// that as a replacement character, which draws as a tofu box and marshals as
// U+FFFD, so one bad byte from a guest put a black diamond in three places at
// once. The guard must drop only the bad bytes: multi-byte characters and a
// real U+FFFD, which is three valid bytes, stay.
//
// Only the first ';' separates the command from the data, so a title holding a
// semicolon keeps it.
//
// Negative control: taking the OSC payload as a string leaves the title
// invalid and the bad-byte cases fail.
func TestTitleFromGuest(t *testing.T) {
	for _, tc := range []struct {
		name, payload, want string
	}{
		{"a lone continuation byte is dropped", "tui\xffos", "tuios"},
		{"nothing but bad bytes is empty", "\xff\xfe\x80", ""},
		{"a semicolon is kept", "foo;bar", "foo;bar"},
		{"plain text", "tuios", "tuios"},
		{"accents", "café", "café"},
		{"wide characters", "日本語", "日本語"},
		{"a symbol", "✳ building", "✳ building"},
		{"a real replacement character", "before � after", "before � after"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := NewEmulator(80, 24)
			defer e.Close()
			_, _ = e.Write([]byte("\x1b]2;" + tc.payload + "\x07"))
			if e.title != tc.want {
				t.Errorf("title = %q, want %q", e.title, tc.want)
			}
			if !utf8.ValidString(e.title) {
				t.Errorf("title %q is not valid UTF-8", e.title)
			}
		})
	}
}
