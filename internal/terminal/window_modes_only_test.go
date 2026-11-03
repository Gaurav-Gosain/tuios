package terminal

import (
	"math/rand"
	"strings"
	"testing"

	"tuios.dev/vt"
)

// What the bridge's model reads from a pane's emulator must come out the same
// whether the emulator sees the whole stream or only what modesOnly passes.
// The ways it could go wrong: a title, cwd, notification, clipboard or bell
// lost; the alternate screen state wrong; a sequence split between reads lost;
// an OSC-looking string inside a DCS, APC or text let through; an OSC that
// ESC cancels let through.
type seen struct {
	titles, cwds, notes, clips []string
	alt                        []bool
	bells                      int
}

func record(t vt.Terminal) *seen {
	s := &seen{}
	t.SetCallbacks(vt.Callbacks{
		Title:            func(v string) { s.titles = append(s.titles, v) },
		WorkingDirectory: func(v string) { s.cwds = append(s.cwds, v) },
		Notify:           func(a, b string) { s.notes = append(s.notes, a+"|"+b) },
		ClipboardSet:     func(_ string, v string) { s.clips = append(s.clips, v) },
		AltScreen:        func(on bool) { s.alt = append(s.alt, on) },
		Bell:             func() { s.bells++ },
	})
	return s
}

const stream = "plain text \x1b[1;31mred\x1b[0m\r\n" +
	"\x1b]0;first title\x07" +
	"\x1b]2;second title\x1b\\" +
	"\x1b]7;file://host/home/me/src\x07" +
	"\x1bP+q\x1b]2;not a title\x07\x1b\\" + // inside a DCS
	"\x1b_Ga=T;AAAA\x1b\\" + // kitty APC
	"\x1b]9;build done\x07" +
	"\x1b]777;notify;Agent;needs you\x07" +
	"\x1b]52;c;aGVsbG8=\x07" +
	"\x1b]2;cancelled\x1b[2Jafter\r\n" + // an OSC that ESC cancels
	"\x07" + // a bell
	"\x1b[?1049h\x1b[Hfull screen\x1b[?25l\x1b[?1049l" +
	"\x1b[?1047h\x1b[?1047l" +
	"text with ] and ; and \x1b[5;5H moves\r\n" +
	"\x1b]2;last title\x07"

func feed(t vt.Terminal, f *modesOnly, data string, cuts []int) {
	prev := 0
	for _, c := range append(cuts, len(data)) {
		part := []byte(data[prev:c])
		if f != nil {
			part = f.filter(part)
		}
		_, _ = t.Write(part)
		prev = c
	}
}

func TestModesOnlyKeepsWhatTheModelReads(t *testing.T) {
	full := vt.New(80, 24)
	want := record(full)
	feed(full, nil, stream, nil)
	if len(want.titles) == 0 || len(want.alt) == 0 {
		t.Fatalf("the full stream fired no callbacks: %+v", want)
	}
	r := rand.New(rand.NewSource(1))
	for round := range 200 {
		// The stream cut at random places, as reads cut it.
		var cuts []int
		for c := 1 + r.Intn(9); c < len(stream); c += 1 + r.Intn(17) {
			cuts = append(cuts, c)
		}
		if round == 0 {
			cuts = nil
		}
		if round == 1 {
			cuts = cuts[:0]
			for c := 1; c < len(stream); c++ {
				cuts = append(cuts, c)
			}
		}
		e := vt.New(80, 24)
		got := record(e)
		feed(e, &modesOnly{}, stream, cuts)
		if strings.Join(got.titles, ",") != strings.Join(want.titles, ",") ||
			strings.Join(got.cwds, ",") != strings.Join(want.cwds, ",") ||
			strings.Join(got.notes, ",") != strings.Join(want.notes, ",") ||
			strings.Join(got.clips, ",") != strings.Join(want.clips, ",") ||
			len(got.alt) != len(want.alt) || got.bells != want.bells {
			t.Fatalf("round %d, cuts %v:\n got  %+v\n want %+v", round, cuts, got, want)
		}
		for i := range got.alt {
			if got.alt[i] != want.alt[i] {
				t.Fatalf("round %d: alt screen %v, want %v", round, got.alt, want.alt)
			}
		}
		if e.IsCursorHidden() != full.IsCursorHidden() {
			t.Fatalf("round %d: cursor hidden %v, want %v", round, e.IsCursorHidden(), full.IsCursorHidden())
		}
	}
}

func TestModesOnlyDropsText(t *testing.T) {
	f := &modesOnly{}
	out := f.filter([]byte(strings.Repeat("a line of output with \x1b[32mcolour\x1b[0m\r\n", 1000)))
	if len(out) != 0 {
		t.Fatalf("passed %d bytes of text: %q", len(out), out[:min(len(out), 40)])
	}
}
