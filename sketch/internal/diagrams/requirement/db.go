// Ported from pele src/diagrams/requirement/db.ts at 8aff183be57021b533f319a29341a529be934b08.

package requirement

import (
	"slices"
	"strings"
	"unicode/utf8"

	"tuios.dev/sketch/internal/js"
	"tuios.dev/sketch/internal/types"
)

// MAX_STYLE_COPIES: applying a class copies its declarations onto each node, so a short text can ask for
// hundreds of millions of copies. Past this many the diagram is refused.
const MAX_STYLE_COPIES = 1_000_000

type RequirementDb struct {
	direction string
	// requirements, elements and classes keep the order pele's maps do.
	requirements      []*Requirement
	requirementByName map[string]*Requirement
	elements          []*RequirementElement
	elementByName     map[string]*RequirementElement
	relations         []Relation
	classes           []*RequirementClass
	classById         map[string]*RequirementClass
	title             string
	accTitle          string
	accDescr          string

	// The grammar reports a body's fields before the requirement or element they belong to.
	newId           string
	newText         string
	newRisk         string
	newVerifyMethod string
	newElementType  string
	newDocRef       string
	styleCopies     int
}

func newRequirementDb() *RequirementDb {
	return &RequirementDb{
		direction:         "TB",
		requirementByName: map[string]*Requirement{},
		elementByName:     map[string]*RequirementElement{},
		classById:         map[string]*RequirementClass{},
	}
}

func (db *RequirementDb) copy(target *[]string, styles []string) {
	db.styleCopies += len(styles)
	if db.styleCopies > MAX_STYLE_COPIES {
		types.Throw("Requirement diagram applies too many style declarations.", types.CodeLimit, types.ErrorInfo{Type: "requirement"})
	}
	*target = append(*target, styles...)
}

func (db *RequirementDb) setDirection(dir string) {
	db.direction = dir
}

func (db *RequirementDb) addRequirement(name, typ string) *Requirement {
	requirement := db.requirementByName[name]
	if requirement == nil {
		requirement = &Requirement{
			name:          name,
			typ:           typ,
			requirementId: db.newId,
			text:          db.newText,
			risk:          db.newRisk,
			verifyMethod:  db.newVerifyMethod,
			classes:       []string{"default"},
		}
		db.requirements = append(db.requirements, requirement)
		db.requirementByName[name] = requirement
	}
	db.newId, db.newText, db.newRisk, db.newVerifyMethod = "", "", "", ""
	return requirement
}

func (db *RequirementDb) setNewReqId(id string)     { db.newId = id }
func (db *RequirementDb) setNewReqText(text string) { db.newText = text }
func (db *RequirementDb) setNewReqRisk(risk string) { db.newRisk = risk }
func (db *RequirementDb) setNewReqVerifyMethod(verifyMethod string) {
	db.newVerifyMethod = verifyMethod
}

func (db *RequirementDb) addElement(name string) *RequirementElement {
	element := db.elementByName[name]
	if element == nil {
		element = &RequirementElement{name: name, typ: db.newElementType, docRef: db.newDocRef, classes: []string{"default"}}
		db.elements = append(db.elements, element)
		db.elementByName[name] = element
	}
	db.newElementType, db.newDocRef = "", ""
	return element
}

func (db *RequirementDb) setNewElementType(typ string)      { db.newElementType = typ }
func (db *RequirementDb) setNewElementDocRef(docRef string) { db.newDocRef = docRef }

func (db *RequirementDb) addRelationship(typ, src, dst string) {
	db.relations = append(db.relations, Relation{typ: typ, src: src, dst: dst})
}

// node gives the styles and classes of the requirement or element of that name.
func (db *RequirementDb) node(id string) (cssStyles, classes *[]string) {
	if r := db.requirementByName[id]; r != nil {
		return &r.cssStyles, &r.classes
	}
	if e := db.elementByName[id]; e != nil {
		return &e.cssStyles, &e.classes
	}
	return nil, nil
}

func (db *RequirementDb) setCssStyle(ids, styles []string) {
	for _, id := range ids {
		cssStyles, _ := db.node(id)
		// Mermaid stops at the first unknown id, leaving the rest of the list unstyled.
		if cssStyles == nil {
			return
		}
		for _, s := range styles {
			if strings.Contains(s, ",") {
				db.copy(cssStyles, strings.Split(s, ","))
			} else {
				db.copy(cssStyles, []string{s})
			}
		}
	}
}

func (db *RequirementDb) setClass(ids, classNames []string) {
	for _, id := range ids {
		cssStyles, classes := db.node(id)
		if cssStyles == nil {
			continue
		}
		for _, name := range classNames {
			*classes = append(*classes, name)
			if c := db.classById[name]; c != nil && c.styles != nil {
				db.copy(cssStyles, c.styles)
			}
		}
	}
}

func (db *RequirementDb) defineClass(ids, style []string) {
	for _, id := range ids {
		styleClass := db.classById[id]
		if styleClass == nil {
			styleClass = &RequirementClass{id: id, styles: []string{}}
			db.classes = append(db.classes, styleClass)
			db.classById[id] = styleClass
		}
		for _, s := range style {
			if strings.Contains(s, "color") {
				styleClass.textStyles = append(styleClass.textStyles, strings.Replace(s, "fill", "bgFill", 1))
			}
			styleClass.styles = append(styleClass.styles, s)
		}
		var split []string
		for _, s := range style {
			split = append(split, strings.Split(s, ",")...)
		}
		for _, requirement := range db.requirements {
			if slices.Contains(requirement.classes, id) {
				db.copy(&requirement.cssStyles, split)
			}
		}
		for _, element := range db.elements {
			if slices.Contains(element.classes, id) {
				db.copy(&element.cssStyles, split)
			}
		}
	}
}

func (db *RequirementDb) setAccTitle(text string) {
	db.accTitle = js.TrimStart(text)
}

// setAccDescription: text.replace(/\n\s+/g, '\n').
func (db *RequirementDb) setAccDescription(text string) {
	if !strings.Contains(text, "\n") {
		db.accDescr = text
		return
	}
	var b strings.Builder
	for i := 0; i < len(text); {
		if text[i] != '\n' {
			b.WriteByte(text[i])
			i++
			continue
		}
		b.WriteByte('\n')
		i++
		for i < len(text) {
			r, size := rune(text[i]), 1
			if r >= utf8.RuneSelf {
				r, size = utf8.DecodeRuneInString(text[i:])
			}
			if !js.IsSpace(r) {
				break
			}
			i += size
		}
	}
	db.accDescr = b.String()
}
