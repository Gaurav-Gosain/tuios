//go:build !js

package diffview

import (
	"path/filepath"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/alecthomas/chroma/v2"

	"github.com/Gaurav-Gosain/tuios/internal/diffview/lexers"
)

const (
	maxHighlightBytes = 512 << 10
	maxHighlightLine  = 4096
)

// Enabled reports whether this build highlights at all. The browser build
// does not: it never shows a review, and the lexers would add megabytes to
// the page.
const Enabled = true

var lexerCache sync.Map

type noLexerT struct{}

var noLexer noLexerT

func lexerFor(path, text string) chroma.Lexer {
	name := filepath.Base(path)
	if v, ok := lexerCache.Load(name); ok {
		if l, ok := v.(chroma.Lexer); ok {
			return l
		}
		return nil
	}
	reg := lexers.Registry()
	l := reg.Match(name)
	if l == nil && filepath.Ext(name) == "" && strings.HasPrefix(text, "#!") {
		first, _, _ := strings.Cut(text, "\n")
		l = reg.Analyse(first)
	}
	if l == nil {
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
	out := make([][]Span, len(lines))
	ok := tokenise(path, lines, func(line, start, end int, t chroma.TokenType) {
		out[line] = addSpan(out[line], start, end, classOf(t))
	})
	if !ok {
		return nil
	}
	for i, spans := range out {
		if n := len(spans); n > 0 && spans[n-1].End > len(lines[i]) {
			out[i] = nil
		}
	}
	return out
}

var langCache sync.Map

var (
	langMissMu sync.Mutex
	langMisses = map[string]bool{}
)

const langMissMax = 64

func lexerForLang(lang string) chroma.Lexer {
	lang = strings.ToLower(lang)
	if v, ok := langCache.Load(lang); ok {
		return v.(chroma.Lexer)
	}
	langMissMu.Lock()
	miss := langMisses[lang]
	langMissMu.Unlock()
	if miss {
		return nil
	}
	l := lexers.Registry().Get(lang)
	if l == nil {
		langMissMu.Lock()
		if len(langMisses) >= langMissMax {
			clear(langMisses)
		}
		langMisses[lang] = true
		langMissMu.Unlock()
		return nil
	}
	l = chroma.Coalesce(l)
	langCache.Store(lang, l)
	return l
}

// HighlightLang is Highlight for a code block whose language is named, as
// a Markdown fence names it, rather than known by its file name. It returns
// nil for an empty or unknown name.
func HighlightLang(lang string, lines []string) [][]Span {
	if lang == "" || len(lang) > 32 {
		return nil
	}
	out := make([][]Span, len(lines))
	ok := tokeniseWith(func(string) chroma.Lexer { return lexerForLang(lang) }, lines, func(line, start, end int, t chroma.TokenType) {
		out[line] = addSpan(out[line], start, end, classOf(t))
	})
	if !ok {
		return nil
	}
	for i, spans := range out {
		if n := len(spans); n > 0 && spans[n-1].End > len(lines[i]) {
			out[i] = nil
		}
	}
	return out
}

// Token is a run of one token class in a line, in bytes of the line's text.
// Class is chroma's short CSS class name for the token type: "k" for a
// keyword, "s2" for a double-quoted string, "c1" for a line comment.
type Token struct {
	Start, End int
	Class      string
}

// Tokens is Highlight for a client that colours the code itself. It picks
// the lexer the same way and keeps the same limits, and gives each run
// chroma's CSS class name rather than a Class. Plain text and whitespace
// get no token. A line longer than skipLine characters is tokenised as
// empty and gets no tokens, so one long line costs the others nothing; 0
// keeps every line.
func Tokens(path string, lines []string, skipLine int) [][]Token {
	text := lines
	copied := false
	for i, l := range lines {
		if skipLine > 0 && len(l) > skipLine && utf8.RuneCountInString(l) > skipLine {
			if !copied {
				text, copied = append([]string(nil), lines...), true
			}
			text[i] = ""
		}
	}
	out := make([][]Token, len(lines))
	ok := tokenise(path, text, func(line, start, end int, t chroma.TokenType) {
		class := cssClass(t)
		if class == "" || class == "w" {
			return
		}
		toks := out[line]
		if n := len(toks); n > 0 && toks[n-1].Class == class && toks[n-1].End == start {
			toks[n-1].End = end
			return
		}
		out[line] = append(toks, Token{Start: start, End: end, Class: class})
	})
	if !ok {
		return nil
	}
	for i, toks := range out {
		if n := len(toks); n > 0 && toks[n-1].End > len(text[i]) {
			out[i] = nil
		}
	}
	return out
}

func cssClass(t chroma.TokenType) string {
	for _, tt := range []chroma.TokenType{t, t.SubCategory(), t.Category()} {
		if c, ok := chroma.StandardTypes[tt]; ok {
			return c
		}
	}
	return ""
}

func tokenise(path string, lines []string, add func(line, start, end int, t chroma.TokenType)) (ok bool) {
	return tokeniseWith(func(text string) chroma.Lexer { return lexerFor(path, text) }, lines, add)
}

func tokeniseWith(find func(text string) chroma.Lexer, lines []string, add func(line, start, end int, t chroma.TokenType)) (ok bool) {
	defer func() {
		if recover() != nil {
			ok = false
		}
	}()
	if len(lines) == 0 {
		return false
	}
	total := 0
	for _, l := range lines {
		if len(l) > maxHighlightLine {
			return false
		}
		total += len(l) + 1
	}
	if total > maxHighlightBytes {
		return false
	}
	text := strings.Join(lines, "\n") + "\n"
	lexer := find(text)
	if lexer == nil {
		return false
	}
	it, err := lexer.Tokenise(nil, text)
	if err != nil {
		return false
	}
	line, off := 0, 0
	for tok := it(); tok != chroma.EOF; tok = it() {
		value := tok.Value
		for value != "" && line < len(lines) {
			piece, rest, nl := strings.Cut(value, "\n")
			if piece != "" {
				add(line, off, off+len(piece), tok.Type)
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
	return true
}

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
