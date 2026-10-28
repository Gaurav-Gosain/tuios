// Ported from pele src/diagrams/cynefin/model.ts at 8aff183be57021b533f319a29341a529be934b08.

package cynefin

import (
	"strings"
	"unicode/utf8"

	"tuios.dev/sketch/internal/js"
)

type CynefinDomain struct {
	name  DomainName
	items []string
}

type CynefinTransition struct {
	from  DomainName
	to    DomainName
	label string
}

type CynefinModel struct {
	title    string
	accTitle string
	accDescr string
	// The domains the source declares. All five are drawn either way.
	domains     map[DomainName]*CynefinDomain
	transitions []CynefinTransition
}

type CynefinDb struct {
	domains     map[DomainName]*CynefinDomain
	transitions []CynefinTransition
}

// setDomains: a later block for a domain replaces an earlier one.
func (db *CynefinDb) setDomains(blocks []DomainBlockAst) {
	for _, block := range blocks {
		db.domains[block.domain] = &CynefinDomain{name: block.domain, items: block.items}
	}
}

func (db *CynefinDb) setTransitions(transitions []TransitionAst) {
	db.transitions = nil
	for _, t := range transitions {
		if t.from != t.to {
			db.transitions = append(db.transitions, CynefinTransition(t))
		}
	}
}

func buildCynefin(ast *CynefinAst, title string) *CynefinModel {
	db := &CynefinDb{domains: map[DomainName]*CynefinDomain{}}
	db.setDomains(ast.domains)
	db.setTransitions(ast.transitions)
	model := &CynefinModel{title: ast.title, domains: db.domains, transitions: db.transitions}
	if model.title == "" {
		model.title = title
	}
	if ast.accTitle != "" {
		model.accTitle = js.TrimStart(ast.accTitle)
	}
	if ast.accDescr != "" {
		model.accDescr = joinLines(ast.accDescr)
	}
	return model
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
