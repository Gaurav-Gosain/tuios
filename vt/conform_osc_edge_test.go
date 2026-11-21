package vt_test

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

		{"eight-bit ST does not end an OSC in a UTF-8 terminal", "\x1b]0;hello\x9c", ""},

		{"an OSC with no semicolon sets an empty title", "\x1b]0\x07", ""},
		{"an OSC with an empty payload sets an empty title", "\x1b]0;\x07", ""},

		{"a semicolon in the payload is kept", "\x1b]0;a;b\x07", "a;b"},

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
			name: "an unterminated OSC swallows the text after it",
			cols: 12,
			in:   "A\x1b]0;forever" + strings.Repeat("x", 4096) + "B",
			want: "A",
		}, {
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
			if _, err := emu.WriteString("\x1b[1;1HOK"); err != nil {
				t.Fatalf("write after the payload: %v", err)
			}
			if got := dumpScreen(emu); got != "OK" {
				t.Errorf("after the payload the screen is %q, want %q", got, "OK")
			}
		})
	}
}
