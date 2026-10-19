package sketch

import (
	"image/color"
	"math"
	"strings"
	"testing"
)

// TestThemeStaysInert gives a theme the font lists and radii a library
// caller might pass from user input. The root style must keep the output
// inert and must not hold a value CSS cannot read.
func TestThemeStaysInert(t *testing.T) {
	refused := map[string]bool{
		"x;background:url(//evil.example/x)": true,
		"Inter\";color:red":                  true,
		"a{}":                                true,
		"Arial, sans-serif; --pele-bg: red":  true,
	}
	fonts := []string{}
	for f := range refused {
		fonts = append(fonts, f)
	}
	for _, font := range append(fonts, PAYLOADS...) {
		for _, radius := range []float64{math.Inf(1), math.Inf(-1), math.NaN(), -3, 0} {
			th := &Theme{Font: font, FontMono: font, Radius: radius, Bg: color.NRGBA{0x80, 0xff, 0x10, 0x40}}
			out, err := Render("flowchart LR\n  A --> B\n", RenderOptions{Theme: th})
			if err != nil {
				t.Fatalf("font %q radius %v: %v", font, radius, err)
			}
			// inertProblem refuses the --pele-* tokens a theme sets, so the
			// root style is checked here on its own.
			if err := assertWellFormed(out.Data); err != nil {
				t.Fatalf("font %q radius %v: %v", font, radius, err)
			}
			for _, el := range xmlElements(out.Data) {
				for _, a := range el.attrs {
					if a[0] == "style" && reUnsafeStyle.MatchString(a[1]) {
						t.Fatalf("font %q radius %v: style=%q", font, radius, a[1])
					}
				}
			}
			bads := []string{"--pele-radius:", "Inf", "NaN"}
			if refused[font] {
				bads = append(bads, "--pele-font:", "--pele-font-mono:")
			}
			for _, bad := range bads {
				if strings.Contains(out.Data, bad) {
					t.Fatalf("font %q radius %v: output holds %q", font, radius, bad)
				}
			}
		}
	}
	out, _ := Render("flowchart LR\n  A --> B\n", RenderOptions{Theme: &Theme{Font: `"Iosevka Term", monospace`, Radius: 6}})
	if !strings.Contains(out.Data, `--pele-font:&quot;Iosevka Term&quot;, monospace;`) || !strings.Contains(out.Data, "--pele-radius:6px;") {
		t.Fatalf("a valid font list and radius were dropped:\n%s", out.Data)
	}
}
