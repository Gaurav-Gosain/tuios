package composer

import (
	"strings"

	"github.com/charmbracelet/x/ansi"
	"tuios.dev/vt"
)

// tabWidth is how many cells a tab takes on screen. The text keeps the tab.
const tabWidth = 4

// cell is one unit of the buffer: a grapheme cluster, a line break, or a
// paste block. A paste block is one cell, so every motion and every delete
// treats it as one unit and it can never be cut in the middle.
type cell struct {
	// s is the cluster's text: "\n" for a line break, "\t" for a tab, and
	// empty for a block.
	s string
	// w is the cluster's width in cells. A block's width is its label's.
	w int
	// space says the cell separates words: a space, a tab or a line
	// break. It is kept so the layout does not compare strings per cell.
	space bool
	// block is the paste block this cell is, nil for text. The payload
	// travels with the cell, so a block is expanded by identity at send and
	// never by a search for its label, and a block that undo brings back
	// brings its payload with it.
	block *block
}

// isSpace reports whether c separates words.
func (c *cell) isSpace() bool { return c.space }

// bytes is the text c holds, for the bound on the draft. An image is
// bounded on its own (MaxImageBytes, MaxImages).
func (c cell) bytes() int {
	if c.block != nil {
		return len(c.block.text)
	}
	return len(c.s)
}

// weight is the memory c holds, an image's bytes included, for the bound on
// the undo stack.
func (c cell) weight() int {
	if c.block != nil && c.block.img != nil {
		return len(c.block.img.Data)
	}
	return c.bytes()
}

// block is one long paste or one image, held whole and shown as a label.
type block struct {
	// n is the number the label shows: the highest number of its kind in
	// the draft plus one when it came in.
	n int
	// text is the paste as it is sent, empty for an image.
	text string
	// img is the image, nil for a paste.
	img *Image
	// lines and chars are the measures the label shows.
	lines, chars int
	// label is the label as drawn, kept so a frame does not build it again.
	label string
}

// segment appends the cells of text to dst. text is already clean: no
// control characters but the line feed and the tab.
func segment(dst []cell, text string) []cell {
	for text != "" {
		switch text[0] {
		case '\n':
			dst = append(dst, cell{s: "\n", space: true})
			text = text[1:]
			continue
		case '\t':
			dst = append(dst, cell{s: "\t", w: tabWidth, space: true})
			text = text[1:]
			continue
		}
		cluster, w := ansi.FirstGraphemeCluster(text, ansi.GraphemeWidth)
		if cluster == "" {
			break
		}
		dst = append(dst, cell{s: cluster, w: w, space: cluster == " "})
		text = text[len(cluster):]
	}
	return dst
}

// clean makes text safe to hold: line ends become line feeds, and every
// control character but the line feed and the tab goes, ESC above all, so no
// text can end a bracketed paste early when it is typed into the pane.
func clean(text string) string {
	if strings.IndexByte(text, '\r') >= 0 {
		text = strings.ReplaceAll(text, "\r\n", "\n")
		text = strings.ReplaceAll(text, "\r", "\n")
	}
	return vt.SanitizePaste(text)
}
