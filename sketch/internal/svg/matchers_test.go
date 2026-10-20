package svg

import (
	"regexp"
	"testing"

	"tuios.dev/sketch/internal/js"
)

// FuzzClassNames holds ClassNames to pele's regexp: each run of [^\w-]
// becomes a space, and the result is trimmed.
func FuzzClassNames(f *testing.F) {
	re := regexp.MustCompile(`[^\w-]+`)
	for _, s := range []string{"", " ", "a", "a b", " a", "a ", "a  b", "a-b_c", "é", "a b", "\xff", "a,b"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		want := js.Trim(re.ReplaceAllString(s, " "))
		if want != "" {
			want = " " + want
		}
		if got := ClassNames(s); got != want {
			t.Fatalf("ClassNames(%q) = %q, regexp %q", s, got, want)
		}
	})
}
