package openedcopy

import (
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"
)

// FuzzPlaceStaysInside is the security boundary of the package: a name and a
// host the far machine chose must give a place inside root, one folder below
// the host's, with a name that is one element.
//
// The ways it could fail, written down first:
//   - a separator of either kind in the name making more than one element;
//   - "", "." or ".." as the name, or a name of dots only;
//   - a host name that climbs out of root;
//   - a control character or invalid UTF-8 reaching the name an opener gets;
//   - a long name cut in the middle of a rune.
func FuzzPlaceStaysInside(f *testing.F) {
	for _, seed := range [][2]string{
		{"build", "notes.md"},
		{"build", `..\..\x`},
		{"..", "../../x"},
		{"a/b", ".."},
		{"", "..."},
		{"build", "a\x00b\nc\x7f"},
		{"build", strings.Repeat("é", 200) + ".pdf"},
		{"build", "CON.txt"},
		{"build", "\xff\xfe"},
	} {
		f.Add(seed[0], seed[1])
	}
	root := filepath.Join(f.TempDir(), "opened")
	f.Fuzz(func(t *testing.T, host, name string) {
		p, err := PlaceFor(root, host, "/far/"+name, name)
		if err != nil {
			t.Fatalf("PlaceFor(%q, %q): %v", host, name, err)
		}
		rel, err := filepath.Rel(root, p.Path)
		if err != nil || !Inside(root, p.Path) {
			t.Fatalf("PlaceFor(%q, %q) = %s, outside %s", host, name, p.Path, root)
		}
		if parts := strings.Split(rel, string(filepath.Separator)); len(parts) != 3 {
			t.Fatalf("PlaceFor(%q, %q) = %s, want host/hash/name", host, name, rel)
		}
		base := filepath.Base(p.Path)
		if strings.Trim(base, ".") == "" || !utf8.ValidString(base) || len(base) > nameMax {
			t.Fatalf("SafeName(%q) = %q", name, base)
		}
		for _, r := range base {
			if r < 0x20 || r == 0x7f || r == '/' || r == '\\' {
				t.Fatalf("SafeName(%q) = %q holds %q", name, base, r)
			}
		}
	})
}
