package main

import (
	"strings"

	"github.com/egoist/mygo/transfer"
	"github.com/egoist/mygo/ui"

	"github.com/Gaurav-Gosain/tuios/internal/diffview"
	"github.com/Gaurav-Gosain/tuios/internal/markdown"

	"github.com/Gaurav-Gosain/tuios/cmd/guios/locales"
)

// Markdown in the cockpit: internal/markdown reads the text into blocks and
// runs, and this draws them with the app's type, the GPUI rewrite's look:
// headings by weight and size, lists with hanging marks, quotes on a bar,
// code on its own ground with its language and a copy button, highlighted
// by internal/diffview, and tables with a ruled header.

// mdBody is the body text's size.
const mdBody = 13.5

// markdownView draws blocks.
func (a *App) markdownView(c *ui.Context, pal *palette, blocks []markdown.Block, mono string) {
	ui.Column(c).Gap(10).AlignItems(ui.Stretch).Children(func() {
		for i := range blocks {
			a.mdBlock(c.Key(i), pal, &blocks[i], mono, 0)
		}
	})
}

// mdRuns are a paragraph's runs as spans.
func mdRuns(pal *palette, src string, mono string, size float32) []ui.Span {
	runs := inlineRuns(src)
	out := make([]ui.Span, 0, len(runs))
	for _, r := range runs {
		s := ui.Span{Text: r.Text, Color: pal.text}
		if r.Style&markdown.Strong != 0 {
			s.Weight = 650
		}
		if r.Style&markdown.Emphasis != 0 {
			s.Italic = true
		}
		if r.Style&markdown.Strike != 0 {
			s.Strikethrough = true
			s.Color = pal.textMuted
		}
		if r.Style&markdown.CodeSpan != 0 {
			s.Font, s.Size = mono, size-1
			s.Background, s.Radius = pal.chip, max(3, (size-1)/4)
		}
		if r.Style&markdown.Link != 0 {
			s.Color, s.Underline = pal.accent, true
			s.DecorationColor = pal.accent.Alpha(0.4)
		}
		if len(out) > 0 {
			prev := out[len(out)-1]
			switch {
			case s.Background.A != 0:
				out = append(out, chipGap(strings.HasSuffix(prev.Text, " "), s))
			case prev.Background.A != 0:
				out = append(out, chipGap(strings.HasPrefix(s.Text, " "), prev))
			}
		}
		out = append(out, s)
	}
	return out
}

// chipGap keeps a code span's chip off the text beside it. MyGo draws a
// span's rounded Background a little wider than its glyphs, by
// min(size*0.22, Radius) on each side, and lays the span out as if it were
// not: the chip covered most of the space before and after it ("in`x`and").
// The gap is an inline object as wide as that overhang, on the chip's side
// of the neighbour. Its rune is a zero width space where the neighbour is a
// space, so the line still breaks there, and a word joiner where it is
// not, so a chip and its punctuation stay on one line. copyThread drops
// both runes again.
func chipGap(space bool, chip ui.Span) ui.Span {
	r := "\u2060"
	if space {
		r = "\u200b"
	}
	return ui.Span{Text: r, ObjectWidth: min(chip.Size*0.22, chip.Radius)}
}

// copyThread is the thread's copy: the selected parts of its paragraphs,
// one a line, as MyGo copies them, without chipGap's runes.
func copyThread(parts []ui.SelectedText) transfer.Data {
	var b strings.Builder
	for i, p := range parts {
		if i > 0 {
			b.WriteByte('\n')
		}
		rs := []rune(p.Text)
		from, to := min(max(p.Start, 0), len(rs)), min(max(p.End, 0), len(rs))
		for _, r := range rs[from:to] {
			if r != '\u200b' && r != '\u2060' {
				b.WriteRune(r)
			}
		}
	}
	return transfer.TextData(b.String())
}

// mdBlock draws one block.
func (a *App) mdBlock(c *ui.Context, pal *palette, b *markdown.Block, mono string, depth int) {
	switch b.Kind {
	case markdown.Paragraph:
		ui.RichText(c, mdRuns(pal, b.Text, mono, mdBody)...).FontSize(mdBody).LineHeight(1.55)
	case markdown.Heading:
		size := map[int]float32{1: 19, 2: 16.5, 3: 14.5}[b.Level]
		if size == 0 {
			size = 13.5
		}
		ui.Column(c).Gap(6).Margin(6, 0, 2, 0).Children(func() {
			ui.RichText(c, mdRuns(pal, b.Text, mono, size)...).FontSize(size).FontWeight(700).LineHeight(1.3)
			if b.Level <= 2 {
				// GitHub's rule under the two top levels.
				ui.Box(c).Height(1).Background(pal.divider)
			}
		})
	case markdown.Item:
		a.mdItem(c, pal, b, mono, depth)
	case markdown.Quote:
		ui.Row(c).AlignItems(ui.Stretch).Gap(12).Children(func() {
			ui.Box(c).Width(3).Shrink(0).Radius(2).Background(pal.textFaint.Alpha(0.5))
			ui.Column(c).Grow(1).MinWidth(0).Gap(8).TextColor(pal.textMuted).Children(func() {
				for i := range b.Children {
					a.mdBlock(c.Key(i), pal, &b.Children[i], mono, depth)
				}
			})
		})
	case markdown.Code:
		a.codeBlock(c, pal, b, mono)
	case markdown.Rule:
		ui.Box(c).Height(1).Background(pal.divider).Margin(4, 0)
	case markdown.Table:
		a.mdTable(c, pal, b, mono)
	}
}

// mdItem is a list item: its mark hangs left of its blocks.
func (a *App) mdItem(c *ui.Context, pal *palette, b *markdown.Block, mono string, depth int) {
	ui.Row(c).Gap(8).AlignItems(ui.Start).Children(func() {
		mark := "•"
		if depth%2 == 1 {
			mark = "◦"
		}
		col := pal.textMuted
		switch {
		case b.Task == 1:
			mark, col = "☐", pal.textMuted
		case b.Task == 2:
			mark, col = "☑", pal.done
		case b.Ordered:
			mark = b.Number
		}
		ui.Text(c, mark).FontSize(mdBody).LineHeight(1.55).TextColor(col).MinWidth(14).TextAlign(ui.End).FontFeatures("tnum").Unselectable()
		ui.Column(c).Grow(1).MinWidth(0).Gap(6).Children(func() {
			for i := range b.Children {
				a.mdBlock(c.Key(i), pal, &b.Children[i], mono, depth+1)
			}
		})
	})
}

// codeBlock is fenced code: its language, a copy button, the lines
// highlighted, scrolled sideways rather than wrapped.
func (a *App) codeBlock(c *ui.Context, pal *palette, b *markdown.Block, mono string) {
	syn := a.syntaxColors(c, pal)
	spans := cachedSpans(b, func() [][]diffview.Span { return diffview.HighlightLang(b.Lang, b.Lines) })
	ui.Column(c).Radius(10).Background(ckCodeBg(pal)).Clip().Children(func() {
		ui.Row(c).Height(28).Padding(0, 6, 0, 12).AlignItems(ui.Center).Unselectable().Children(func() {
			lang := b.Lang
			if lang == "" {
				lang = locales.CockpitCode()
			}
			ui.Text(c, lang).FontSize(11.5).TextColor(pal.textFaint)
			ui.Spacer(c)
			cp := ui.ButtonBase(c.Key("copy")).Size(24, 22).Radius(5).Center().TextColor(pal.textMuted).Label(locales.CockpitCopy()).Tooltip(locales.CockpitCopy())
			if cp.Hovered() {
				cp.Background(pal.rowHover)
			}
			cp.Children(func() { ui.Icon(c, copyIcon).Size(13, 13) })
			if cp.Clicked() {
				c.Invalidate()
				a.copyText = strings.Join(b.Lines, "\n")
			}
		})
		ui.ScrollHorizontal(c).Children(func() {
			ui.Column(c).Padding(0, 14, 10, 14).Children(func() {
				for i, line := range b.Lines {
					var sp []diffview.Span
					if i < len(spans) {
						sp = spans[i]
					}
					ui.RichText(c, detab(codeSpans(line, sp, syn, pal.text))...).Font(mono).FontSize(12.5).LineHeight(1.5).NoWrap()
				}
			})
		})
	})
}

// mdTable is a pipe table: a bold header over a rule, cells aligned as
// written.
func (a *App) mdTable(c *ui.Context, pal *palette, b *markdown.Block, mono string) {
	align := func(i int) ui.Align {
		if i < len(b.Align) {
			switch b.Align[i] {
			case markdown.AlignCenter:
				return ui.Center
			case markdown.AlignRight:
				return ui.End
			}
		}
		return ui.Start
	}
	n := len(b.Header)
	row := func(cells []string, head bool) {
		ui.Row(c).Gap(0).Children(func() {
			for i := range n {
				cell := ""
				if i < len(cells) {
					cell = cells[i]
				}
				t := ui.RichText(c, mdRuns(pal, cell, mono, 13)...).FontSize(13).LineHeight(1.45).Grow(1).Basis(0).MinWidth(0).Padding(6, 10).TextAlign(align(i))
				if head {
					t.FontWeight(650)
				}
			}
		})
	}
	ui.Column(c).Radius(8).Border(1, pal.divider).Clip().Children(func() {
		ui.Column(c).Background(pal.chip.Alpha(0.6)).Children(func() { row(b.Header, true) })
		for ri, r := range b.Rows {
			ui.Box(c.Key(ri)).Height(1).Background(pal.divider)
			row(r, false)
		}
	})
}

// The caches keep what a frame would otherwise work out again on every
// frame of a scroll: a paragraph's runs by its source, and a code block's or
// a hunk's highlighting by the block. They are dropped whole once they grow
// past their bound.
var (
	runsCache  = map[string][]markdown.Run{}
	spansCache = map[any][][]diffview.Span{}
)

const cacheBound = 4096

func inlineRuns(src string) []markdown.Run {
	if r, ok := runsCache[src]; ok {
		return r
	}
	if len(runsCache) > cacheBound {
		clear(runsCache)
	}
	r := markdown.Inline(src)
	runsCache[src] = r
	return r
}

func cachedSpans(key any, f func() [][]diffview.Span) [][]diffview.Span {
	if s, ok := spansCache[key]; ok {
		return s
	}
	if len(spansCache) > cacheBound {
		clear(spansCache)
	}
	s := f()
	spansCache[key] = s
	return s
}
