package vt_test

import (
	"strings"
	"testing"

	"tuios.dev/vt"
)

func TestConform_MouseEncoding(t *testing.T) {
	click := func(x, y int, b vt.MouseButton, mod vt.KeyMod) vt.Mouse {
		return vt.MouseClick{X: x, Y: y, Button: b, Mod: mod}
	}
	release := func(x, y int, b vt.MouseButton, mod vt.KeyMod) vt.Mouse {
		return vt.MouseRelease{X: x, Y: y, Button: b, Mod: mod}
	}
	left := vt.MouseLeft
	x10 := func(b, x, y byte) string { return string([]byte{0x1b, '[', 'M', b, x, y}) }

	for _, tc := range []struct {
		name  string
		modes string
		ev    vt.Mouse
		want  string
	}{
		{"X10 form at the origin", "\x1b[?1000h", click(0, 0, left, 0), "\x1b[M !!"},
		{"X10 form, column 95 is one raw byte", "\x1b[?1000h", click(95, 0, left, 0), x10(' ', 0x80, '!')},
		{"X10 form, the last column it can carry", "\x1b[?1000h", click(222, 3, left, 0), x10(' ', 0xff, '$')},
		{"X10 form, a column past it is not reported", "\x1b[?1000h", click(223, 0, left, 0), ""},
		{"X10 form, column 250 is not reported", "\x1b[?1000h", click(249, 0, left, 0), ""},
		{"X10 form, a release is button 3", "\x1b[?1000h", release(4, 1, left, 0), "\x1b[M#%\""},
		{"X10 form, a release keeps its modifiers", "\x1b[?1000h", release(0, 0, vt.MouseRight, vt.ModShift), "\x1b[M'!!"},
		{"X10 form, the right button with ctrl", "\x1b[?1000h", click(0, 0, vt.MouseRight, vt.ModCtrl), "\x1b[M2!!"},

		{"mode 9 reports a press", "\x1b[?9h", click(1, 1, left, 0), "\x1b[M \"\""},
		{"mode 9 drops the modifiers", "\x1b[?9h", click(1, 1, left, vt.ModShift|vt.ModCtrl), "\x1b[M \"\""},
		{"mode 9 reports no release", "\x1b[?9h", release(1, 1, left, 0), ""},

		{"UTF-8 form at the origin", "\x1b[?1000h\x1b[?1005h", click(0, 0, left, 0), "\x1b[M !!"},
		{"UTF-8 form, column 95 is two bytes", "\x1b[?1000h\x1b[?1005h", click(95, 0, left, 0), "\x1b[M \u0080!"},
		{"UTF-8 form, column 250", "\x1b[?1000h\x1b[?1005h", click(249, 0, left, 0), "\x1b[M Ě!"},
		{"UTF-8 form, the back button is UTF-8", "\x1b[?1000h\x1b[?1005h", click(0, 0, vt.MouseBackward, 0), "\x1b[M\u00a0!!"},
		{"UTF-8 form, forward with ctrl is UTF-8", "\x1b[?1000h\x1b[?1005h", click(0, 0, vt.MouseForward, vt.ModCtrl), "\x1b[M\u00b1!!"},
		{"X10 form, the back button is one raw byte", "\x1b[?1000h", click(0, 0, vt.MouseBackward, 0), x10(0xa0, '!', '!')},
		{"UTF-8 form, a release is button 3", "\x1b[?1000h\x1b[?1005h", release(0, 0, left, 0), "\x1b[M#!!"},

		{"urxvt form", "\x1b[?1000h\x1b[?1015h", click(0, 0, left, 0), "\x1b[32;1;1M"},
		{"urxvt form, column 250", "\x1b[?1000h\x1b[?1015h", click(249, 2, left, 0), "\x1b[32;250;3M"},
		{"urxvt form, a release is button 3", "\x1b[?1000h\x1b[?1015h", release(9, 2, left, 0), "\x1b[35;10;3M"},
		{"urxvt form, modifiers", "\x1b[?1000h\x1b[?1015h", click(0, 0, vt.MouseMiddle, vt.ModAlt), "\x1b[41;1;1M"},

		{"SGR form, column 250", "\x1b[?1000h\x1b[?1006h", click(249, 0, left, 0), "\x1b[<0;250;1M"},
		{"SGR form, a release keeps its button", "\x1b[?1000h\x1b[?1006h", release(249, 0, left, 0), "\x1b[<0;250;1m"},
		{"SGR over UTF-8 and urxvt", "\x1b[?1000h\x1b[?1005h\x1b[?1015h\x1b[?1006h", click(0, 0, left, 0), "\x1b[<0;1;1M"},
		{"urxvt over UTF-8", "\x1b[?1000h\x1b[?1005h\x1b[?1015h", click(0, 0, left, 0), "\x1b[32;1;1M"},
		{"UTF-8 again once urxvt is off", "\x1b[?1000h\x1b[?1005h\x1b[?1015h\x1b[?1015l", click(95, 0, left, 0), "\x1b[M \u0080!"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			emu := vt.NewEmulator(300, 5)
			log := &unhandledLog{}
			emu.SetLogger(log)
			_, _ = emu.WriteString(tc.modes)
			if len(log.lines) > 0 {
				t.Errorf("modes %q were logged as unrecognised: %s", tc.modes, strings.Join(log.lines, "; "))
			}

			if got := emu.EncodeMouseEvent(tc.ev); got != tc.want {
				t.Errorf("EncodeMouseEvent = %q, want %q", got, tc.want)
			}
			emu.SendMouse(tc.ev)
			if got := drainReply(emu); got != tc.want {
				t.Errorf("SendMouse wrote %q, want %q", got, tc.want)
			}
		})
	}
}
