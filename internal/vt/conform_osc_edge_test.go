package vt_test

// Conformance for OSC payloads that are truncated, oversized or hostile.
//
// conform_string_test.go covers the OSC commands this emulator implements with
// payloads they expect. This covers the parser underneath them: which
// terminators end a string, which control bytes abort one, and what happens
// when a guest sends a megabyte and no terminator at all. An OSC accumulator is
// the one place in a terminal where a guest chooses how much memory to spend,
// and this one runs in the daemon, so the answer has to be bounded.
//
// Cases follow kitty's kitty_tests/parser.py, ghostty's src/terminal/osc.zig
// tests, and xterm's rules for what ends a string in charproc.c.

import (
	"strings"
	"testing"
)

// TestConform_OSCTerminators walks the ways a string can end.
func TestConform_OSCTerminators(t *testing.T) {
	for _, tc := range []struct {
		name  string
		in    string
		title string
	}{
		{"BEL ends an OSC", "\x1b]0;hello\x07", "hello"},
		{"seven-bit ST ends an OSC", "\x1b]0;hello\x1b\\", "hello"},

		// The eight-bit ST is one byte. A guest that has asked for eight-bit
		// controls sends it, and a parser that only knows ESC-backslash
		// swallows the rest of the stream looking for a terminator that
		// already went past.
		{"eight-bit ST ends an OSC", "\x1b]0;hello\x9c", "hello"},

		// An OSC with no payload separator is still a command. Setting an
		// empty title is a thing programs do on exit.
		{"an OSC with no semicolon sets an empty title", "\x1b]0\x07", ""},
		{"an OSC with an empty payload sets an empty title", "\x1b]0;\x07", ""},

		// A semicolon inside the payload belongs to the payload. Titles
		// contain them.
		{"a semicolon in the payload is kept", "\x1b]0;a;b\x07", "a;b"},

		// A C0 byte that is not a terminator does not end the string. xterm
		// keeps it in the payload; this drops it, and for a multiplexer that
		// is the better answer. A title reaches the outer terminal, so a
		// control byte smuggled into one is a sequence the guest gets to
		// execute in a terminal it does not own.
		{"a C0 byte in the payload is dropped rather than ending the string", "\x1b]0;a\x01b\x07", "ab"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			emu, c := newOSCEmulator(t, 20, 3)
			if _, err := emu.WriteString(tc.in); err != nil {
				t.Fatalf("write: %v", err)
			}
			if c.title != tc.title {
				t.Errorf("title = %q, want %q", c.title, tc.title)
			}
		})
	}
}

// TestConform_OSCAbortAndTruncation covers the strings that never finish.
func TestConform_OSCAbortAndTruncation(t *testing.T) {
	runConform(t, []conformCase{
		{
			// CAN abandons the sequence in progress. Whatever follows is
			// ordinary output again, which is the point: it is how a program
			// gets the parser back to a known state.
			name: "CAN abandons an OSC and the text after it prints",
			cols: 12,
			in:   "A\x1b]0;half\x18B",
			want: "AB",
		}, {
			name: "SUB abandons an OSC and the text after it prints",
			cols: 12,
			in:   "A\x1b]0;half\x1aB",
			want: "AB",
		}, {
			// An OSC that never terminates swallows everything after it. That
			// is correct rather than a bug: the guest said it was still
			// sending a string. What matters is that the emulator survives it.
			name: "an unterminated OSC swallows the text after it",
			cols: 12,
			in:   "A\x1b]0;forever" + strings.Repeat("x", 4096) + "B",
			want: "A",
		}, {
			// A truncated introducer leaves the parser mid-state with the next
			// write arriving behind it.
			name:  "an OSC split across writes still completes",
			cols:  12,
			split: []string{"A\x1b]0;he", "llo\x07B"},
			want:  "AB",
		}, {
			name:  "an OSC split immediately after the introducer still completes",
			cols:  12,
			split: []string{"A\x1b]", "0;hello\x07B"},
			want:  "AB",
		}, {
			name:  "an OSC split inside its terminator still completes",
			cols:  12,
			split: []string{"A\x1b]0;hello\x1b", "\\B"},
			want:  "AB",
		},
	})
}

// TestConform_OSCOversizedPayloads is the memory half.
//
// The emulator accumulates an OSC payload until it is terminated. A guest that
// never terminates one is asking the daemon to hold whatever it sends, so the
// only safe answer is a cap. These do not assert where the cap is, only that
// the emulator keeps working afterwards and that nothing from the payload
// reaches the screen.
func TestConform_OSCOversizedPayloads(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   string
	}{
		{"a megabyte of title", "\x1b]0;" + strings.Repeat("A", 1<<20) + "\x07"},
		{"a megabyte with no terminator, then one", "\x1b]0;" + strings.Repeat("A", 1<<20) + "\x1b\\"},
		{"an OSC number too long to be a number", "\x1b]" + strings.Repeat("9", 4096) + ";x\x07"},
		{"a megabyte of clipboard", "\x1b]52;c;" + strings.Repeat("QUJD", 1<<16) + "\x07"},
		{"a hyperlink with a megabyte of URL", "\x1b]8;;" + strings.Repeat("u", 1<<20) + "\x1b\\"},
		{"an OSC 4 index nobody has", "\x1b]4;99999999999;rgb:00/00/00\x07"},
		{"OSC 52 with something that is not base64", "\x1b]52;c;!!!not base64!!!\x07"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			emu, _ := newOSCEmulator(t, 12, 3)
			if _, err := emu.WriteString(tc.in); err != nil {
				t.Fatalf("write: %v", err)
			}
			// The emulator has to be usable afterwards, which is the whole
			// claim: a payload the guest chose the size of must not leave the
			// parser stuck in a string state forever.
			if _, err := emu.WriteString("\x1b[1;1HOK"); err != nil {
				t.Fatalf("write after the payload: %v", err)
			}
			if got := dumpScreen(emu); got != "OK" {
				t.Errorf("after the payload the screen is %q, want %q", got, "OK")
			}
		})
	}
}
