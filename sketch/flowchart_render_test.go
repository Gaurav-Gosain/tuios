// Ported from pele tests/render/flowchart.test.ts at 8fad23bd51783e4df1204e177a7fb0d2a30dcd13:
// the cases v1.8.0 added. The rest of that file is covered by the goldens.

package sketch

import (
	"strings"
	"testing"
)

func renderSVG(t *testing.T, src string, opts RenderOptions) string {
	t.Helper()
	out, err := Render(src, opts)
	if err != nil {
		t.Fatalf("%q: %v", src, err)
	}
	return out.Data
}

func TestFlowchartLabelMarkup(t *testing.T) {
	for _, tag := range []string{"b", "strong", "i", "em"} {
		t.Run("keeps <"+tag+"> formatting across explicit line breaks", func(t *testing.T) {
			for _, br := range []string{"<br/>", "<br>", "<BR />", `\n`, "\n"} {
				source := "flowchart LR\n  A[\"<" + tag + ">First" + br + "Second</" + tag + "> plain" + br + "Third\"]"
				reopened := "flowchart LR\n  A[\"<" + tag + ">First</" + tag + ">" + br + "<" + tag + ">Second</" + tag + "> plain" + br + "Third\"]"
				if renderSVG(t, source, RenderOptions{}) != renderSVG(t, reopened, RenderOptions{}) {
					t.Errorf("%s across %q", tag, br)
				}
			}
		})
	}

	t.Run("keeps nested HTML formatting across breaks without changing other labels or the base style", func(t *testing.T) {
		source := "flowchart LR\n  A[\"<b><i>First<br/><br/>Second</i> bold</b> plain\"] --> B[plain]"
		reopened := "flowchart LR\n  A[\"<b><i>First</i></b><br/><br/><b><i>Second</i> bold</b> plain\"] --> B[plain]"
		for _, suffix := range []string{"", "\n  style A font-weight:bold"} {
			if renderSVG(t, source+suffix, RenderOptions{}) != renderSVG(t, reopened+suffix, RenderOptions{}) {
				t.Errorf("suffix %q", suffix)
			}
		}
		if renderSVG(t, "flowchart LR\n A[\"<b>First<br/>Second\"] --> B[plain]", RenderOptions{}) !=
			renderSVG(t, "flowchart LR\n A[\"<b>First</b><br/><b>Second</b>\"] --> B[plain]", RenderOptions{}) {
			t.Error("an unclosed <b> does not carry across the break")
		}
	})

	t.Run("decodes HTML5 aliases such as lcub and verbar in labels", func(t *testing.T) {
		svg := renderSVG(t, "flowchart LR\n  A[\"GET /plans/&lcub;id&rcub;\"] --> B[\"items#lsqb;0#rsqb; #verbar; none\"]", RenderOptions{})
		for _, want := range []string{"GET /plans/{id}", "items[0] | none"} {
			if !strings.Contains(svg, want) {
				t.Errorf("no %q in %s", want, svg)
			}
		}
	})

	t.Run("decodes each entity code once", func(t *testing.T) {
		svg := renderSVG(t, "flowchart LR\n  A[\"x #amp;lt; y #38;gt; z\"]", RenderOptions{})
		if !strings.Contains(svg, "x &amp;lt; y &amp;gt; z") {
			t.Errorf("got %s", svg)
		}
	})
}
