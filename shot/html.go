package shot

import (
	"encoding/base64"
	"fmt"
	"html"
	"strings"
)

// HTML backend: one self-contained file with inline styles. Selectable
// text, links preserved, opens on any machine with a monospace fallback and
// no fonts installed. The frame carries over as CSS on two nested divs.

// RenderHTML renders the grid and frame to a standalone HTML document.
func RenderHTML(g *Grid, f *Frame) []byte {
	var b strings.Builder
	b.Grow(1 << 15)
	b.WriteString("<!doctype html>\n<meta charset=\"utf-8\">\n")

	family := "monospace"
	if f != nil && f.FontFamily != "" {
		family = f.FontFamily
	}
	b.WriteString("<style>\n")
	if f != nil && f.EmbedFont && len(f.Font.Data) > 0 {
		fmt.Fprintf(&b, "@font-face{font-family:%q;src:url(data:font/ttf;base64,%s);}\n",
			svgPrimaryFamily(family), base64.StdEncoding.EncodeToString(f.Font.Data))
	}
	fmt.Fprintf(&b, "pre{margin:0;font-family:%s;font-size:14px;line-height:1.3;}\n", family)
	b.WriteString("a{color:inherit;}\n</style>\n")

	framed := f != nil && f.Mode != FrameNone
	if framed {
		wash := "transparent"
		if !f.Transparent {
			wash = fmt.Sprintf("linear-gradient(135deg,%s,%s)", Hex(f.WashStart), Hex(f.WashEnd))
		}
		shadow := ""
		if f.Shadow {
			shadow = "box-shadow:0 8px 24px rgba(0,0,0,0.35);"
		}
		fmt.Fprintf(&b, `<div style="display:inline-block;padding:%dpx;background:%s">`+"\n", f.Padding, wash)
		fmt.Fprintf(&b, `<div style="background:%s;border-radius:%dpx;padding:14px 16px;%s">`+"\n",
			Hex(g.BG), f.Radius, shadow)
		if f.Mode == FrameWindow {
			writeHTMLTitleBar(&b, g, f)
		}
	}

	images := len(g.Images) > 0
	if images {
		// The images are placed on the cell grid in ch and em, so the box they
		// sit in carries the same font as the text.
		fmt.Fprintf(&b, `<div style="position:relative;display:inline-block;vertical-align:top;overflow:hidden;font-family:%s;font-size:14px;background:%s">`+"\n",
			family, Hex(g.BG))
		writeHTMLImages(&b, g, true)
		fmt.Fprintf(&b, `<pre style="position:relative;z-index:1;color:%s">`, Hex(g.FG))
	} else {
		fmt.Fprintf(&b, `<pre style="color:%s;background:%s">`, Hex(g.FG), Hex(g.BG))
	}
	for y := 0; y < g.Rows; y++ {
		writeHTMLRow(&b, g, y)
		b.WriteByte('\n')
	}
	b.WriteString("</pre>\n")
	if images {
		writeHTMLImages(&b, g, false)
		b.WriteString("</div>\n")
	}
	if framed {
		b.WriteString("</div>\n</div>\n")
	}
	return []byte(b.String())
}

func writeHTMLTitleBar(b *strings.Builder, g *Grid, f *Frame) {
	dot := func(c string) {
		fmt.Fprintf(b, `<span style="display:inline-block;width:11px;height:11px;border-radius:6px;background:%s;margin-right:7px"></span>`, c)
	}
	b.WriteString(`<div style="display:flex;align-items:center;margin:0 0 12px 0">`)
	switch f.Controls {
	case ControlsMacOS:
		for _, c := range [3]string{"#ff5f57", "#febc2e", "#28c840"} {
			dot(c)
		}
	case ControlsGlyphs:
		for _, m := range []string{f.CloseGlyph, f.MinimizeGlyph, f.MaximizeGlyph} {
			if m != "" {
				fmt.Fprintf(b, `<span style="color:%s;margin-right:7px">%s</span>`, Hex(f.Accent), html.EscapeString(m))
			}
		}
	case ControlsNone:
	}
	if f.Title != "" {
		fmt.Fprintf(b, `<span style="flex:1;text-align:center;color:%s;font-family:%s;font-size:13px">%s</span>`,
			Hex(mix(g.FG, g.BG, 0.3)), f.FontFamily, html.EscapeString(f.Title))
		// Balance the controls so the title centers on the card.
		b.WriteString(`<span style="width:47px"></span>`)
	}
	b.WriteString("</div>\n")
}

func writeHTMLRow(b *strings.Builder, g *Grid, y int) {
	var line strings.Builder
	for x := 0; x < g.Cols; x++ {
		line.Reset()
		c := g.Cells[y][x]
		if c.Width == 0 {
			continue
		}
		// Merge forward while the style holds.
		start := c
		var text strings.Builder
		text.WriteString(displayCluster(c))
		for x+1 < g.Cols {
			n := g.Cells[y][x+1]
			if n.Width == 0 {
				x++
				continue
			}
			if !n.SameStyle(start) {
				break
			}
			text.WriteString(displayCluster(n))
			x++
		}
		writeHTMLSpan(b, g, start, text.String())
	}
}

func writeHTMLSpan(b *strings.Builder, g *Grid, c Cell, text string) {
	var css []string
	if c.FG != g.FG {
		css = append(css, "color:"+Hex(c.FG))
	}
	if !c.BGDefault {
		css = append(css, "background:"+Hex(c.BG))
	}
	if c.Bold {
		css = append(css, "font-weight:bold")
	}
	if c.Italic {
		css = append(css, "font-style:italic")
	}
	if c.Faint {
		css = append(css, "opacity:0.55")
	}
	var deco []string
	if c.Strike {
		deco = append(deco, "line-through")
	}
	switch c.Underline {
	case UnderlineSingle:
		deco = append(deco, "underline")
	case UnderlineDouble:
		deco = append(deco, "underline double")
	case UnderlineCurly:
		deco = append(deco, "underline wavy")
	case UnderlineDotted:
		deco = append(deco, "underline dotted")
	case UnderlineDashed:
		deco = append(deco, "underline dashed")
	}
	if len(deco) > 0 {
		css = append(css, "text-decoration:"+strings.Join(deco, " "))
	}
	escaped := html.EscapeString(text)
	open, close := "", ""
	if c.Link != "" {
		open = fmt.Sprintf(`<a href="%s">`, html.EscapeString(c.Link))
		close = "</a>"
	}
	if len(css) == 0 {
		b.WriteString(open + escaped + close)
		return
	}
	fmt.Fprintf(b, `%s<span style="%s">%s</span>%s`, open, strings.Join(css, ";"), escaped, close)
}

// writeHTMLImages writes the images of one layer as absolutely placed <img>
// elements with PNG data URIs. A row is 1.3em, the line height of the <pre>,
// and a column is 1ch.
//
// An image below the text sits under the whole <pre>, so a cell with a
// background of its own covers it. PNG and SVG draw such an image over the
// cell backgrounds, as kitty does. HTML has no layer between a span's
// background and its text.
func writeHTMLImages(b *strings.Builder, g *Grid, below bool) {
	z := 2
	if below {
		z = 0
	}
	for _, img := range g.Images {
		if img.Below != below {
			continue
		}
		c, ok := img.cut(g.Cols, g.Rows)
		if !ok {
			continue
		}
		uri, ok := imageDataURI(img.Pixels, c.src)
		if !ok {
			continue
		}
		fmt.Fprintf(b, `<img alt="" src="%s" style="position:absolute;z-index:%d;left:%sch;top:%sem;width:%sch;height:%sem">`+"\n",
			uri, z, fnum(c.col0), fnum(c.row0*1.3), fnum(c.col1-c.col0), fnum((c.row1-c.row0)*1.3))
	}
}
