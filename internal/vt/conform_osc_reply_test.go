package vt_test

// Conformance for the form of OSC replies.
//
// xterm ends a reply to an OSC query with the terminator the query used: BEL
// for BEL and ST for ST. A guest that reads up to the terminator it sent does
// not see the other one, and waits out its timeout. And an OSC 10, 11 or 12
// with several items applies each item after the first to the next colour, so
// OSC 10 ; ? ; ? asks for the foreground and the background in one request.

import (
	"strings"
	"testing"
)

func TestConform_OSCReplyKeepsTheQueryTerminator(t *testing.T) {
	for _, tc := range []struct {
		name   string
		query  string // the OSC body, without ESC ] and the terminator
		prefix string // the start of the reply
	}{
		{"OSC 10", "10;?", "\x1b]10;rgb:"},
		{"OSC 11", "11;?", "\x1b]11;rgb:"},
		{"OSC 12", "12;?", "\x1b]12;rgb:"},
		{"OSC 4", "4;1;?", "\x1b]4;1;rgb:"},
		{"OSC 52", "52;c;?", "\x1b]52;c;"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, term := range []string{"\x07", "\x1b\\"} {
				got := replyChecked(t, "\x1b]"+tc.query+term)
				if !strings.HasPrefix(got, tc.prefix) || !strings.HasSuffix(got, term) {
					t.Errorf("query ended with %q replied %q, want a reply starting %q and ending %q",
						term, got, tc.prefix, term)
				}
				if strings.Count(got, "\x1b]") != 1 {
					t.Errorf("query ended with %q replied %q, want exactly one reply", term, got)
				}
			}
		})
	}
}

func TestConform_OSCDynamicColorItems(t *testing.T) {
	// Each item's answer is what the single query for that colour gets, so
	// the expectation is built from those rather than from literal colours.
	single := func(n, term string) string { return replyChecked(t, "\x1b]"+n+";?"+term) }
	fg, bg, cur := single("10", "\x1b\\"), single("11", "\x1b\\"), single("12", "\x1b\\")
	fgBEL, bgBEL, curBEL := single("10", "\x07"), single("11", "\x07"), single("12", "\x07")

	for _, tc := range []struct {
		name string
		in   string
		want string
	}{
		{"foreground and background", "\x1b]10;?;?\x1b\\", fg + bg},
		{"all three, ended with BEL", "\x1b]10;?;?;?\x07", fgBEL + bgBEL + curBEL},
		{"background and cursor", "\x1b]11;?;?\x1b\\", bg + cur},
		// There is no colour past the cursor's here, so the extra item is
		// dropped rather than answered as something else.
		{"an item past the cursor colour is dropped", "\x1b]12;?;?\x1b\\", cur},
		{"a set item, then a query", "\x1b]10;#102030;?\x1b\\", bg},
		{"the set item took effect", "\x1b]10;#102030;?\x1b\\\x1b]10;?\x1b\\", bg + "\x1b]10;rgb:1010/2020/3030\x1b\\"},
		{"a query, then a set item", "\x1b]10;?;#405060\x1b\\\x1b]11;?\x07", fg + "\x1b]11;rgb:4040/5050/6060\x07"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := replyChecked(t, tc.in); got != tc.want {
				t.Errorf("reply = %q, want %q", got, tc.want)
			}
		})
	}
}
