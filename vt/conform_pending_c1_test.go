//go:build !ghostty

package vt_test

// The pending input of a sequence an 8-bit C1 control started. The pure Go
// parser honours CSI, OSC, DCS, SOS, PM and APC in their 8-bit forms outside a
// string; libghostty does not, so these cases run on the pure backend only.
//
// Ways this can go wrong:
//   - the start is taken from the last ESC, which is before the C1 control,
//     so a replay paints the text between them twice;
//   - a byte with a C1 control's value inside a UTF-8 character or inside a
//     string payload is taken for the start, so the replay loses the
//     introducer and the rest prints as text;
//   - a control carried out inside a C1 CSI is kept and carried out twice.

import (
	"testing"

	"tuios.dev/vt"
)

func TestSnapshotState_PendingInputC1(t *testing.T) {
	cases := []struct {
		name   string
		writes []string
		want   string
	}{
		{name: "a C1 CSI cut", writes: []string{"ab\x9b1;3"}, want: "\x9b1;3"},
		{name: "a C1 CSI after an ESC sequence", writes: []string{"\x1b[1mfoo\x9b3"}, want: "\x9b3"},
		{name: "a C1 CSI continued in the next write", writes: []string{"ab\x9b1", ";2"}, want: "\x9b1;2"},
		{name: "a control inside a C1 CSI is not kept", writes: []string{"\x9b1\n;2"}, want: "\x9b1;2"},
		{name: "a C1 OSC after a character holding 9b bytes", writes: []string{"\x1b[0m\xe2\x9b\x9b\x9d2;t"}, want: "\x9d2;t"},
		{name: "an OSC whose payload holds a 9d byte", writes: []string{"x\x1b]2;\xe2\x9d\x84 ti"}, want: "\x1b]2;\xe2\x9d\x84 ti"},
		{name: "a C1 CSI after a rune cut across writes", writes: []string{"a\xf0\x9f", "\x98\x9b\x9b4"}, want: "\x9b4"},
		{name: "a C1 CSI that ended", writes: []string{"\x1b[1m\x9b2mfoo"}, want: ""},
		{name: "a C1 CSI right after an ESC", writes: []string{"1\x1b\x9b(\t"}, want: "\x9b("},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			term := vt.NewEmulator(20, 4)
			t.Cleanup(func() { _ = term.Close() })
			for _, w := range tc.writes {
				feed(t, term, w)
			}
			if got := string(term.PendingInput()); got != tc.want {
				t.Errorf("pending input %q, want %q", got, tc.want)
			}
		})
	}
}
