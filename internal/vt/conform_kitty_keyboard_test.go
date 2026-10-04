package vt_test

// Conformance for the kitty keyboard protocol's flag stacks.
//
// The protocol (https://sw.kovidgoyal.net/kitty/keyboard-protocol/) gives the
// main and the alternate screen independent stacks, and asks the terminal to
// bound them, evicting the oldest entry when a push finds the stack full.
// kitty and ghostty hold eight entries. Popping more entries than there are
// leaves no flags set.

import (
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/vt"
)

func TestConform_KittyKeyboardStacks(t *testing.T) {
	push := func(flags ...string) string {
		var b strings.Builder
		for _, f := range flags {
			b.WriteString("\x1b[>" + f + "u")
		}
		return b.String()
	}
	const q = "\x1b[?u"
	ans := func(flags ...string) string {
		var b strings.Builder
		for _, f := range flags {
			b.WriteString("\x1b[?" + f + "u")
		}
		return b.String()
	}

	for _, tc := range []struct {
		name string
		in   string
		want string
	}{
		{"nothing pushed", q, ans("0")},
		{"push and query", push("1") + q, ans("1")},
		{"push, push, pop", push("1", "3") + "\x1b[<u" + q, ans("1")},
		{"popping more than was pushed leaves no flags", push("1") + "\x1b[<5u" + q, ans("0")},
		// kitty clears the entry a set wrote to when it is popped, so a pop
		// after a set with nothing pushed is back to no flags.
		{"a set with nothing pushed, then a pop", "\x1b[=5;1u" + q + "\x1b[<u" + q, ans("5", "0")},

		// One stack per screen.
		{"the alternate screen starts with no flags", push("1") + "\x1b[?1049h" + q, ans("0")},
		{"the main screen's flags come back", push("1") + "\x1b[?1049h" + q + "\x1b[?1049l" + q, ans("0", "1")},
		{"flags pushed on the alternate screen stay there",
			"\x1b[?1049h" + push("5") + q + "\x1b[?1049l" + q, ans("5", "0")},
		{"a set on the alternate screen leaves the main screen alone",
			push("1") + "\x1b[?1049h\x1b[=31;1u" + q + "\x1b[?1049l" + q, ans("31", "1")},
		{"mode 1047 switches stacks", push("1") + "\x1b[?1047h" + q + "\x1b[?1047l" + q, ans("0", "1")},
		{"mode 47 switches stacks", push("1") + "\x1b[?47h" + q + "\x1b[?47l" + q, ans("0", "1")},
		// A program that exits without popping does not hand its flags to
		// the next one to enter the alternate screen.
		{"flags left on the alternate screen are gone on the next entry",
			"\x1b[?1049h" + push("5") + "\x1b[?1049l\x1b[?1049h" + q, ans("0")},
		{"entering the alternate screen twice keeps its stack",
			"\x1b[?1049h" + push("5") + "\x1b[?1049h" + q, ans("5")},

		// Bounded at eight entries, the oldest evicted.
		{"eight pushes all fit", push("1", "2", "3", "4", "5", "6", "7", "8") + "\x1b[<7u" + q, ans("1")},
		{"a ninth push evicts the oldest",
			push("1", "2", "3", "4", "5", "6", "7", "8", "9") + q + "\x1b[<7u" + q + "\x1b[<u" + q,
			ans("9", "2", "0")},
		{"a hundred pushes keep the newest eight",
			strings.Repeat(push("4"), 92) + push("1", "2", "3", "4", "5", "6", "7", "8") + "\x1b[<7u" + q,
			ans("1")},

		// RIS empties both stacks.
		{"RIS on the main screen", push("1") + "\x1bc" + q, ans("0")},
		{"RIS on the alternate screen", push("1") + "\x1b[?1049h" + push("3") + "\x1bc" + q + "\x1b[?1049l" + q,
			ans("0", "0")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := replyChecked(t, tc.in); got != tc.want {
				t.Errorf("reply = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestConform_KittyKeyboardFlagsFollowTheScreen checks the value the input
// path encodes keys with, which is read through KittyKeyboardFlags rather
// than through a query.
func TestConform_KittyKeyboardFlagsFollowTheScreen(t *testing.T) {
	emu := vt.NewEmulator(10, 3)
	step := func(in string, want int) {
		t.Helper()
		_, _ = emu.WriteString(in)
		if got := emu.KittyKeyboardFlags(); got != want {
			t.Errorf("after %q, KittyKeyboardFlags() = %d, want %d", in, got, want)
		}
	}
	step("\x1b[>1u", 1)
	step("\x1b[?1049h", 0)
	step("\x1b[>31u", 31)
	step("\x1b[?1049l", 1)
	step("\x1b[?1049h", 0)
}

// TestKittyKeyboardStackRestoresOntoItsScreen pins the reattach path: the
// daemon saves the stack of the screen in use, and a restore puts it back on
// that screen, after the screen itself, without touching the other one.
func TestKittyKeyboardStackRestoresOntoItsScreen(t *testing.T) {
	src := vt.NewEmulator(10, 3)
	_, _ = src.WriteString("\x1b[>1u\x1b[?1049h\x1b[>5u\x1b[>7u")
	saved := src.KittyKeyboardStack()
	if want := []int{0, 5, 7}; !equalInts(saved, want) {
		t.Fatalf("saved stack = %v, want %v", saved, want)
	}

	dst := vt.NewEmulator(10, 3)
	dst.RestoreAltScreenMode(true)
	dst.RestoreKittyKeyboardState(saved)
	if got := dst.KittyKeyboardFlags(); got != 7 {
		t.Errorf("restored flags = %d, want 7", got)
	}
	dst.RestoreAltScreenMode(false)
	if got := dst.KittyKeyboardFlags(); got != 0 {
		t.Errorf("main screen flags after restoring onto the alternate screen = %d, want 0", got)
	}

	// A stack longer than the limit keeps its newest entries.
	long := []int{0, 1, 2, 3, 4, 5, 6, 7, 8, 9}
	dst.RestoreKittyKeyboardState(long)
	if got, want := dst.KittyKeyboardStack(), long[2:]; !equalInts(got, want) {
		t.Errorf("an over-long restore kept %v, want %v", got, want)
	}
}

func equalInts(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
