package invisible

import (
	"strings"
	"testing"
)

// classes holds one sample of every kind of character Rune must report.
var classes = []struct {
	name string
	r    rune
}{
	{"soft hyphen", 0x00ad},
	{"arabic letter mark", 0x061c},
	{"mongolian vowel separator", 0x180e},
	{"zero width space", 0x200b},
	{"zero width non-joiner", 0x200c},
	{"zero width joiner", 0x200d},
	{"left-to-right mark", 0x200e},
	{"right-to-left mark", 0x200f},
	{"line separator", 0x2028},
	{"paragraph separator", 0x2029},
	{"left-to-right embedding", 0x202a},
	{"right-to-left override", 0x202e},
	{"word joiner", 0x2060},
	{"invisible plus", 0x2064},
	{"unassigned U+2065", 0x2065},
	{"first strong isolate", 0x2068},
	{"pop directional isolate", 0x2069},
	{"nominal digit shapes", 0x206f},
	{"variation selector 1", 0xfe00},
	{"variation selector 16", 0xfe0f},
	{"byte order mark", 0xfeff},
	{"interlinear annotation anchor", 0xfff9},
	{"interlinear annotation terminator", 0xfffb},
	{"musical symbol begin beam", 0x1d173},
	{"musical symbol end phrase", 0x1d17a},
	{"tag U+E0000", 0xe0000},
	{"language tag", 0xe0001},
	{"tag latin small a", 0xe0061},
	{"cancel tag", 0xe007f},
	{"variation selector 17", 0xe0100},
	{"variation selector 256", 0xe01ef},
}

// legit is text that must come through Strip unchanged.
var legit = []struct {
	name, s string
}{
	{"ascii", "hello, world"},
	{"cjk", "\u4f60\u597d\u4e16\u754c \u65e5\u672c\u8a9e"},
	{"hangul", "\ud55c\uad6d\uc5b4"},
	{"combining marks", "e\u0301 n\u0303 a\u0308"},
	{"hebrew", "\u05e9\u05dc\u05d5\u05dd"},
	{"arabic", "\u0645\u0631\u062d\u0628\u0627"},
	{"devanagari", "\u0928\u092e\u0938\u094d\u0924\u0947"},
	{"emoji", "\U0001f600 \U0001f680"},
	{"emoji zwj family", "\U0001f468\u200d\U0001f469\u200d\U0001f467"},
	{"emoji zwj profession with skin tone", "\U0001f469\U0001f3fd\u200d\U0001f4bb"},
	{"emoji zwj heart on fire", "\u2764\u200d\U0001f525"},
	{"flag", "\U0001f1ef\U0001f1f5"},
	{"tab and newline", "a\tb\nc"},
}

func TestRuneReportsEveryClass(t *testing.T) {
	for _, c := range classes {
		if !Rune(c.r) {
			t.Errorf("%s (U+%04X): Rune = false, want true", c.name, c.r)
		}
	}
}

func TestRuneKeepsVisibleText(t *testing.T) {
	for _, c := range legit {
		for _, r := range c.s {
			if r == zwj {
				continue
			}
			if Rune(r) {
				t.Errorf("%s: Rune(U+%04X) = true, want false", c.name, r)
			}
		}
	}
	// Mongolian free variation selectors are part of how Mongolian is
	// written, and they are not in either variation selector block.
	if Rune(0x180b) {
		t.Errorf("Rune(U+180B) = true, want false")
	}
}

func TestStripRemovesEveryClass(t *testing.T) {
	for _, c := range classes {
		in := "a" + string(c.r) + "b"
		got := Strip(in)
		want := "ab"
		if c.r == 0x2028 || c.r == 0x2029 {
			want = "a\nb"
		}
		if got != want {
			t.Errorf("%s: Strip(%q) = %q, want %q", c.name, in, got, want)
		}
	}
}

func TestStripKeepsLegitText(t *testing.T) {
	for _, c := range legit {
		if got := Strip(c.s); got != c.s {
			t.Errorf("%s: Strip(%q) = %q, want it unchanged", c.name, c.s, got)
		}
	}
}

func TestStrip(t *testing.T) {
	cases := []struct {
		name, in, want string
	}{
		{"joiner between letters", "ad\u200dmin", "admin"},
		{"joiner after emoji before letter", "\U0001f600\u200dx", "\U0001f600x"},
		{"joiner at the end", "\U0001f600\u200d", "\U0001f600"},
		{"joiner at the start", "\u200d\U0001f600", "\U0001f600"},
		{"two joiners between emoji", "\U0001f600\u200d\u200d\U0001f600", "\U0001f600\u200d\U0001f600"},
		{"rainbow flag loses its selector, keeps its joiner", "\U0001f3f3\ufe0f\u200d\U0001f308", "\U0001f3f3\u200d\U0001f308"},
		{"text hidden in variation selectors", "hi\ufe01\ufe02\U000e0101\U000e0142!", "hi!"},
		{"text hidden in tags", "ok\U000e0001\U000e0069\U000e0067\U000e006e\U000e007f", "ok"},
		{"bidi override", "invoice\u202etxt.exe", "invoicetxt.exe"},
		{"isolates", "\u2066abc\u2069", "abc"},
		{"line separator", "one\u2028two\u2029three", "one\ntwo\nthree"},
		{"soft hyphen", "pass\u00adword", "password"},
		{"empty", "", ""},
	}
	for _, c := range cases {
		if got := Strip(c.in); got != c.want {
			t.Errorf("%s: Strip(%q) = %q, want %q", c.name, c.in, got, c.want)
		}
	}
}

// TestStripLeavesNoInvisibleRune checks that nothing Rune reports survives
// Strip, other than a joiner between two emoji.
func TestStripLeavesNoInvisibleRune(t *testing.T) {
	var b strings.Builder
	for _, c := range classes {
		b.WriteString("x")
		b.WriteRune(c.r)
		b.WriteString("\U0001f600")
		b.WriteRune(c.r)
	}
	for _, r := range Strip(b.String()) {
		if Rune(r) {
			t.Errorf("Strip left U+%04X", r)
		}
	}
}
