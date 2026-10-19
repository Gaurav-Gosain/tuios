// Ported from pele src/errors.ts at 458322a5b8abb1ae57ddf127c32017ab9fb14a92.

package types

import (
	"strconv"
	"strings"
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
	Type    string
	Line    int
	Column  int
	Snippet string
}

func (e *PeleError) Error() string { return e.Message }

// ErrorInfo is the optional part of a PeleError.
type ErrorInfo struct {
	Type    string
	Line    int
	Column  int
	Snippet string
}

func NewPeleError(message string, code PeleErrorCode, info ErrorInfo) *PeleError {
	if code == "" {
		code = CodeSyntax
	}
	return &PeleError{Message: message, Code: code, Type: info.Type, Line: info.Line, Column: info.Column, Snippet: info.Snippet}
}

// Throw panics with a PeleError, as pele throws one.
func Throw(message string, code PeleErrorCode, info ErrorInfo) {
	panic(NewPeleError(message, code, info))
}

type Location struct {
	Line    int
	Column  int
	Snippet string
}

// Locate finds the line and column of a byte offset. Columns count UTF-16 units, as pele's do.
func Locate(src string, offset int) Location {
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
