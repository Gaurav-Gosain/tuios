package termconfig

import (
	"testing"

	"tuios.dev/swatch"
)

// FuzzParse feeds the parser arbitrary files. A scheme comes from a stranger
// as often as from the person, so whatever the bytes Parse must not panic,
// every Raw key must be one of the twenty, and a palette it returns must have
// every role set.
func FuzzParse(f *testing.F) {
	for _, s := range []string{
		"foreground #ebdbb2\nbackground #282828\ncolor1 #cc241d\n",
		"foreground = #ebdbb2\npalette = 4=#83a598\n",
		"[colors.primary]\nforeground = \"#ebdbb2\"\n[colors.normal]\nmagenta = \"#b16286\"\n",
		"[colors]\nansi = [\"#282828\", \"#cc241d\"]\nbrights = [\"#928374\"]\n",
		"shopping list\nmilk\n",
	} {
		f.Add([]byte(s))
	}
	keys := map[string]bool{KeyForeground: true, KeyBackground: true, KeyCursor: true}
	for i := range 16 {
		keys[ANSIKey(i)] = true
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		p, _, err := Parse(data)
		if err != nil {
			return
		}
		for k := range p.Raw {
			if !keys[k] {
				t.Fatalf("Raw key %q", k)
			}
		}
		for i, c := range p.ANSI {
			if c == nil {
				t.Fatalf("ANSI %d unset", i)
			}
		}
		for _, c := range []any{p.Background, p.Foreground, p.Cursor, p.Accent, p.Border, p.Surface} {
			if c == nil {
				t.Fatal("a role is unset")
			}
		}
		if swatch.Hex(p.Background) == "" {
			t.Fatal("no background")
		}
	})
}
