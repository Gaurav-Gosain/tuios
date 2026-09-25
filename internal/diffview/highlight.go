//go:build !js

package diffview

import (
	"path/filepath"
	"strings"
	"sync"

	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/lexers"
)

// Limits past which a text is drawn plain rather than tokenised. chroma's
// lexers are regular expressions, and a minified file or a generated one can
// hold a line long enough to make one of them crawl. A diff that big is not
// read for its colours.
const (
	maxHighlightBytes = 512 << 10
	maxHighlightLine  = 4096
)

// Enabled reports whether this build highlights at all. The browser build
// does not: it never shows a review, and the lexers would add megabytes to
// the page.
const Enabled = true

// lexerCache holds the lexer for each file name, nil for none. Matching a
// name runs every lexer's patterns, which is the slow part of a lookup, and
// the answer for a name never changes.
var lexerCache sync.Map // string -> chroma.Lexer (nil stored as noLexer)

type noLexerT struct{}

var noLexer noLexerT

// lexerFor finds the lexer for a path by its name, then by the first line of
// the text for a file with no extension (a script with a #! line). It is nil
// when nothing matches.
func lexerFor(path, text string) chroma.Lexer {
	name := filepath.Base(path)
	if v, ok := lexerCache.Load(name); ok {
		if l, ok := v.(chroma.Lexer); ok {
			return l
		}
		return nil
	}
	l := lexers.Match(name)
	if l == nil && filepath.Ext(name) == "" && strings.HasPrefix(text, "#!") {
		first, _, _ := strings.Cut(text, "\n")
		l = lexers.Analyse(first)
	}
	if l == nil || l == lexers.Fallback {
		// A name with no extension is cached with its shebang's answer
		// only when it had one, so a later file of the same name with a
		// different first line is asked again.
		if filepath.Ext(name) != "" {
			lexerCache.Store(name, noLexer)
		}
		return nil
	}
	l = chroma.Coalesce(l)
	lexerCache.Store(name, l)
	return l
}

// Highlight tokenises lines of the file at path as one text, and returns the
// spans of each line. It returns nil when the file type is not known, the
// text is past the limits, or the lexer fails: the caller draws those lines
// plain.
//
// The lines are one side of one hunk, in order. Tokenising them together
// rather than one by one is what keeps a block comment or a multi-line
// string coloured on its later lines. A hunk starts wherever the diff does,
// so a hunk that opens inside a comment is read as code until the comment
// ends; that is the most a view of part of a file can do.
func Highlight(path string, lines []string) [][]Span {
	if len(lines) == 0 {
		return nil
	}
	total := 0
	for _, l := range lines {
		if len(l) > maxHighlightLine {
			return nil
		}
		total += len(l) + 1
	}
	if total > maxHighlightBytes {
		return nil
	}
	text := strings.Join(lines, "\n") + "\n"
	lexer := lexerFor(path, text)
	if lexer == nil {
		return nil
	}
	it, err := lexer.Tokenise(nil, text)
	if err != nil {
		return nil
	}
	out := make([][]Span, len(lines))
	line, off := 0, 0
	for tok := it(); tok != chroma.EOF; tok = it() {
		class := classOf(tok.Type)
		value := tok.Value
		for value != "" && line < len(lines) {
			piece, rest, nl := strings.Cut(value, "\n")
			if piece != "" {
				out[line] = addSpan(out[line], off, off+len(piece), class)
				off += len(piece)
			}
			if !nl {
				break
			}
			line, off, value = line+1, 0, rest
		}
		if line >= len(lines) {
			break
		}
	}
	// A lexer that changed the text (some normalise line endings or
	// expand tabs) would leave spans that do not fit the lines. Such a line
	// is drawn plain rather than with colours in the wrong places.
	for i, spans := range out {
		if n := len(spans); n > 0 && spans[n-1].End > len(lines[i]) {
			out[i] = nil
		}
	}
	return out
}

// addSpan appends a span, merging it into the last one when they are of one
// class and touch.
func addSpan(spans []Span, start, end int, class Class) []Span {
	if class == Plain {
		return spans
	}
	if n := len(spans); n > 0 && spans[n-1].Class == class && spans[n-1].End == start {
		spans[n-1].End = end
		return spans
	}
	return append(spans, Span{Start: start, End: end, Class: class})
}

// classOf maps chroma's token types onto the classes.
func classOf(t chroma.TokenType) Class {
	switch {
	case t == chroma.CommentPreproc || t == chroma.CommentPreprocFile:
		return Preproc
	case t.InCategory(chroma.Comment):
		return Comment
	case t == chroma.KeywordType:
		return Type
	case t == chroma.KeywordConstant:
		return Number
	case t.InCategory(chroma.Keyword):
		return Keyword
	case t == chroma.NameFunction || t == chroma.NameFunctionMagic:
		return Func
	case t == chroma.NameBuiltin || t == chroma.NameBuiltinPseudo:
		return Builtin
	case t == chroma.NameClass || t == chroma.NameException:
		return Type
	case t == chroma.NameConstant:
		return Number
	case t == chroma.NameDecorator:
		return Preproc
	case t == chroma.NameTag:
		return Tag
	case t == chroma.NameAttribute || t == chroma.NameProperty:
		return Attr
	case t.InSubCategory(chroma.LiteralString):
		return String
	case t.InSubCategory(chroma.LiteralNumber):
		return Number
	case t == chroma.Literal || t == chroma.LiteralDate:
		return String
	case t.InCategory(chroma.Operator):
		return Operator
	case t == chroma.Punctuation:
		return Punct
	case t == chroma.GenericHeading || t == chroma.GenericSubheading:
		return Heading
	}
	return Plain
}
