package vt_test

import (
	"image/color"
	"testing"
	"time"

	"tuios.dev/vt"
)

type reportSet struct {
	fg, bg color.Color
	pal    [16]color.Color
}

var (
	tokyoNight = reportSet{
		fg:  color.RGBA{R: 0xc0, G: 0xca, B: 0xf5, A: 0xff},
		bg:  color.RGBA{R: 0x1a, G: 0x1b, B: 0x26, A: 0xff},
		pal: [16]color.Color{4: color.RGBA{R: 0x7a, G: 0xa2, B: 0xf7, A: 0xff}},
	}
	dracula = reportSet{
		fg:  color.RGBA{R: 0xf8, G: 0xf8, B: 0xf2, A: 0xff},
		bg:  color.RGBA{R: 0x28, G: 0x2a, B: 0x36, A: 0xff},
		pal: [16]color.Color{4: color.RGBA{R: 0xbd, G: 0x93, B: 0xf9, A: 0xff}},
	}
	latte = reportSet{
		fg:  color.RGBA{R: 0x4c, G: 0x4f, B: 0x69, A: 0xff},
		bg:  color.RGBA{R: 0xef, G: 0xf1, B: 0xf5, A: 0xff},
		pal: [16]color.Color{4: color.RGBA{R: 0x1e, G: 0x66, B: 0xf5, A: 0xff}},
	}
)

func (r reportSet) with(change func(*reportSet)) reportSet {
	change(&r)
	return r
}

func guestReplies(term vt.Terminal) func() string {
	out := make(chan string, 1)
	go func() {
		var all []byte
		buf := make([]byte, 256)
		for {
			n, err := term.Read(buf)
			all = append(all, buf[:n]...)
			if err != nil {
				break
			}
		}
		out <- string(all)
	}()
	return func() string {
		time.Sleep(100 * time.Millisecond)
		_ = term.Close()
		return <-out
	}
}

func TestConform_ColorSchemeNotice(t *testing.T) {
	for _, tc := range []struct {
		name     string
		guest    string
		from, to reportSet
		want     string
	}{
		{"dark to light", "\x1b[?2031h", tokyoNight, latte, "\x1b[?997;2n"},
		{"light to dark", "\x1b[?2031h", latte, tokyoNight, "\x1b[?997;1n"},
		{"dark to dark sends one notice", "\x1b[?2031h", tokyoNight, dracula, "\x1b[?997;1n"},
		{"palette only", "\x1b[?2031h", tokyoNight, tokyoNight.with(func(r *reportSet) { r.pal = dracula.pal }), "\x1b[?997;1n"},
		{"text colour only", "\x1b[?2031h", tokyoNight, tokyoNight.with(func(r *reportSet) { r.fg = dracula.fg }), "\x1b[?997;1n"},
		{"a report taken away", "\x1b[?2031h", latte, reportSet{}, "\x1b[?997;1n"},
		{"the same push again", "\x1b[?2031h", tokyoNight, tokyoNight, ""},
		{"an equal colour of another type", "\x1b[?2031h", tokyoNight,
			tokyoNight.with(func(r *reportSet) { r.bg = color.NRGBA{R: 0x1a, G: 0x1b, B: 0x26, A: 0xff} }), ""},
		{"no mode 2031", "", tokyoNight, dracula, ""},
		{"mode 2031 reset", "\x1b[?2031h\x1b[?2031l", tokyoNight, dracula, ""},
		{"Ps reads the guest's own background", "\x1b[?2031h\x1b]11;rgb:ff/ff/ff\x1b\\", tokyoNight, dracula, "\x1b[?997;2n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			term := vt.New(20, 4)
			term.SetReportColors(tc.from.fg, tc.from.bg, tc.from.pal)
			if tc.guest != "" {
				if _, err := term.Write([]byte(tc.guest)); err != nil {
					t.Fatal(err)
				}
			}
			done := guestReplies(term)
			term.SetReportColors(tc.to.fg, tc.to.bg, tc.to.pal)
			if got := done(); got != tc.want {
				t.Errorf("the emulator wrote %q, want %q", got, tc.want)
			}
		})
	}
	t.Logf("ran on the %s backend", vt.Backend)
}

// SetThemeColors changes what the screen is drawn in and sends no notice,
// even for a switch between dark and light. Only SetReportColors notifies.
func TestConform_ColorSchemeThemeColorsAreSilent(t *testing.T) {
	term := vt.New(20, 4)
	term.SetThemeColors(tokyoNight.fg, tokyoNight.bg, nil, tokyoNight.pal)
	if _, err := term.Write([]byte("\x1b[?2031h")); err != nil {
		t.Fatal(err)
	}
	done := guestReplies(term)
	term.SetThemeColors(latte.fg, latte.bg, nil, latte.pal)
	if got := done(); got != "" {
		t.Errorf("SetThemeColors wrote %q, want nothing", got)
	}
	t.Logf("ran on the %s backend", vt.Backend)
}

func TestConform_ColorSchemeDSR(t *testing.T) {
	lightBg := color.RGBA{R: 0xfa, G: 0xfa, B: 0xfa, A: 0xff}
	for _, tc := range []struct {
		name  string
		setup func(vt.Terminal)
		in    string
		want  string
	}{
		{"nothing set reads as dark", func(vt.Terminal) {}, "\x1b[?996n", "\x1b[?997;1n"},
		{"a dark report", func(term vt.Terminal) { term.SetReportColors(tokyoNight.fg, tokyoNight.bg, tokyoNight.pal) }, "\x1b[?996n", "\x1b[?997;1n"},
		{"a light report", func(term vt.Terminal) { term.SetReportColors(latte.fg, latte.bg, latte.pal) }, "\x1b[?996n", "\x1b[?997;2n"},
		{"a light theme and no report", func(term vt.Terminal) {
			term.SetThemeColors(color.Black, lightBg, nil, [16]color.Color{})
		}, "\x1b[?996n", "\x1b[?997;2n"},
		{"answered with mode 2031 set too", func(term vt.Terminal) { term.SetReportColors(latte.fg, latte.bg, latte.pal) }, "\x1b[?2031h\x1b[?996n", "\x1b[?997;2n"},
		{"the guest's own background", func(term vt.Terminal) { term.SetReportColors(latte.fg, latte.bg, latte.pal) }, "\x1b]11;rgb:00/00/00\x1b\\\x1b[?996n", "\x1b[?997;1n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			term := vt.New(20, 4)
			tc.setup(term)
			done := guestReplies(term)
			if _, err := term.Write([]byte(tc.in)); err != nil {
				t.Fatal(err)
			}
			if got := done(); got != tc.want {
				t.Errorf("%q answered %q, want %q", tc.in, got, tc.want)
			}
		})
	}
	t.Logf("ran on the %s backend", vt.Backend)
}
