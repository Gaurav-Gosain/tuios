package theme

import (
	"bytes"
	"encoding/base64"
	"strings"
	"testing"
	"unicode/utf8"
)

// FuzzThemeFileExport feeds a theme file, the kind a stranger hands over, to
// the reader, and the theme it reads back through the writer that
// export, share and import-theme use. The written file must parse, and read
// back as the same theme: a writer that drops or mangles a field turns every
// shared theme into a different one.
//
// The base is cleared, so the run does not depend on the registry.
func FuzzThemeFileExport(f *testing.F) {
	for _, seed := range []string{
		`{"id":"x","fg":"#ffffff","bg":"#000000","chrome":{"accent":"red","surface":"#1e1e2e"}}`,
		`{"id":"a\u001b[31mb","display_name":"\u0007bell","dark":false}`,
		`{"chrome":{"border":{"unfocused":"bright_black","copy":"yellow"},"rule":"surface","status":{"busy":"accent"}}}`,
		`{"chrome":{"dock":{"ground":"#181825"},"rail":{"ground":"card"},"dots":{"close":"red","zoom":"green"}}}`,
		`{"chrome":{"accent":"warning","warning":"accent"}}`,
		`{"id":"../../etc/passwd","base":"dracula"}`,
		`[]`, `{"chrome":[1,2]}`, `{"chrome":{"border":"red"}}`,
	} {
		f.Add([]byte(seed))
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > 64<<10 {
			return
		}
		in, err := readThemeFile("fuzz.json", data)
		if err != nil {
			return
		}
		in.Base = ""
		r, err := mergeBase(in)
		if err != nil {
			return
		}
		// A string that is not UTF-8 is replaced on the way through JSON,
		// which is the decoder's doing, not the writer's.
		if !utf8.ValidString(r.tint.ID) || !utf8.ValidString(r.tint.DisplayName) {
			return
		}
		for _, s := range r.chrome {
			if !utf8.ValidString(s) {
				return
			}
		}
		if _, err := buildChrome(r.chrome, r.tint); err != nil {
			return
		}

		doc := fullDoc(r.tint, r.chrome)
		for _, out := range [][]byte{doc.encode(true), doc.encode(false)} {
			back, err := readThemeFile("fuzz.json", out)
			if err != nil {
				t.Fatalf("the written file does not parse: %v\n%s", err, out)
			}
			got, err := mergeBase(back)
			if err != nil {
				t.Fatalf("the written file does not merge: %v\n%s", err, out)
			}
			if got.tint.ID != r.tint.ID || got.tint.DisplayName != r.tint.DisplayName || got.tint.Dark != r.tint.Dark {
				t.Fatalf("id, name or dark changed: %q %q %v, want %q %q %v\n%s",
					got.tint.ID, got.tint.DisplayName, got.tint.Dark, r.tint.ID, r.tint.DisplayName, r.tint.Dark, out)
			}
			want := themeColours(r.tint)
			for i, c := range themeColours(got.tint) {
				if tintHex(*c) != tintHex(*want[i]) {
					t.Fatalf("%s is %s, want %s\n%s", colourKeys[i], tintHex(*c), tintHex(*want[i]), out)
				}
			}
			if got.chrome != r.chrome {
				t.Fatalf("chrome changed:\n got %q\nwant %q\n%s", got.chrome, r.chrome, out)
			}
		}

		// The share string carries the compact file, and nothing in it may
		// need quoting in a chat message: one line, no spaces or controls.
		share := SharePrefix + base64.RawURLEncoding.EncodeToString(bytes.TrimSpace(doc.encode(false)))
		if strings.ContainsFunc(share, func(r rune) bool { return r <= ' ' || r > '~' }) {
			t.Fatalf("share string has a space, a control or a non-ASCII rune: %q", share)
		}
	})
}
