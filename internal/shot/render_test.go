package shot

import (
	"strings"
	"testing"
)

func put(g *Grid, x, y int, s string, mod func(*Cell)) {
	for _, r := range s {
		if x >= g.Cols {
			return
		}
		c := &g.Cells[y][x]
		c.Cluster = string(r)
		c.Width = 1
		if mod != nil {
			mod(c)
		}
		x++
	}
}

// TestHTMLAndSVGEscapeTheirContent keeps a captured shell line from injecting
// markup into the artifact.
//
// Negative control: swapping xmlEscape for the identity put a live <script>
// into the SVG and failed both cases.
func TestHTMLAndSVGEscapeTheirContent(t *testing.T) {
	g := NewGrid(24, 1, RGB(0xff, 0xff, 0xff), RGB(0, 0, 0))
	put(g, 0, 0, `<script>&"x"`, nil)
	for name, out := range map[string]string{
		"svg":  string(RenderSVG(g, nil, nil)),
		"html": string(RenderHTML(g, nil, nil)),
	} {
		if strings.Contains(out, "<script>") {
			t.Errorf("%s left the markup live", name)
		}
		if !strings.Contains(out, "&lt;script&gt;") || !strings.Contains(out, "&amp;") {
			t.Errorf("%s did not escape the content", name)
		}
	}
}

// TestLinksSurviveAsAnchors checks OSC 8 targets reach the two formats that
// can carry them.
//
// Negative control: dropping the Link arm of writeSVGRun removed the <a> and
// failed the svg case.
func TestLinksSurviveAsAnchors(t *testing.T) {
	g := NewGrid(8, 1, RGB(0xff, 0xff, 0xff), RGB(0, 0, 0))
	put(g, 0, 0, "click", func(c *Cell) { c.Link = "https://example.com/a?b=1&c=2" })
	for name, out := range map[string]string{
		"svg":  string(RenderSVG(g, nil, nil)),
		"html": string(RenderHTML(g, nil, nil)),
	} {
		if !strings.Contains(out, `<a href="https://example.com/a?b=1&amp;c=2">`) {
			t.Errorf("%s carries no escaped anchor:\n%s", name, out)
		}
	}
}
