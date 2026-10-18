package tml

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"unicode/utf8"
)

// Limits bound what one document may hold. Over a limit is a problem with
// the code limit, and the limit named in its message.
//
// Every function takes a *Limits. A nil pointer means DefaultLimits, and a
// zero field means the default for that field, so a caller sets only what
// it changes.
type Limits struct {
	DocBytes   int // the source text or JSON
	Nodes      int // components and text runs, before each expands
	Depth      int // nesting of nodes
	StateBytes int // the state store as compact JSON
	Each       int // nodes that each and list expand to, in all
	Rows       int // rows and data points of tables, lists, trees and charts, after each expands
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
		Rows:       50000,
		PatchBytes: 64 << 10,
		Images:     4,
		ImageBytes: 4 << 20,
		Problems:   20,
	}
}

// resolve returns the limits to use: the defaults for a nil pointer and for
// each zero field.
func (l *Limits) resolve() Limits {
	d := DefaultLimits()
	if l == nil {
		return d
	}
	out := *l
	for _, f := range []struct{ v, def *int }{
		{&out.DocBytes, &d.DocBytes}, {&out.Nodes, &d.Nodes}, {&out.Depth, &d.Depth},
		{&out.StateBytes, &d.StateBytes}, {&out.Each, &d.Each}, {&out.Rows, &d.Rows},
		{&out.PatchBytes, &d.PatchBytes}, {&out.Images, &d.Images},
		{&out.ImageBytes, &d.ImageBytes}, {&out.Problems, &d.Problems},
	} {
		if *f.v <= 0 {
			*f.v = *f.def
		}
	}
	return out
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
// already clean, so a clean store is not copied. Each value is visited once.
//
// Two keys that become one after Sanitize keep the value of the key that
// sorts first, so the result does not depend on map order. A value that is
// not a JSON value becomes null. NewStore and the parsers refuse both cases
// instead; see SanitizeJSON.
func SanitizeValue(v any) any {
	out, _, _ := sanitizeValue(v, false)
	return out
}

// SanitizeJSON is SanitizeValue for data that enters a store or a tree. It
// refuses two keys that become one after Sanitize, a number that is not
// finite, and any Go value outside the JSON model (other than int, int64
// and float32, which become float64).
func SanitizeJSON(v any) (any, error) {
	out, _, err := sanitizeValue(v, true)
	return out, err
}

type sanitizedEntry struct {
	key, orig string
	val       any
}

func sanitizeValue(v any, strict bool) (any, bool, error) {
	switch x := v.(type) {
	case nil, bool:
		return v, false, nil
	case float64:
		if math.IsNaN(x) || math.IsInf(x, 0) {
			if strict {
				return nil, false, fmt.Errorf("a number is not finite")
			}
			return nil, true, nil
		}
		return v, false, nil
	case int:
		return float64(x), true, nil
	case int64:
		return float64(x), true, nil
	case float32:
		return sanitizeValue(float64(x), strict)
	case string:
		c := Sanitize(x)
		return c, c != x, nil
	case []any:
		var out []any
		for i, e := range x {
			c, changed, err := sanitizeValue(e, strict)
			if err != nil {
				return nil, false, err
			}
			if changed && out == nil {
				out = make([]any, len(x))
				copy(out, x[:i])
			}
			if out != nil {
				out[i] = c
			}
		}
		if out == nil {
			return x, false, nil
		}
		return out, true, nil
	case map[string]any:
		entries := make([]sanitizedEntry, 0, len(x))
		dirty := false
		for k, e := range x {
			c, changed, err := sanitizeValue(e, strict)
			if err != nil {
				return nil, false, err
			}
			ck := Sanitize(k)
			dirty = dirty || changed || ck != k
			entries = append(entries, sanitizedEntry{ck, k, c})
		}
		if !dirty {
			return x, false, nil
		}
		out := make(map[string]any, len(entries))
		owner := make(map[string]string, len(entries))
		for _, e := range entries {
			if prev, dup := owner[e.key]; dup {
				if strict {
					a, b := min(prev, e.orig), max(prev, e.orig)
					return nil, false, fmt.Errorf("the keys %q and %q are the same after invisible characters are removed", a, b)
				}
				if prev < e.orig {
					continue
				}
			}
			owner[e.key] = e.orig
			out[e.key] = e.val
		}
		return out, true, nil
	}
	if strict {
		return nil, false, fmt.Errorf("%T is not a JSON value; use nil, bool, float64, string, []any or map[string]any", v)
	}
	return nil, true, nil
}

// canonicalJSON compacts JSON text and sanitizes the strings and keys in
// it. Clean text keeps its key order and number spelling. Text that held an
// unsafe character is encoded again, with sorted keys.
func canonicalJSON(text []byte) (json.RawMessage, error) {
	if serr := scanJSON(text); serr != nil {
		return nil, serr
	}
	var buf bytes.Buffer
	if err := json.Compact(&buf, text); err != nil {
		return nil, errors.New("the JSON does not parse")
	}
	var v any
	if err := json.Unmarshal(buf.Bytes(), &v); err != nil {
		// The syntax is valid, so this is a number too large for a float64.
		return nil, errors.New("a number is too large")
	}
	clean, changed, err := sanitizeValue(v, true)
	if err != nil {
		return nil, err
	}
	if !changed {
		return buf.Bytes(), nil
	}
	var out bytes.Buffer
	enc := json.NewEncoder(&out)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(clean); err != nil {
		return nil, err
	}
	return bytes.TrimSuffix(out.Bytes(), []byte("\n")), nil
}
