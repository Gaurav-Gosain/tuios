package text

import (
	"regexp"
	"testing"
)

// FuzzHasMarkup holds hasMarkup to pele's regexp.
func FuzzHasMarkup(f *testing.F) {
	re := regexp.MustCompile(`[<\\\n*_]|fa[bklrs]?:fa-`)
	for _, s := range []string{"", "fa:fa-x", "fab:fa-x", "faa:fa-", "ffa:fa-", "fa", "fafa:fa-", "a<b", "x_y", "fak:fa", "fas:fa-"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		if got, want := hasMarkup(s), re.MatchString(s); got != want {
			t.Fatalf("hasMarkup(%q) = %v, regexp %v", s, got, want)
		}
	})
}
