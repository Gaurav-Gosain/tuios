//go:build !js

package lexers

// The lexers chroma defines in Go rather than XML, which its lexers package
// holds and tuios no longer imports. The rules are copied from chroma
// v2.27.0 (lexers/go.go and lexers/markdown.go), MIT licensed; the licence
// is in COPYING. They differ from chroma's only in taking the
// registry they are registered in, where chroma's use its global one.

import (
	"strings"

	. "github.com/alecthomas/chroma/v2" //nolint:revive,staticcheck // the rules read as chroma's do, so they stay diffable against it
)

// registerCodedLexers adds Go, Go text templates and Markdown to reg. The
// XML lexers must be registered first: Go reads its raw strings as templates.
func registerCodedLexers(reg *LexerRegistry) {
	goTextTemplate := reg.Register(MustNewXMLLexer(
		gzipFS{lexerFiles},
		"go_template.xml",
	).SetConfig(
		&Config{
			Name:    "Go Text Template",
			Aliases: []string{"go-text-template"},
		},
	))

	reg.Register(MustNewLexer(
		&Config{
			Name:      "Go",
			Aliases:   []string{"go", "golang"},
			Filenames: []string{"*.go"},
			MimeTypes: []string{"text/x-gosrc"},
		},
		func() Rules { return goRules(goTextTemplate) },
	).SetAnalyser(func(text string) float32 {
		if strings.Contains(text, "fmt.") && strings.Contains(text, "package ") {
			return 0.5
		}
		if strings.Contains(text, "package ") {
			return 0.1
		}
		return 0.0
	}))

	reg.Register(&markdownLexer{reg: reg, Lexer: MustNewLexer(
		&Config{
			Name:      "markdown",
			Aliases:   []string{"md", "mkd"},
			Filenames: []string{"*.md", "*.mkd", "*.markdown"},
			MimeTypes: []string{"text/x-markdown"},
		},
		markdownRules,
	)})
}

// rule is a rule that leaves the state as it is, which is all of the rules
// below. chroma writes these as unkeyed literals, which go vet flags.
func rule(pattern string, emit Emitter) Rule {
	return Rule{Pattern: pattern, Type: emit}
}

func goRules(goTextTemplate Lexer) Rules {
	return Rules{
		"root": {
			rule(`\n`, TextWhitespace),
			rule(`\s+`, TextWhitespace),
			rule(`//[^\s\n\r][^\n\r]*`, CommentPreproc),
			rule(`//[^\n\r]*`, CommentSingle),
			rule(`/(\\\n)?[*](.|\n)*?[*](\\\n)?/`, CommentMultiline),
			rule(`(import|package)\b`, KeywordNamespace),
			rule(`(var|func|struct|map|chan|type|interface|const)\b`, KeywordDeclaration),
			rule(Words(``, `\b`, `break`, `default`, `select`, `case`, `defer`, `go`, `else`, `goto`, `switch`, `fallthrough`, `if`, `range`, `continue`, `for`, `return`), Keyword),
			rule(`(true|false|iota|nil)\b`, KeywordConstant),
			rule(Words(``, `\b(\()`, `uint`, `uint8`, `uint16`, `uint32`, `uint64`, `int`, `int8`, `int16`, `int32`, `int64`, `float`, `float32`, `float64`, `complex64`, `complex128`, `byte`, `rune`, `string`, `bool`, `error`, `uintptr`, `print`, `println`, `panic`, `recover`, `close`, `complex`, `real`, `imag`, `len`, `cap`, `append`, `copy`, `delete`, `new`, `make`, `clear`, `min`, `max`), ByGroups(NameBuiltin, Punctuation)),
			rule(Words(``, `\b`, `uint`, `uint8`, `uint16`, `uint32`, `uint64`, `int`, `int8`, `int16`, `int32`, `int64`, `float`, `float32`, `float64`, `complex64`, `complex128`, `byte`, `rune`, `string`, `bool`, `error`, `uintptr`, `any`), KeywordType),
			rule(`\d+i`, LiteralNumber),
			rule(`\d+\.\d*([Ee][-+]\d+)?i`, LiteralNumber),
			rule(`\.\d+([Ee][-+]\d+)?i`, LiteralNumber),
			rule(`\d+[Ee][-+]\d+i`, LiteralNumber),
			rule(`\d+(\.\d+[eE][+\-]?\d+|\.\d*|[eE][+\-]?\d+)`, LiteralNumberFloat),
			rule(`\.\d+([eE][+\-]?\d+)?`, LiteralNumberFloat),
			rule(`0[0-7]+`, LiteralNumberOct),
			rule(`0[xX][0-9a-fA-F_]+`, LiteralNumberHex),
			rule(`0b[01_]+`, LiteralNumberBin),
			rule(`(0|[1-9][0-9_]*)`, LiteralNumberInteger),
			rule(`'(\\['"\\abfnrtv]|\\x[0-9a-fA-F]{2}|\\[0-7]{1,3}|\\u[0-9a-fA-F]{4}|\\U[0-9a-fA-F]{8}|[^\\])'`, LiteralStringChar),
			rule("(`)([^`]*)(`)", ByGroups(LiteralString, UsingLexer(TypeRemappingLexer(goTextTemplate, TypeMapping{{Other, LiteralString, nil}})), LiteralString)),
			rule(`"(\\\\|\\"|[^"])*"`, LiteralString),
			rule(`(<<=|>>=|<<|>>|<=|>=|&\^=|&\^|\+=|-=|\*=|/=|%=|&=|\|=|&&|\|\||<-|\+\+|--|==|!=|:=|\.\.\.|[+\-*/%&])`, Operator),
			rule(`([a-zA-Z_]\w*)(\s*)(\()`, ByGroups(NameFunction, UsingSelf("root"), Punctuation)),
			rule(`[|^<>=!()\[\]{}.,;:~]`, Punctuation),
			rule(`[^\W\d]\w*`, NameOther),
		},
	}
}

// markdownLexer highlights a leading YAML front matter block, then the rest
// as Markdown.
type markdownLexer struct {
	Lexer
	reg *LexerRegistry
}

func (m *markdownLexer) Tokenise(options *TokeniseOptions, text string) (Iterator, error) {
	frontmatter, rest, ok := splitFrontmatter(text)
	if !ok {
		return m.Lexer.Tokenise(options, text)
	}
	yamlLexer := m.reg.Get("YAML")
	if yamlLexer == nil {
		return m.Lexer.Tokenise(options, text)
	}
	yamlTokens, err := yamlLexer.Tokenise(options, frontmatter)
	if err != nil {
		return nil, err
	}
	markdownTokens, err := m.Lexer.Tokenise(options, rest)
	if err != nil {
		return nil, err
	}
	return Concaterator(yamlTokens, markdownTokens), nil
}

// splitFrontmatter cuts a leading YAML front matter block off text.
func splitFrontmatter(text string) (frontmatter string, rest string, ok bool) {
	if !strings.HasPrefix(text, "---\n") && !strings.HasPrefix(text, "---\r\n") {
		return "", text, false
	}
	lineEnd := strings.IndexByte(text, '\n')
	if lineEnd < 0 {
		return "", text, false
	}
	if strings.TrimSuffix(text[:lineEnd], "\r") != "---" {
		return "", text, false
	}
	for pos := lineEnd + 1; pos < len(text); {
		next := strings.IndexByte(text[pos:], '\n')
		if next < 0 {
			break
		}
		lineEnd = pos + next
		line := strings.TrimSuffix(text[pos:lineEnd], "\r")
		if line == "---" {
			return text[:lineEnd+1], text[lineEnd+1:], true
		}
		pos = lineEnd + 1
	}
	return "", text, false
}

func markdownRules() Rules {
	return Rules{
		"root": {
			rule(`<!--[\w\W]*?-->`, CommentMultiline),
			rule(`^(#[^#].+\n)`, ByGroups(GenericHeading)),
			rule(`^(#{2,6}.+\n)`, ByGroups(GenericSubheading)),
			rule(`^(\s*)([*-] )(\[[ xX]\])( .+\n)`, ByGroups(Text, Keyword, Keyword, UsingSelf("inline"))),
			rule(`^(\s*)([*-])(\s)(.+\n)`, ByGroups(Text, Keyword, Text, UsingSelf("inline"))),
			rule(`^(\s*)([0-9]+\.)( .+\n)`, ByGroups(Text, Keyword, UsingSelf("inline"))),
			rule(`^(\s*>\s)(.+\n)`, ByGroups(Keyword, GenericEmph)),
			rule("^(```\\n)([\\w\\W]*?)(^```$)", ByGroups(String, Text, String)),
			rule("^(```)(\\w+)(\\n)([\\w\\W]*?)(^```$)", UsingByGroup(2, 4, String, String, String, Text, String)),
			Include("inline"),
		},
		"inline": {
			rule(`<!--[\w\W]*?-->`, CommentMultiline),
			rule(`\\.`, Text),
			rule(`(\s)(\*|_)((?:(?!\2).)*)(\2)((?=\W|\n))`, ByGroups(Text, GenericEmph, GenericEmph, GenericEmph, Text)),
			rule(`(\s)((\*\*|__).*?)\3((?=\W|\n))`, ByGroups(Text, GenericStrong, GenericStrong, Text)),
			rule(`(\s)(~~[^~]+~~)((?=\W|\n))`, ByGroups(Text, GenericDeleted, Text)),
			rule("`[^`]+`", LiteralStringBacktick),
			rule(`[@#][\w/:]+`, NameEntity),
			rule(`(!?\[)([^]]+)(\])(\()([^)]+)(\))`, ByGroups(Text, NameTag, Text, Text, NameAttribute, Text)),
			rule(`.|\n`, Text),
		},
	}
}
