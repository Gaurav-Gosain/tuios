package config

import (
	"fmt"
	"testing"
)

// The layout is read from what /usr/bin/defaults prints, which no test on
// Linux runs. A misread is silent: a UK Mac read as US types no £ again
// (issue #746), and a US Mac read as UK loses Option+N. The samples are the
// two forms defaults prints for com.apple.HIToolbox.
func TestMacInputSourceIsUS(t *testing.T) {
	const selected = `(
        {
        InputSourceKind = "Keyboard Layout";
        "KeyboardLayout ID" = %s;
        "KeyboardLayout Name" = %s;
    },
        {
        "Bundle ID" = "com.apple.CharacterPaletteIM";
        InputSourceKind = "Non Keyboard Input Method";
    }
)
`
	for _, tc := range []struct {
		raw  string
		want bool
	}{
		{"com.apple.keylayout.US\n", true},
		{"com.apple.keylayout.ABC\n", true},
		{"com.apple.keylayout.British\n", false},
		{"com.apple.keylayout.French\n", false},
		{"com.apple.keylayout.USInternational-PC\n", false},
		{"", false},
		{fmt.Sprintf(selected, "0", `"U.S."`), true},
		{fmt.Sprintf(selected, "252", "ABC"), true},
		{fmt.Sprintf(selected, "2", "British"), false},
		{fmt.Sprintf(selected, "0", `"U.S."`) + fmt.Sprintf(selected, "2", "British"), false},
		{`( { "Bundle ID" = "com.apple.inputmethod.Kotoeri"; } )`, false},
	} {
		if got := inputSourceIsUS(tc.raw); got != tc.want {
			t.Errorf("inputSourceIsUS(%q) = %v, want %v", tc.raw, got, tc.want)
		}
	}
}
