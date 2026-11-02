package app

import (
	"strings"
	"time"

	"tuios.dev/sketch"
	sketchtext "tuios.dev/sketch/text"
)

const (
	// quickLookMermaidMax bounds the diagrams drawn in one document, and the source
	// of each.
	quickLookMermaidMax    = 8
	quickLookMermaidBytes  = 16 << 10
	quickLookMermaidCols   = 100
	quickLookMermaidBudget = 2 * time.Second
)

// quickLookDrawMermaid turns the Mermaid fences of a document into plain fences that
// hold the diagram in text. It runs off the loop, in the read. A diagram that fails or
// takes too long stays as its source.
func quickLookDrawMermaid(md string, ascii bool) string {
	if !strings.Contains(md, "```mermaid") {
		return md
	}
	var b strings.Builder
	drawn := 0
	for {
		start := strings.Index(md, "```mermaid\n")
		if start < 0 || (start > 0 && md[start-1] != '\n') {
			break
		}
		end := strings.Index(md[start+11:], "\n```")
		if end < 0 {
			break
		}
		src := md[start+11 : start+11+end]
		b.WriteString(md[:start])
		lines, ok := quickLookMermaidText(src, drawn)
		for i, l := range lines {
			lines[i] = quickLookMermaidGlyphs(l, ascii)
		}
		if ok {
			drawn++
			b.WriteString("```\n")
			b.WriteString(strings.Join(lines, "\n"))
		} else {
			b.WriteString("```text\n")
			b.WriteString(src)
		}
		md = md[start+11+end+1:]
		b.WriteString("\n")
	}
	b.WriteString(md)
	return b.String()
}

// quickLookMermaidGlyphs swaps each glyph the code block would drop for one it keeps,
// so the columns of the drawing stay lined up. The code block drops the geometric
// shapes, which some terminals draw as emoji: the arrow heads are among them. At ASCII
// the box lines go too.
func quickLookMermaidGlyphs(line string, ascii bool) string {
	return strings.Map(func(r rune) rune {
		if printableRune(r, ascii) {
			return r
		}
		switch r {
		case '►', '▶', '▸':
			return '>'
		case '◄', '◀', '◂':
			return '<'
		case '▲', '▴':
			return '^'
		case '▼', '▾':
			return 'v'
		case '─', '━', '┄', '┈', '╌':
			return '-'
		case '│', '┃', '┆', '┊', '╎':
			return '|'
		}
		if r >= 0x2500 && r <= 0x257f {
			return '+'
		}
		return '*'
	}, line)
}

// quickLookMermaidText draws one diagram, under the time limit.
func quickLookMermaidText(src string, drawn int) ([]string, bool) {
	if drawn >= quickLookMermaidMax || len(src) > quickLookMermaidBytes {
		return nil, false
	}
	done := make(chan []string, 1)
	go func() {
		d, err := sketch.Parse(src, sketch.ParseOptions{})
		if err != nil {
			done <- nil
			return
		}
		lines, err := sketchtext.Draw(d, sketchtext.Options{Cols: quickLookMermaidCols})
		if err != nil {
			done <- nil
			return
		}
		done <- lines
	}()
	select {
	case lines := <-done:
		return lines, lines != nil
	case <-time.After(quickLookMermaidBudget):
		return nil, false
	}
}

// quickLookHasDetails reports whether the Markdown on the panel has closed sections.
func quickLookHasDetails(ql *quickLookState) bool {
	return ql.res != nil && ql.res.kind == "markdown" && !ql.mdOpen && strings.Contains(ql.res.md, "<details")
}

// QuickLookOpenDetails opens the closed sections of a Markdown file.
func (m *OS) QuickLookOpenDetails() {
	if ql := &m.quickLook; ql.open && quickLookHasDetails(ql) {
		ql.mdOpen = true
	}
}
