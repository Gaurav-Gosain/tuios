package tml

import (
	"strings"
	"unicode/utf8"
)

// Limits bound what one document may hold. Over a limit is a problem with
// the code limit, and the limit named in its message.
type Limits struct {
	DocBytes   int // the source text or JSON
	Nodes      int // components and text runs, before each expands
	Depth      int // nesting of nodes
	StateBytes int // the state store as compact JSON
	Each       int // nodes that each and list expand to, in all
	TableRows  int // rows of one table
	PatchBytes int // one JSON Patch
	Images     int // image nodes in a document, after each expands
	ImageBytes int // one decoded data: image
	Problems   int // problems reported in one pass; at least 1
}

// DefaultLimits are the limits for a document from a program, a plugin or an
// agent.
func DefaultLimits() Limits {
	return Limits{
		DocBytes:   256 << 10,
		Nodes:      5000,
		Depth:      32,
		StateBytes: 1 << 20,
		Each:       2000,
		TableRows:  50000,
		PatchBytes: 64 << 10,
		Images:     4,
		ImageBytes: 4 << 20,
		Problems:   20,
	}
}

// PersonLimits are the limits for a document the person opens from their own
// command line: a larger document, the rest the same.
func PersonLimits() Limits {
	l := DefaultLimits()
	l.DocBytes = 1 << 20
	return l
}

// ExprSteps is the expression step budget of one surface for one frame.
const ExprSteps = 10000

// Expansion is the budget a renderer spends as it expands each and list
// against the live state. The validator checks the state a document arrives
// with, but a patch can grow an array later, so the renderer checks again
// with this.
type Expansion struct {
	left int
}

// NewExpansion returns a budget of lim.Each expanded nodes.
func NewExpansion(lim Limits) *Expansion { return &Expansion{left: lim.Each} }

// Take spends n nodes. It reports false, and spends nothing, when fewer
// than n are left. The renderer then stops expanding and shows the rest as
// cut off.
func (e *Expansion) Take(n int) bool {
	if n < 0 || n > e.left {
		return false
	}
	e.left -= n
	return true
}

// Left returns the nodes still allowed.
func (e *Expansion) Left() int { return e.left }

// Sanitize removes what must never reach a terminal from text a document
// carries:
//
//   - C0 and C1 control characters other than tab and newline, and DEL
//   - the bidirectional marks, embeddings, overrides and isolates (U+061C,
//     U+200E, U+200F, U+202A to U+202E, U+2066 to U+2069)
//   - the line and paragraph separators U+2028 and U+2029
//   - invisible characters: U+200B, U+2060 to U+2064, U+180E, U+FEFF, and
//     the tag characters U+E0000 to U+E007F
//
// It keeps the zero-width joiner and non-joiner (U+200D, U+200C), which
// emoji sequences and several scripts need. Invalid UTF-8 becomes U+FFFD.
// The parsers apply it to every text run and attribute, the store to every
// string and key it holds, and a template to every value it returns.
func Sanitize(s string) string {
	clean := true
	for i := 0; i < len(s); {
		c := s[i]
		if c >= 0x20 && c < 0x7f {
			i++
			continue
		}
		r, size := utf8.DecodeRuneInString(s[i:])
		if (r == utf8.RuneError && size == 1) || unsafeRune(r) {
			clean = false
			break
		}
		i += size
	}
	if clean {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		i += size
		switch {
		case r == utf8.RuneError && size == 1:
			b.WriteRune(utf8.RuneError)
		case unsafeRune(r):
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

func unsafeRune(r rune) bool {
	switch {
	case r == '\t' || r == '\n':
		return false
	case r < 0x20, r == 0x7f, r >= 0x80 && r <= 0x9f:
		return true
	case r == 0x061c, r == 0x180e, r == 0x200b, r == 0x200e, r == 0x200f, r == 0xfeff:
		return true
	case r >= 0x2028 && r <= 0x202e, r >= 0x2060 && r <= 0x2064, r >= 0x2066 && r <= 0x2069:
		return true
	case r >= 0xe0000 && r <= 0xe007f:
		return true
	}
	return false
}

// SanitizeValue applies Sanitize to every string and object key in a JSON
// value, as encoding/json decodes it into any. It returns v itself when v is
// already clean, so a clean store is not copied.
func SanitizeValue(v any) any {
	out, _ := sanitizeValue(v)
	return out
}

func sanitizeValue(v any) (any, bool) {
	switch x := v.(type) {
	case string:
		c := Sanitize(x)
		return c, c != x
	case []any:
		var out []any
		for i, e := range x {
			c, changed := sanitizeValue(e)
			if changed && out == nil {
				out = make([]any, len(x))
				copy(out, x[:i])
			}
			if out != nil {
				out[i] = c
			}
		}
		if out == nil {
			return x, false
		}
		return out, true
	case map[string]any:
		dirty := false
		for k, e := range x {
			if _, changed := sanitizeValue(e); changed || Sanitize(k) != k {
				dirty = true
				break
			}
		}
		if !dirty {
			return x, false
		}
		out := make(map[string]any, len(x))
		for k, e := range x {
			out[Sanitize(k)], _ = sanitizeValue(e)
		}
		return out, true
	}
	return v, false
}
