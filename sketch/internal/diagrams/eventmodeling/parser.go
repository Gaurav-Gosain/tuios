package eventmodeling

import (
	"strings"
	"sync"
	"unicode/utf8"

	"tuios.dev/sketch/internal/diagrams/common"
	"tuios.dev/sketch/internal/js"
)

func isIDStart(c byte) bool { return c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') }
func isIDPart(c byte) bool  { return isIDStart(c) || (c >= '0' && c <= '9') }

func matchID(src string, p int) int {
	if !isIDStart(src[p]) {
		return -1
	}
	q := p + 1
	for q < len(src) && isIDPart(src[q]) {
		q++
	}
	return q
}

func matchFrameID(src string, p int) int {
	q := p
	for q < len(src) && q-p < 3 && src[q] >= '0' && src[q] <= '9' {
		q++
	}
	if q == p {
		return -1
	}
	return q
}

func lineEnd(src string, q int) int {
	for q < len(src) {
		switch src[q] {
		case '\n', '\r':
			return q
		case 0xe2:
			if strings.HasPrefix(src[q:], " ") || strings.HasPrefix(src[q:], " ") {
				return q
			}
		}
		q++
	}
	return q
}

func matchInline(src string, p int) int {
	closer := byte(0)
	switch src[p] {
	case '{':
		closer = '}'
	case '"', '\'':
		closer = src[p]
	default:
		return -1
	}
	end := lineEnd(src, p+1)
	if at := strings.LastIndexByte(src[p+1:end], closer); at >= 0 {
		return p + 1 + at + 1
	}
	return -1
}

func lineBreakOrSpace(src string, q int) int {
	if strings.HasPrefix(src[q:], "\r\n") {
		return q + 2
	}
	if strings.HasPrefix(src[q:], "\n") {
		return q + 1
	}
	if q >= len(src) {
		return q
	}
	if c, _ := utf8.DecodeRuneInString(src[q:]); js.IsSpace(c) {
		return q
	}
	return -1
}

func closeBlock(src string, q int) int {
	if q >= len(src) || src[q] != '}' {
		return -1
	}
	return lineBreakOrSpace(src, q+1)
}

func matchBlock(src string, p int) int {
	if src[p] != '{' {
		return -1
	}
	q := p + 1
	for q < len(src) && (src[q] == ' ' || src[q] == '\t') {
		q++
	}
	if strings.HasPrefix(src[q:], "\r\n") {
		q += 2
	} else if strings.HasPrefix(src[q:], "\n") {
		q++
	} else {
		return -1
	}
	for c := q; c < len(src); c++ {
		if src[c] != '\n' {
			continue
		}
		if end := closeBlock(src, c+1); end >= 0 {
			return end
		}
	}
	return closeBlock(src, q)
}

func matchSpace(src string, p int) int {
	q := p
	for q < len(src) {
		c, size := utf8.DecodeRuneInString(src[q:])
		if !js.IsSpace(c) {
			break
		}
		q += size
	}
	if q == p {
		return -1
	}
	return q
}

func matchBlockComment(src string, p int) int {
	if !strings.HasPrefix(src[p:], "/*") {
		return -1
	}
	if at := strings.Index(src[p+2:], "*/"); at >= 0 {
		return p + 2 + at + 2
	}
	return -1
}

func matchLineComment(src string, p int) int {
	if !strings.HasPrefix(src[p:], "//") {
		return -1
	}
	q := p + 2
	for q < len(src) && src[q] != '\n' && src[q] != '\r' {
		q++
	}
	return q
}

var EM_ID = &common.TokenType{Name: "EM_ID", Match: matchID}

func word(text string) *common.TokenType {
	return &common.TokenType{Name: text, Pattern: text, Longer: []*common.TokenType{EM_ID}}
}

func sign(text string) *common.TokenType { return &common.TokenType{Name: text, Pattern: text} }

func renamed(t *common.TokenType, name string) *common.TokenType {
	c := *t
	c.Name = name
	return &c
}

var WORDS = []string{
	"resetframe", "readmodel", "processor", "timeframe", "command", "entity", "event", "jsobj", "figma", "given", "json", "salt", "html",
	"text", "data", "note", "when", "then", "rmo", "cmd", "evt", "pcr", "uri",
}

func buildTokens() []*common.TokenType {
	keyword := *common.Keyword("eventmodeling")
	keyword.Longer = []*common.TokenType{EM_ID}
	out := []*common.TokenType{&keyword}
	for _, w := range WORDS {
		out = append(out, word(w))
	}
	return append(out,
		sign("->>"),
		word("gwt"),
		word("ui"),
		word("md"),
		word("tf"),
		sign("[["),
		sign("]]"),
		word("rf"),
		sign("`"),
		sign("."),
		EM_ID,
		&common.TokenType{Name: "EM_FID", Match: matchFrameID},
		&common.TokenType{Name: "EM_DATA_INLINE", Match: matchInline},
		&common.TokenType{Name: "EM_DATA_BLOCK", Match: matchBlock},
		common.AccDescr("EM_ACC_DESCR"),
		renamed(common.ACC_TITLE, "EM_ACC_TITLE"),
		renamed(common.TitleBefore(common.WordNext), "EM_TITLE"),
		&common.TokenType{Name: "EM_WS", Match: matchSpace, Hidden: true},
		renamed(common.YAML, "EM_YAML"),
		renamed(common.DIRECTIVE, "EM_DIRECTIVE"),
		renamed(common.SINGLE_LINE_COMMENT, "EM_SINGLE_LINE_COMMENT"),
		&common.TokenType{Name: "EM_ML_COMMENT", Match: matchBlockComment, Hidden: true},
		&common.TokenType{Name: "EM_SL_COMMENT", Match: matchLineComment, Hidden: true},
	)
}

type kindSet []int

func (s kindSet) has(kind int) bool {
	for _, k := range s {
		if k == kind {
			return true
		}
	}
	return false
}

type tokenKinds struct {
	keyword, arrow, open, close, tick, dot, id, frameID, inline, block, accDescr, accTitle, title, entity, data, note, gwt, given, when, then int
}

type lexicon struct {
	tokens                                      []*common.TokenType
	T                                           tokenKinds
	timeFrame, resetFrame, entityType, dataType kindSet
}

var (
	lexiconOnce sync.Once
	theLexicon  *lexicon
)

func getLexicon() *lexicon {
	lexiconOnce.Do(func() {
		tokens := buildTokens()
		index := func(name string) int {
			for k, t := range tokens {
				if t.Name == name {
					return k
				}
			}
			return -1
		}
		kinds := func(names ...string) kindSet {
			var out kindSet
			for _, name := range names {
				out = append(out, index(name))
			}
			return out
		}
		theLexicon = &lexicon{
			tokens: tokens,
			T: tokenKinds{
				keyword: 0, arrow: index("->>"), open: index("[["), close: index("]]"), tick: index("`"), dot: index("."), id: index("EM_ID"),
				frameID: index("EM_FID"), inline: index("EM_DATA_INLINE"), block: index("EM_DATA_BLOCK"), accDescr: index("EM_ACC_DESCR"),
				accTitle: index("EM_ACC_TITLE"), title: index("EM_TITLE"), entity: index("entity"), data: index("data"), note: index("note"),
				gwt: index("gwt"), given: index("given"), when: index("when"), then: index("then"),
			},
			timeFrame:  kinds("tf", "timeframe"),
			resetFrame: kinds("rf", "resetframe"),
			entityType: kinds("rmo", "readmodel", "ui", "cmd", "command", "evt", "event", "pcr", "processor"),
			dataType:   kinds("json", "jsobj", "figma", "salt", "uri", "md", "html", "text"),
		}
	})
	return theLexicon
}

type EmFrame struct {
	reset            bool
	name             string
	modelEntityType  string
	entityIdentifier string
	sourceFrames     []string
	dataReference    string
	hasReference     bool
	dataType         string
	dataInlineValue  string
	hasInline        bool
	offset           int
}

type EmDataEntity struct {
	name           string
	dataType       string
	dataBlockValue string
}

type EmNoteEntity struct {
	sourceFrame    string
	dataType       string
	dataBlockValue string
}

type EmGwt struct {
	sourceFrame                                     string
	givenStatements, whenStatements, thenStatements []string
}

type EventModelAst struct {
	title         string
	accTitle      string
	accDescr      string
	modelEntities []string
	frames        []EmFrame
	dataEntities  []EmDataEntity
	noteEntities  []EmNoteEntity
	gwtEntities   []EmGwt
	source        string
}

func parseEventModel(src string) *EventModelAst {
	lx := getLexicon()
	T, TIME_FRAME, RESET_FRAME, ENTITY_TYPE, DATA_TYPE := lx.T, lx.timeFrame, lx.resetFrame, lx.entityType, lx.dataType
	r := common.NewReader(common.Tokenize(src, lx.tokens, "eventmodeling"), lx.tokens, "eventmodeling")
	ast := &EventModelAst{source: src}
	at := func(kind int) bool { return r.Kind() == kind }
	one := func(set kindSet, expected string) string {
		if !set.has(r.Kind()) {
			r.Fail(expected)
		}
		return r.Take()
	}
	qualifiedName := func() string {
		first := r.Expect(T.id)
		if r.Kind() != T.dot {
			return first
		}
		var name strings.Builder
		name.WriteString(first)
		for r.Accept(T.dot) {
			name.WriteString(".")
			name.WriteString(r.Expect(T.id))
		}
		return name.String()
	}
	dataType := func() (string, bool) {
		if !r.Accept(T.tick) {
			return "", false
		}
		typ := one(DATA_TYPE, "a data type")
		r.Expect(T.tick)
		return typ, true
	}
	statements := func() []string {
		var out []string
		for {
			one(ENTITY_TYPE, "an entity type")
			out = append(out, r.Expect(T.id))
			if !ENTITY_TYPE.has(r.Kind()) {
				return out
			}
		}
	}

	r.Expect(T.keyword)
	for !at(-1) {
		kind := r.Kind()
		switch {
		case kind == T.accDescr:
			ast.accDescr = r.Take()
		case kind == T.accTitle:
			ast.accTitle = r.Take()
		case kind == T.title:
			ast.title = r.Take()
		case kind == T.entity:
			r.I++
			ast.modelEntities = append(ast.modelEntities, qualifiedName())
		case TIME_FRAME.has(kind) || RESET_FRAME.has(kind):
			frame := EmFrame{reset: !TIME_FRAME.has(kind), offset: r.Tokens.Starts[r.I]}
			r.I++
			frame.name = r.Expect(T.frameID)
			frame.modelEntityType = one(ENTITY_TYPE, "an entity type")
			frame.entityIdentifier = qualifiedName()
			for r.Accept(T.arrow) {
				frame.sourceFrames = append(frame.sourceFrames, r.Expect(T.frameID))
			}
			if r.Accept(T.open) {
				frame.dataReference, frame.hasReference = r.Expect(T.id), true
				r.Expect(T.close)
			}
			if at(T.tick) || at(T.inline) {
				if typ, ok := dataType(); ok {
					frame.dataType = typ
				}
				frame.dataInlineValue, frame.hasInline = r.Expect(T.inline), true
			}
			ast.frames = append(ast.frames, frame)
		case kind == T.data:
			r.I++
			entity := EmDataEntity{name: r.Expect(T.id)}
			if typ, ok := dataType(); ok {
				entity.dataType = typ
			}
			entity.dataBlockValue = r.Expect(T.block)
			ast.dataEntities = append(ast.dataEntities, entity)
		case kind == T.note:
			r.I++
			note := EmNoteEntity{sourceFrame: r.Expect(T.frameID)}
			if typ, ok := dataType(); ok {
				note.dataType = typ
			}
			note.dataBlockValue = r.Expect(T.block)
			ast.noteEntities = append(ast.noteEntities, note)
		case kind == T.gwt:
			r.I++
			gwt := EmGwt{sourceFrame: r.Expect(T.frameID)}
			r.Expect(T.given)
			gwt.givenStatements = statements()
			if r.Accept(T.when) {
				gwt.whenStatements = statements()
			}
			r.Expect(T.then)
			gwt.thenStatements = statements()
			ast.gwtEntities = append(ast.gwtEntities, gwt)
		default:
			r.Fail("a frame, an entity, a data block, a note, or a specification")
		}
	}
	return ast
}
