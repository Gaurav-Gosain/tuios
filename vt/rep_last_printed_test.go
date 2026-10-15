package vt

import "testing"

// TestLastPrintedSkipsControls: what LastPrinted reports is a character REP
// can repeat. A control is not one, whether it arrives as a C0 byte or as a
// C1 control encoded in UTF-8 (U+0080 to U+009F), so it leaves the
// character before it. The backend the build selects is the one tested.
func TestLastPrintedSkipsControls(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"a printed character", "ab", "b"},
		{"a C0 control after it", "ab\r\n\x07", "b"},
		{"NEL as UTF-8", "ab\u0085", "b"},
		{"CSI as UTF-8", "ab\u009b", "b"},
		{"the first character past C1", "ab ", " "},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			term := New(20, 4)
			defer func() { _ = term.Close() }()
			if _, err := term.Write([]byte(c.in)); err != nil {
				t.Fatal(err)
			}
			if got := term.LastPrinted(); got != c.want {
				t.Errorf("LastPrinted after %q: %q, want %q", c.in, got, c.want)
			}
		})
	}
}
