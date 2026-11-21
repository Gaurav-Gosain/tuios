package vt_test

import (
	"strings"
	"testing"
	"time"

	"tuios.dev/vt"
)

func reply(t *testing.T, cols, rows int, in string) string {
	t.Helper()
	emu := vt.NewEmulator(cols, rows)
	if _, err := emu.WriteString(in); err != nil {
		t.Fatalf("write %q: %v", in, err)
	}
	got := make(chan string, 1)
	go func() {
		buf := make([]byte, 512)
		n, _ := emu.Read(buf)
		got <- string(buf[:n])
	}()
	select {
	case s := <-got:
		return s
	case <-time.After(2 * time.Second):
		return ""
	}
}

// TestConform_CursorPositionReport pins the shape of a CPR answer.
//
// CPR is CSI Pl ; Pc R, line first and column second (VT510 reference manual,
// CPR). esctest's tests/cpr.py builds its expectation the same way round, and
// every case here is one of its shapes.
func TestConform_CursorPositionReport(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   string
		want string
	}{
		{"home", "\x1b[H\x1b[6n", "\x1b[1;1R"},

		{"row 3 column 4", "\x1b[3;4H\x1b[6n", "\x1b[3;4R"},
		{"row 5 column 2", "\x1b[5;2H\x1b[6n", "\x1b[5;2R"},
		{"row 1 column 10", "\x1b[1;10H\x1b[6n", "\x1b[1;10R"},

		{"after five characters", "hello\x1b[6n", "\x1b[1;6R"},

		{"origin mode reports region-relative", "\x1b[10;20r\x1b[?6h\x1b[1;1H\x1b[6n", "\x1b[1;1R"},
		{"origin mode, third row of the region", "\x1b[10;20r\x1b[?6h\x1b[3;5H\x1b[6n", "\x1b[3;5R"},

		{"absolute when origin mode is off", "\x1b[10;20r\x1b[3;5H\x1b[6n", "\x1b[3;5R"},

		{"DECXCPR", "\x1b[5;2H\x1b[?6n", "\x1b[?5;2;1R"},
		{"DECXCPR in origin mode", "\x1b[10;20r\x1b[?6h\x1b[3;5H\x1b[?6n", "\x1b[?3;5;1R"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := reply(t, 80, 24, tc.in); got != tc.want {
				t.Errorf("reply = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestConform_CursorPositionReportUnderLeftMargin checks the horizontal half of
// origin mode. DECOM makes the left margin column 1, so a report has to
// subtract it the same way the row subtracts the top margin (xterm charproc.c,
// CASE_DSR: `if (xw->flags & ORIGIN) { row -= top_marg; col -= lft_marg; }`).
func TestConform_CursorPositionReportUnderLeftMargin(t *testing.T) {
	const in = "\x1b[?69h\x1b[10;40s\x1b[5;15r\x1b[?6h\x1b[1;1H\x1b[6n"
	if got, want := reply(t, 80, 24, in), "\x1b[1;1R"; got != want {
		t.Errorf("reply = %q, want %q", got, want)
	}
}

// TestConform_DeviceStatusReport covers the non-cursor half of DSR.
func TestConform_DeviceStatusReport(t *testing.T) {
	if got, want := reply(t, 80, 24, "\x1b[5n"), "\x1b[0n"; got != want {
		t.Errorf("DSR 5 replied %q, want %q", got, want)
	}
	if got, want := reply(t, 80, 24, "\x1b[?5n"), "\x1b[?0n"; got != want {
		t.Errorf("DSR ?5 replied %q, want %q", got, want)
	}
}

// TestConform_DeviceAttributes pins the identity this emulator claims.
//
// DA1 decides what a guest is willing to send. A program reading the answer
// picks its feature set from it, so the reply has to be a well-formed DA
// response and it has to keep claiming the features that are actually here.
func TestConform_DeviceAttributes(t *testing.T) {
	da1 := reply(t, 80, 24, "\x1b[c")
	if !strings.HasPrefix(da1, "\x1b[?6") || !strings.HasSuffix(da1, "c") {
		t.Errorf("DA1 replied %q, which is not a VT220-or-later device attributes report", da1)
	}
	for _, want := range []string{";22c"} {
		if !strings.Contains(da1, want) {
			t.Errorf("DA1 replied %q, which does not contain %q", da1, want)
		}
	}
	fields := strings.Split(strings.TrimSuffix(strings.TrimPrefix(da1, "\x1b[?"), "c"), ";")
	switch fields[0] {
	case "62", "63", "64", "65":
	default:
		t.Errorf("DA1 replied %q, whose class %q is not VT220 or later", da1, fields[0])
	}
	implemented := map[string]bool{"4": true, "22": true}
	for _, f := range fields[1:] {
		if !implemented[f] {
			t.Errorf("DA1 replied %q, which claims attribute %s that this emulator does not implement", da1, f)
		}
	}

	da2 := reply(t, 80, 24, "\x1b[>c")
	if !strings.HasPrefix(da2, "\x1b[>") || !strings.HasSuffix(da2, "c") {
		t.Errorf("DA2 replied %q, which is not a secondary device attributes report", da2)
	}
}

// TestConform_DECRQSS covers requests for the current value of a setting.
//
// DECRQSS is DCS $ q <setting> ST, answered with DCS Ps $ r <value> ST where Ps
// is 1 for a setting the terminal reports and 0 for one it does not. The half
// that matters most is that there is always an answer. A guest that asks and
// hears nothing waits out a timeout before deciding, and some do that on every
// start, so silence costs a visible pause where a refusal costs nothing.
func TestConform_DECRQSS(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   string
		want string
	}{
		{
			"the scroll region with none set",
			"\x1bP$qr\x1b\\",
			"\x1bP1$r1;24r\x1b\\",
		}, {
			"the scroll region as the guest set it",
			"\x1b[2;5r\x1bP$qr\x1b\\",
			"\x1bP1$r2;5r\x1b\\",
		}, {
			"the horizontal margins with none set",
			"\x1bP$qs\x1b\\",
			"\x1bP1$r1;80s\x1b\\",
		}, {
			"the horizontal margins as the guest set them",
			"\x1b[?69h\x1b[10;40s\x1bP$qs\x1b\\",
			"\x1bP1$r10;40s\x1b\\",
		}, {
			"a setting this emulator does not report is refused, not ignored",
			"\x1bP$q\"p\x1b\\",
			"\x1bP0$r\x1b\\",
		}, {
			"a setting nobody defines is refused too",
			"\x1bP$qZZ\x1b\\",
			"\x1bP0$r\x1b\\",
		}, {
			"an empty request is refused",
			"\x1bP$q\x1b\\",
			"\x1bP0$r\x1b\\",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := reply(t, 80, 24, tc.in); got != tc.want {
				t.Errorf("reply = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestConform_DECRQM covers the mode-report matrix.
//
// DECRQM answers CSI ? Pm ; Ps $ y where Ps is 0 for a mode the terminal does
// not recognise, 1 set, 2 reset, 3 permanently set and 4 permanently reset
// (VT510 reference manual, DECRPM). esctest exercises the same values in
// tests/decrqm.py. Reporting 1 for a mode nothing acts on is worse than
// reporting 0: the guest changes what it sends on the strength of the answer.
func TestConform_DECRQM(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   string
		want string
	}{
		{"DECAWM is set by default", "\x1b[?7$p", "\x1b[?7;1$y"},
		{"DECAWM reports reset after ?7l", "\x1b[?7l\x1b[?7$p", "\x1b[?7;2$y"},
		{"DECAWM reports set again after ?7h", "\x1b[?7l\x1b[?7h\x1b[?7$p", "\x1b[?7;1$y"},
		{"DECOM is reset by default", "\x1b[?6$p", "\x1b[?6;2$y"},
		{"DECOM reports set after ?6h", "\x1b[?6h\x1b[?6$p", "\x1b[?6;1$y"},
		{"DECTCEM reports reset after ?25l", "\x1b[?25l\x1b[?25$p", "\x1b[?25;2$y"},
		{"the alternate screen reports set while in it", "\x1b[?1049h\x1b[?1049$p", "\x1b[?1049;1$y"},
		{"synchronised output reports set", "\x1b[?2026h\x1b[?2026$p", "\x1b[?2026;1$y"},

		{"mode 47 reports reset by default", "\x1b[?47$p", "\x1b[?47;2$y"},
		{"mode 47 reports set while in it", "\x1b[?47h\x1b[?47$p", "\x1b[?47;1$y"},
		{"UTF-8 mouse reports reset by default", "\x1b[?1005$p", "\x1b[?1005;2$y"},
		{"UTF-8 mouse reports set after ?1005h", "\x1b[?1005h\x1b[?1005$p", "\x1b[?1005;1$y"},
		{"urxvt mouse reports reset by default", "\x1b[?1015$p", "\x1b[?1015;2$y"},
		{"urxvt mouse reports set after ?1015h", "\x1b[?1015h\x1b[?1015$p", "\x1b[?1015;1$y"},
		{"SGR pixel mouse reports reset by default", "\x1b[?1016$p", "\x1b[?1016;2$y"},
		{"SGR pixel mouse reports set after ?1016h", "\x1b[?1016h\x1b[?1016$p", "\x1b[?1016;1$y"},
		{"in-band resize reports reset by default", "\x1b[?2048$p", "\x1b[?2048;2$y"},
		{"in-band resize reports set after ?2048h", "\x1b[?2048h\x1b[?2048$p", "\x1b[48;24;80;480;800t\x1b[?2048;1$y"},

		{"an unknown private mode reports not recognised", "\x1b[?9999$p", "\x1b[?9999;0$y"},

		{"an unknown private mode set first still reports not recognised", "\x1b[?9999h\x1b[?9999$p", "\x1b[?9999;0$y"},
		{"DECCOLM set first reports not recognised", "\x1b[?3h\x1b[?3$p", "\x1b[?3;0$y"},
		{"reverse wrap set first reports not recognised", "\x1b[?45h\x1b[?45$p", "\x1b[?45;0$y"},
		{"an unknown ANSI mode set first reports not recognised", "\x1b[2h\x1b[2$p", "\x1b[2;0$y"},
		{"a soft reset does not make KAM recognised", "\x1b[!p\x1b[2$p", "\x1b[2;0$y"},

		{"grapheme clustering reports permanently set", "\x1b[?2027$p", "\x1b[?2027;3$y"},
		{"grapheme clustering stays permanently set after ?2027l", "\x1b[?2027l\x1b[?2027$p", "\x1b[?2027;3$y"},
		{"grapheme clustering stays permanently set after RIS", "\x1bc\x1b[?2027$p", "\x1b[?2027;3$y"},

		{"ANSI IRM reports reset by default", "\x1b[4$p", "\x1b[4;2$y"},
		{"ANSI IRM reports set after CSI 4 h", "\x1b[4h\x1b[4$p", "\x1b[4;1$y"},
		{"ANSI LNM reports set after CSI 20 h", "\x1b[20h\x1b[20$p", "\x1b[20;1$y"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := reply(t, 80, 24, tc.in); got != tc.want {
				t.Errorf("reply = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestConform_PixelSizeReports pins the three answers a guest reads its pixel
// size from: XTWINOPS 14 (text area), XTWINOPS 16 (one cell) and the in-band
// resize report of mode 2048. They come from one cell size, the host's once it
// is set and the fallback cell before that, and none of them is ever zero.
//
// Issue #506: the 2048 report carried 0 for both pixel sizes. Textual takes
// pixels per cell from it and divides every SGR-pixel mouse report by that,
// so it quit with ZeroDivisionError when the mouse entered its pane.
//
// The ways it could fail, written down before the code:
//   - The 2048 report keeps 0;0 for the pixels.
//   - The reports disagree: 14 from the host cell, 2048 from the fallback.
//   - A cell size that changes after 2048 is on is never told to the guest,
//     which then scales the mouse by the old cell.
//   - A cell size set again to the same value sends a report each time.
func TestConform_PixelSizeReports(t *testing.T) {
	for _, tc := range []struct {
		name   string
		cw, ch int
		in     string
		want   string
	}{
		{"14t with no cell size uses the fallback", 0, 0, "\x1b[14t", "\x1b[4;480;800t"},
		{"16t with no cell size uses the fallback", 0, 0, "\x1b[16t", "\x1b[6;20;10t"},
		{"2048 with no cell size uses the fallback", 0, 0, "\x1b[?2048h", "\x1b[48;24;80;480;800t"},
		{"14t with the host cell", 8, 16, "\x1b[14t", "\x1b[4;384;640t"},
		{"16t with the host cell", 8, 16, "\x1b[16t", "\x1b[6;16;8t"},
		{"2048 with the host cell", 8, 16, "\x1b[?2048h", "\x1b[48;24;80;384;640t"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			emu := vt.NewEmulator(80, 24)
			emu.SetCellSize(tc.cw, tc.ch)
			next := replies(emu)
			if _, err := emu.WriteString(tc.in); err != nil {
				t.Fatal(err)
			}
			if got := next(); got != tc.want {
				t.Errorf("reply = %q, want %q", got, tc.want)
			}
		})
	}

	t.Run("a new cell size is reported once while 2048 is on", func(t *testing.T) {
		emu := vt.NewEmulator(80, 24)
		next := replies(emu)
		if _, err := emu.WriteString("\x1b[?2048h"); err != nil {
			t.Fatal(err)
		}
		if got, want := next(), "\x1b[48;24;80;480;800t"; got != want {
			t.Fatalf("on ?2048h: reply = %q, want %q", got, want)
		}
		emu.SetCellSize(8, 16)
		if got, want := next(), "\x1b[48;24;80;384;640t"; got != want {
			t.Fatalf("after a new cell size: reply = %q, want %q", got, want)
		}
		emu.SetCellSize(8, 16)
		if got := next(); got != "" {
			t.Fatalf("the same cell size again sent %q, want nothing", got)
		}
	})

	t.Run("a zero cell size keeps the cell set before", func(t *testing.T) {
		emu := vt.NewEmulator(80, 24)
		next := replies(emu)
		emu.SetCellSize(8, 16)
		emu.SetCellSize(0, 0)
		if _, err := emu.WriteString("\x1b[16t"); err != nil {
			t.Fatal(err)
		}
		if got, want := next(), "\x1b[6;16;8t"; got != want {
			t.Fatalf("reply = %q, want %q", got, want)
		}
	})

	t.Run("a new cell size is not reported while 2048 is off", func(t *testing.T) {
		emu := vt.NewEmulator(80, 24)
		next := replies(emu)
		emu.SetCellSize(8, 16)
		if got := next(); got != "" {
			t.Fatalf("a new cell size with 2048 off sent %q, want nothing", got)
		}
	})
}

func replies(emu *vt.Emulator) func() string {
	ch := make(chan string, 16)
	go func() {
		buf := make([]byte, 512)
		for {
			n, err := emu.Read(buf)
			if n > 0 {
				ch <- string(buf[:n])
			}
			if err != nil {
				return
			}
		}
	}()
	return func() string {
		select {
		case s := <-ch:
			return s
		case <-time.After(300 * time.Millisecond):
			return ""
		}
	}
}
