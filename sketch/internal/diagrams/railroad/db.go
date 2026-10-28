// Ported from pele src/diagrams/railroad/db.ts at 8aff183be57021b533f319a29341a529be934b08.

package railroad

import (
	"strings"
	"unicode/utf8"

	"tuios.dev/sketch/internal/diagrams/common"
	"tuios.dev/sketch/internal/js"
	"tuios.dev/sketch/internal/types"
)

type RailroadDb struct {
	notation Notation
	title    string
	accTitle string
	accDescr string
	// Every rule in source order. A name defined twice appears twice.
	rules []RailroadRule
}

func (db *RailroadDb) setTitle(text string) { db.title = text }

func (db *RailroadDb) setAccTitle(text string) { db.accTitle = js.TrimStart(text) }

func (db *RailroadDb) setAccDescription(text string) { db.accDescr = joinLines(text) }

// addRule: a name defined again is drawn again.
func (db *RailroadDb) addRule(rule RailroadRule) { db.rules = append(db.rules, rule) }

// notationOf answers as /^\s*railroad-(ebnf-|abnf-|peg-)?beta/i: Mermaid has a detector per
// keyword, each matching without regard to case.
func notationOf(source string) (Notation, bool) {
	s := js.TrimStart(source)
	if !common.WordAt(s, 0, "railroad-") {
		return "", false
	}
	s = s[len("railroad-"):]
	for _, infix := range [...]string{"ebnf", "abnf", "peg"} {
		if common.WordAt(s, 0, infix+"-beta") {
			return infix, true
		}
	}
	if common.WordAt(s, 0, "beta") {
		return "railroad", true
	}
	return "", false
}

type parsed struct {
	title, accTitle, accDescr string
}

func grammar(source string, notation Notation) (parsed, []RailroadRule) {
	var ast *Grammar
	switch notation {
	case "ebnf":
		ast = parseEbnf(source)
		return ast.meta(), ebnfRules(ast)
	case "abnf":
		ast = parseAbnf(source)
		return ast.meta(), abnfRules(ast)
	case "peg":
		ast = parsePeg(source)
		return ast.meta(), pegRules(ast)
	}
	ast = parseIr(source)
	return ast.meta(), irRules(ast)
}

// populate parses the text in one notation and loads the result into the model, as Mermaid's four parsers do.
func populate(db *RailroadDb, source string, notation Notation) {
	ast, rules := grammar(source, notation)
	db.notation = notation
	if ast.accDescr != "" {
		db.setAccDescription(ast.accDescr)
	}
	if ast.accTitle != "" {
		db.setAccTitle(ast.accTitle)
	}
	if ast.title != "" {
		db.setTitle(ast.title)
	}
	for _, rule := range rules {
		db.addRule(rule)
	}
}

func parseRailroad(source string, title string) *RailroadDb {
	notation, ok := notationOf(source)
	if !ok {
		types.Throw("No railroad diagram keyword found.", types.CodeSyntax, types.ErrorInfo{Type: "railroad"})
	}
	db := &RailroadDb{notation: "railroad", title: title}
	populate(db, source, notation)
	return db
}

// joinLines is .replace(/\n\s+/g, '\n').
func joinLines(text string) string {
	var out strings.Builder
	for i := 0; i < len(text); {
		if text[i] != '\n' {
			out.WriteByte(text[i])
			i++
			continue
		}
		j := i + 1
		for j < len(text) {
			c, size := utf8.DecodeRuneInString(text[j:])
			if !js.IsSpace(c) {
				break
			}
			j += size
		}
		out.WriteByte('\n')
		i = j
	}
	return out.String()
}
