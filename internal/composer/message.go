package composer

import "strings"

// PartKind is what one part of a message is.
type PartKind uint8

const (
	// PartText is text the person typed, or a short paste kept inline.
	PartText PartKind = iota
	// PartPaste is the text of a paste block.
	PartPaste
	// PartImage is an image block.
	PartImage
)

// Part is one piece of a message, in the order the draft holds it. A host
// that can send parts sends them so the far end can choose how each one
// travels, such as a long paste as a file. One that cannot joins them with
// Text.
type Part struct {
	Kind PartKind
	// Text is the part's text, for a text part and a paste block.
	Text string
	// N is the block's number as its label shows it.
	N int
	// Lines is how many lines a paste block holds.
	Lines int
	// Image is an image block's image.
	Image *Image
}

// Parts is the draft as ordered parts. Text between two blocks is one part.
func (m *Model) Parts() []Part {
	var out []Part
	var run strings.Builder
	flush := func() {
		if run.Len() > 0 {
			out = append(out, Part{Kind: PartText, Text: run.String()})
			run.Reset()
		}
	}
	for _, c := range m.cells {
		if c.block != nil && c.block.img != nil {
			flush()
			out = append(out, Part{Kind: PartImage, N: c.block.n, Image: c.block.img})
			continue
		}
		if c.block != nil {
			flush()
			out = append(out, Part{Kind: PartPaste, Text: c.block.text, N: c.block.n, Lines: c.block.lines})
			continue
		}
		run.WriteString(c.s)
	}
	flush()
	return out
}

// SubmitParts is Submit for a host that sends parts: it removes invisible
// characters the same way, and returns false with a notice when it did.
func (m *Model) SubmitParts() ([]Part, bool) {
	if _, ok := m.Submit(); !ok {
		return nil, false
	}
	return m.Parts(), true
}

// JoinParts is the message the parts make as one text, every paste block's
// text in its place. An image has no text and adds nothing.
func JoinParts(parts []Part) string {
	n := 0
	for _, p := range parts {
		n += len(p.Text)
	}
	var b strings.Builder
	b.Grow(n)
	for _, p := range parts {
		b.WriteString(p.Text)
	}
	return b.String()
}
