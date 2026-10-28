// Ported from pele tests/render/sankey.test.ts at 8aff183be57021b533f319a29341a529be934b08:
// the parse cases v1.9.0 added. The drawings are covered by testdata/extra/sankey-1-9.json.

package sketch

import (
	"errors"
	"strings"
	"testing"
)

func TestSankeyUnicodeNames(t *testing.T) {
	for _, keyword := range []string{"sankey", "sankey-beta"} {
		for _, pair := range [][2]string{{"Доход", "Расходы"}, {"Café", "München"}, {"収入", "支出"}, {"☀️", "🏠"}, {"Café", "Cafè"}} {
			for _, quoted := range []bool{false, true} {
				field := func(name string) string {
					if quoted {
						return `"` + name + `"`
					}
					return name
				}
				svg := renderSVG(t, keyword+"\n"+field(pair[0])+","+field(pair[1])+",100\n", RenderOptions{})
				for _, name := range pair {
					if !strings.Contains(svg, `data-id="`+name+`"`) || !strings.Contains(svg, ">"+name+" <tspan") {
						t.Errorf("%s, quoted %v: %q is not drawn", keyword, quoted, name)
					}
				}
			}
		}

		svg := renderSVG(t, keyword+"\n\"Доход, \"\"чистый\"\"\",Café,2\nCafé,収入,1\n", RenderOptions{})
		if !strings.Contains(svg, ">Доход, &quot;чистый&quot; <tspan") || strings.Count(svg, `class="pele-sankey-node" data-id="Café"`) != 1 {
			t.Errorf("%s: CSV escaping or node identity is lost", keyword)
		}

		fails := func(src string) *Error {
			_, err := Parse(src, ParseOptions{})
			var pe *Error
			if !errors.As(err, &pe) {
				t.Errorf("%q: got %v, want a PeleError", src, err)
				return nil
			}
			return pe
		}
		for _, row := range []string{"Доход,Расходы", "Доход,Расходы,1,2", "\"Доход,Расходы,1", "\"Доход\"x,Расходы,1", "Доход,,1"} {
			fails(keyword + "\n" + row + "\n")
		}
		// pele also rejects lone surrogates, which a Go string cannot hold.
		for _, c := range []string{"\x00", "\x01", "\t", "\x1f", "\x7f", "\u0080", "\u009f", "￾", "￿"} {
			for _, name := range []string{"a" + c + "b", `"a` + c + `b"`} {
				fails(keyword + "\n" + name + ",c,1\n")
			}
		}
		for _, entity := range []string{"#65534;", "#65535;", "&#65534;", "&#65535;", "#1;"} {
			for _, name := range []string{"a" + entity, `"a` + entity + `"`} {
				for _, row := range []string{name + ",b,1", "b," + name + ",1"} {
					if pe := fails(keyword + "\n" + row + "\n"); pe != nil && !strings.Contains(pe.Message, "Invalid XML character in node name") {
						t.Errorf("%q: %s", row, pe.Message)
					}
				}
			}
		}
	}
}
