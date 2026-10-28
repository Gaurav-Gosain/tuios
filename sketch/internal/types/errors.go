// Ported from pele src/errors.ts at 8aff183be57021b533f319a29341a529be934b08.

package types

import (
	"regexp"
	"strconv"
	"strings"
	"sync"
	"unicode/utf8"
	"unsafe"

	"tuios.dev/sketch/internal/js"
)

type PeleErrorCode string

const (
	CodeUnsupported PeleErrorCode = "unsupported-diagram"
	CodeSyntax      PeleErrorCode = "syntax"
	CodeSemantic    PeleErrorCode = "semantic"
	CodeLimit       PeleErrorCode = "limit"
)

type PeleError struct {
	Message string
	Code    PeleErrorCode
	// Type is the diagram type, or "" for none.
	Type string
	// MermaidType is the diagram type as Mermaid names it, or "" for none.
	MermaidType string
	Line        int
	Column      int
	Snippet     string
}

func (e *PeleError) Error() string { return e.Message }

// ErrorInfo is the optional part of a PeleError.
type ErrorInfo struct {
	// Type is the diagram type, or "" for none.
	Type string
	// MermaidType is the diagram type as Mermaid names it, or "" for none.
	MermaidType string
	Line        int
	Column      int
	Snippet     string
}

//go:noinline
func NewPeleError(message string, code PeleErrorCode, info ErrorInfo) *PeleError {
	if code == "" {
		code = CodeSyntax
	}
	return &PeleError{Message: message, Code: code, Type: info.Type, MermaidType: info.MermaidType, Line: info.Line, Column: info.Column, Snippet: info.Snippet}
}

// Throw panics with a PeleError, as pele throws one.
//
//go:noinline
func Throw(message string, code PeleErrorCode, info ErrorInfo) {
	panic(NewPeleError(message, code, info))
}

type Location struct {
	Line    int
	Column  int
	Snippet string
}

// Edit is an input range [From, To) that a step replaced with Length output bytes.
// Out is the output when it holds characters of more than one byte: pele counts an
// offset inside it in UTF-16 units.
type Edit struct {
	From, To, Length int
	Out              string
}

// Replacements lists the matches of re in text, each replaced with length bytes.
func Replacements(text string, re *regexp.Regexp, length int) []Edit {
	var out []Edit
	for _, m := range re.FindAllStringIndex(text, -1) {
		out = append(out, Edit{From: m[0], To: m[1], Length: length})
	}
	return out
}

func stepInput(edits []Edit, at int) int {
	shift := 0
	for _, e := range edits {
		start := e.From - shift
		if at < start {
			break
		}
		if at < start+e.Length {
			k := at - start
			if e.Out != "" {
				k = utf16Len(e.Out[:k])
			}
			return e.From + min(k, e.To-e.From-1)
		}
		shift += e.To - e.From - e.Length
	}
	return at + shift
}

// InputOffset maps an offset in the output of the steps back to their input.
func InputOffset(steps [][]Edit, at int) int {
	for s := len(steps) - 1; s >= 0; s-- {
		at = stepInput(steps[s], at)
	}
	return at
}

// Origin finds an offset of the text being parsed in the source as written.
type Origin interface {
	Find(offset int) (source string, at int)
}

// origins holds the Origin of each text being parsed. pele keeps one in a module
// variable while it parses; a key per text lets parses on other goroutines run at
// the same time. The key is the string's data and length, which stands for
// JavaScript's === on the parsed text: the parsers pass that very string.
var origins struct {
	sync.Mutex
	m map[originKey]Origin
}

type originKey struct {
	data *byte
	n    int
}

func keyOf(s string) originKey { return originKey{unsafe.StringData(s), len(s)} }

// Locating makes errors located in parsed report positions in the source that o
// gives, until DoneLocating.
func Locating(parsed string, o Origin) {
	origins.Lock()
	if origins.m == nil {
		origins.m = map[originKey]Origin{}
	}
	origins.m[keyOf(parsed)] = o
	origins.Unlock()
}

// DoneLocating ends Locating.
func DoneLocating(parsed string) {
	origins.Lock()
	delete(origins.m, keyOf(parsed))
	origins.Unlock()
}

// Written maps an offset of src to the source as written, when src is exactly the
// text being parsed.
func Written(src string, offset int) (string, int) {
	origins.Lock()
	o := origins.m[keyOf(src)]
	origins.Unlock()
	if o != nil {
		return o.Find(offset)
	}
	return src, offset
}

// Locate finds the line and column of a byte offset. Columns count UTF-16 units, as pele's do.
func Locate(src string, offset int) Location {
	// Keep EOF errors on the last content line.
	if end := len(js.TrimEnd(src)); end > 0 && offset >= end {
		_, size := utf8.DecodeLastRuneInString(src[:end])
		src, offset = Written(src, end-size)
		if offset < len(src) {
			_, size = utf8.DecodeRuneInString(src[offset:])
			offset += size
		} else {
			offset++
		}
	} else {
		src, offset = Written(src, offset)
	}
	if offset > len(src) {
		offset = len(src)
	}
	line := 1
	lineStart := 0
	for i := strings.IndexByte(src, '\n'); i != -1 && i < offset; {
		line++
		lineStart = i + 1
		j := strings.IndexByte(src[i+1:], '\n')
		if j == -1 {
			break
		}
		i = i + 1 + j
	}
	lineEnd := -1
	if offset <= len(src) {
		if j := strings.IndexByte(src[offset:], '\n'); j != -1 {
			lineEnd = offset + j
		}
	}
	if lineEnd == -1 {
		lineEnd = len(src)
	}
	column := utf16Len(src[lineStart:offset]) + 1
	text := src[lineStart:lineEnd]
	return Location{Line: line, Column: column, Snippet: text + "\n" + strings.Repeat("-", column-1) + "^"}
}

func utf16Len(s string) int {
	n := 0
	for _, r := range s {
		if r > 0xffff {
			n += 2
		} else {
			n++
		}
	}
	return n
}

func SyntaxError(typ string, src string, offset int, detail string, lexical bool) *PeleError {
	loc := Locate(src, offset)
	var message string
	if lexical {
		message = "Lexical error on line " + strconv.Itoa(loc.Line) + ". Unrecognized text.\n" + loc.Snippet
	} else {
		message = "Parse error on line " + strconv.Itoa(loc.Line) + ":\n" + loc.Snippet + "\n" + detail
	}
	return NewPeleError(message, CodeSyntax, ErrorInfo{Type: typ, Line: loc.Line, Column: loc.Column, Snippet: loc.Snippet})
}
