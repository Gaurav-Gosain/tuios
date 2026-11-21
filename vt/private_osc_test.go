package vt

import (
	"slices"
	"strconv"
	"strings"
	"testing"
)

var privateOSCCases = []struct {
	name, in string
	want     []string
}{
	{"a private number", "\x1b]7777;tuios-nvim-navigator;focus;right\x07", []string{"7777 7777;tuios-nvim-navigator;focus;right"}},
	{"a private number with ST", "\x1b]5113;a;b\x1b\\", []string{"5113 5113;a;b"}},
	{"a private number alone", "\x1b]5113\x07", []string{"5113 5113"}},
	{"a number the emulator does not use", "\x1b]22;pointer\x07", []string{"22 22;pointer"}},
	{"no number", "\x1b]foo;bar\x07", nil},
	{"an empty body", "\x1b]\x07", nil},
	{"a number too long for an int", "\x1b]123456789012345678901;x\x07", nil},
	{"a number above int32", "\x1b]2147483648;x\x07", nil},
	{"a number past the largest", "\x1b]1048577;x\x07", nil},
	{"the largest number", "\x1b]1048576;x\x07", []string{"1048576 1048576;x"}},
	{"the title", "\x1b]2;title\x07", nil},
	{"the working directory", "\x1b]7;file://host/tmp\x07", nil},
	{"a hyperlink", "\x1b]8;;https://example.com\x07x\x1b]8;;\x07", nil},
	{"an iTerm2 notification", "\x1b]9;hi\x07", nil},
	{"a urxvt notification", "\x1b]777;notify;a;b\x07", nil},
}

func runPrivateOSCCases(t *testing.T, mk func() Terminal) {
	t.Helper()
	for _, tc := range privateOSCCases {
		t.Run(tc.name, func(t *testing.T) {
			term := mk()
			defer term.Close()
			var got []string
			term.SetCallbacks(Callbacks{PrivateOSC: func(cmd int, data []byte) {
				got = append(got, strconv.Itoa(cmd)+" "+string(data))
			}})
			if _, err := term.Write([]byte(tc.in)); err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(got, tc.want) {
				t.Errorf("%q: PrivateOSC got %q, want %q", tc.in, strings.Join(got, ", "), strings.Join(tc.want, ", "))
			}
		})
	}
}

func TestPrivateOSC(t *testing.T) {
	runPrivateOSCCases(t, func() Terminal { return NewEmulator(20, 3) })
}

// The pure emulator's handlers and interpretedOSC name the same numbers, so
// the ghostty backend, which reads the list, calls the hook for the numbers
// the pure one does.
func TestInterpretedOSCMatchesTheHandlers(t *testing.T) {
	e := NewEmulator(1, 1)
	var registered []int
	for n := range e.oscHandlers {
		registered = append(registered, n)
	}
	slices.Sort(registered)
	listed := slices.Clone(interpretedOSC[:])
	slices.Sort(listed)
	if !slices.Equal(registered, listed) {
		t.Fatalf("handlers %v, interpretedOSC %v", registered, listed)
	}
}
