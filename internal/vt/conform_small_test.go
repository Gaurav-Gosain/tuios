package vt_test

// Conformance for the title stack (XTWINOPS 22 and 23) and for SGR 53 and 55,
// overline, which this emulator recognises as unimplemented.

import (
	"strings"
	"testing"

	uv "github.com/charmbracelet/ultraviolet"

	"github.com/Gaurav-Gosain/tuios/internal/vt"
)

// TestConform_Overline pins that overline is reported unhandled rather than
// dropped quietly: the cell style has no attribute to hold it. The rest of
// the SGR still applies.
func TestConform_Overline(t *testing.T) {
	runConform(t, []conformCase{
		{
			name:      "SGR 53 is unhandled, and the rest of the SGR applies",
			in:        "\x1b[1;53;31mX",
			want:      "X",
			cells:     []cellWant{{x: 0, y: 0, content: "X", attrs: ptr(uint8(uv.AttrBold)), fg: indexed(1)}},
			unhandled: true,
		}, {
			name:      "SGR 55 is unhandled too",
			in:        "\x1b[55mX",
			want:      "X",
			unhandled: true,
		},
	})
}

// titleLog records the titles and icon names the emulator announces.
type titleLog struct{ titles, icons []string }

func newTitleEmulator(t *testing.T) (*vt.Emulator, *titleLog, *unhandledLog) {
	t.Helper()
	emu := vt.NewEmulator(10, 3)
	tl := &titleLog{}
	emu.SetCallbacks(vt.Callbacks{
		Title:    func(s string) { tl.titles = append(tl.titles, s) },
		IconName: func(s string) { tl.icons = append(tl.icons, s) },
	})
	log := &unhandledLog{}
	emu.SetLogger(log)
	return emu, tl, log
}

// TestConform_TitleStack covers XTWINOPS 22 (push) and 23 (pop), per xterm's
// ctlseqs: the second parameter is 0 for both the icon name and the window
// title, 1 for the icon name and 2 for the window title. A program such as
// vim pushes the title at start, sets its own, and pops it when it exits.
func TestConform_TitleStack(t *testing.T) {
	for _, tc := range []struct {
		name          string
		in            string
		titles, icons string // what was announced, comma separated
	}{
		{"push and pop both", "\x1b]0;shell\x07\x1b[22;0t\x1b]0;vim\x07\x1b[23;0t",
			"shell,vim,shell", "shell,vim,shell"},
		{"no parameter means both", "\x1b]0;shell\x07\x1b[22t\x1b]0;vim\x07\x1b[23t",
			"shell,vim,shell", "shell,vim,shell"},
		{"the window title alone", "\x1b]0;shell\x07\x1b[22;2t\x1b]0;vim\x07\x1b[23;0t",
			"shell,vim,shell", "shell,vim"},
		{"the icon name alone", "\x1b]0;shell\x07\x1b[22;1t\x1b]0;vim\x07\x1b[23;0t",
			"shell,vim", "shell,vim,shell"},
		{"pop only the title of a pair", "\x1b]0;shell\x07\x1b[22;0t\x1b]0;vim\x07\x1b[23;2t",
			"shell,vim,shell", "shell,vim"},
		{"nested", "\x1b]2;a\x07\x1b[22;2t\x1b]2;b\x07\x1b[22;2t\x1b]2;c\x07\x1b[23;2t\x1b[23;2t",
			"a,b,c,b,a", ""},
		{"a pop with nothing pushed changes nothing", "\x1b]2;a\x07\x1b[23;0t", "a", ""},
		{"RIS empties the stack", "\x1b]2;a\x07\x1b[22;2t\x1bc\x1b]2;b\x07\x1b[23;2t", "a,b", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			emu, tl, log := newTitleEmulator(t)
			_, _ = emu.WriteString(tc.in)
			if len(log.lines) > 0 {
				t.Errorf("logged as unrecognised: %s", strings.Join(log.lines, "; "))
			}
			if got := strings.Join(tl.titles, ","); got != tc.titles {
				t.Errorf("titles announced = %q, want %q", got, tc.titles)
			}
			if got := strings.Join(tl.icons, ","); got != tc.icons {
				t.Errorf("icon names announced = %q, want %q", got, tc.icons)
			}
		})
	}

	t.Run("the stack holds ten entries", func(t *testing.T) {
		emu, tl, _ := newTitleEmulator(t)
		var in strings.Builder
		for i := range 11 {
			in.WriteString("\x1b]2;" + string(rune('a'+i)) + "\x07\x1b[22;2t")
		}
		in.WriteString("\x1b]2;end\x07")
		in.WriteString(strings.Repeat("\x1b[23;2t", 11))
		_, _ = emu.WriteString(in.String())
		// The eleventh push dropped "a", so the pops end at "b".
		if got, want := strings.Join(tl.titles[12:], ","), "k,j,i,h,g,f,e,d,c,b"; got != want {
			t.Errorf("titles popped = %q, want %q", got, want)
		}
	})

	t.Run("an unknown selector is unhandled", func(t *testing.T) {
		emu, _, log := newTitleEmulator(t)
		_, _ = emu.WriteString("\x1b[22;3t")
		if len(log.lines) != 1 {
			t.Errorf("CSI 22 ; 3 t logged %v, want it reported unrecognised", log.lines)
		}
	})
}
