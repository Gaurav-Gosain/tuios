package flowchart

import (
	"regexp"
	"testing"

	"tuios.dev/sketch/internal/js"
)

// FuzzMatchers holds the hand-written matchers of the lexer and the renderer
// to the regexps they replace, which are pele's.
func FuzzMatchers(f *testing.F) {
	stick := func(pattern string) *regexp.Regexp { return regexp.MustCompile(`^(?:` + pattern + `)`) }
	var (
		reDefault = regexp.MustCompile(`^default[` + js.S + `]?`)
		reEdge    = regexp.MustCompile(`edge-thickness-normal|edge-pattern-solid|flowchart-link`)
		reNewline = stick(`(?:\r?\n)+`)
		reSpace   = stick(`[\t\v\f \x{a0}\x{1680}\x{2000}-\x{200a}\x{2028}\x{2029}\x{202f}\x{205f}\x{3000}\x{feff}]+`)
		reLink    = stick(sp + `*[xo<]?--+[-xo>]` + sp + `*`)
		reStart   = stick(sp + `*[xo<]?--` + sp + `*`)
	)
	at := func(re *regexp.Regexp, s string, p int) int {
		loc := re.FindStringIndex(s[p:])
		if loc == nil {
			return -1
		}
		return p + loc[1]
	}
	for _, s := range []string{"", "default", "default ", "default　x", "defaultx", "default\xff",
		"edge-thickness-normal edge-pattern-solid flowchart-link x", "flowchart-linkflowchart-link", "edge-edge-pattern-solid",
		"\r\n\r\nx", "\n\r", " \t　 x", "\xff ", "-->", "--x", "---", "--", "x--o", "<-->", " 　--> b", "---x", "-- text"} {
		f.Add(s, 0)
	}
	f.Fuzz(func(t *testing.T, s string, p int) {
		if a, b := withoutDefault(s), reDefault.ReplaceAllString(s, ""); a != b {
			t.Fatalf("withoutDefault(%q) = %q, regexp %q", s, a, b)
		}
		if a, b := withoutEdgeClasses(s), reEdge.ReplaceAllString(s, ""); a != b {
			t.Fatalf("withoutEdgeClasses(%q) = %q, regexp %q", s, a, b)
		}
		if p < 0 || p > len(s) {
			return
		}
		for _, m := range []struct {
			name string
			got  func(string, int) int
			re   *regexp.Regexp
		}{{"newlineRun", newlineRun, reNewline}, {"spaceRun", spaceRun, reSpace}, {"linkEnd", linkEnd, reLink}, {"startLinkEnd", startLinkEnd, reStart}} {
			if a, b := m.got(s, p), at(m.re, s, p); a != b {
				t.Fatalf("%s(%q, %d) = %d, regexp %d", m.name, s, p, a, b)
			}
		}
	})
}
