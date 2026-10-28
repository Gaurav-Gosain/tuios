// Ported from pele src/diagrams/venn/db.ts at 8aff183be57021b533f319a29341a529be934b08.

package venn

import (
	"slices"
	"strings"

	"tuios.dev/sketch/internal/js"
	"tuios.dev/sketch/internal/types"
)

// normalizeText: Mermaid takes one pair of quotes off an id, label or style value.
func normalizeText(text string) string {
	trimmed := js.Trim(text)
	if js.Len(trimmed) >= 2 && strings.HasPrefix(trimmed, "\"") && strings.HasSuffix(trimmed, "\"") {
		return trimmed[1 : len(trimmed)-1]
	}
	return trimmed
}

func normalizeIds(ids []string) []string {
	out := make([]string, len(ids))
	for i, id := range ids {
		out[i] = normalizeText(id)
	}
	slices.SortStableFunc(out, js.Compare)
	return out
}

type VennDb struct {
	title      string
	hasTitle   bool
	subsets    []VennSubset
	textNodes  []VennText
	styles     []VennStyle
	known      map[string]bool
	current    []string
	hasCurrent bool
	// Whether an indented `text` line belongs to the set or union before it. The lexer reads this.
	indent bool
}

func newVennDb() *VennDb { return &VennDb{known: map[string]bool{}} }

func (db *VennDb) setDiagramTitle(title string) {
	db.title, db.hasTitle = title, true
}

// addSubsetData: a set without a size gets 10, and an overlap of n sets 10 / n².
func (db *VennDb) addSubsetData(ids []string, label string, hasLabel bool, size float64, hasSize bool) {
	sets := normalizeIds(ids)
	db.current, db.hasCurrent = sets, true
	if len(sets) == 1 {
		db.known[sets[0]] = true
	}
	if !hasSize {
		n := float64(len(ids))
		size = 10 / float64(n*n)
	}
	subset := VennSubset{sets: sets, size: size}
	if hasLabel && label != "" {
		subset.label, subset.hasLabel = normalizeText(label), true
	}
	db.subsets = append(db.subsets, subset)
}

func (db *VennDb) addTextData(ids []string, id string, label string, hasLabel bool) {
	node := VennText{sets: normalizeIds(ids), id: normalizeText(id)}
	if hasLabel && label != "" {
		node.label, node.hasLabel = normalizeText(label), true
	}
	db.textNodes = append(db.textNodes, node)
}

func (db *VennDb) addStyleData(ids []string, data [][2]string) {
	styles := make([][2]string, 0, len(data))
	at := map[string]int{}
	for _, kv := range data {
		value := kv[1]
		if value != "" {
			value = normalizeText(value)
		}
		// A later value for a key replaces the earlier one in its place, as Map.set does.
		if k, ok := at[kv[0]]; ok {
			styles[k][1] = value
		} else {
			at[kv[0]] = len(styles)
			styles = append(styles, [2]string{kv[0], value})
		}
	}
	db.styles = append(db.styles, VennStyle{targets: normalizeIds(ids), styles: styles})
}

func (db *VennDb) validateUnionIdentifiers(ids []string) {
	var unknown []string
	for _, id := range ids {
		if id := normalizeText(id); !db.known[id] {
			unknown = append(unknown, id)
		}
	}
	if len(unknown) > 0 {
		types.Throw("unknown set identifier: "+strings.Join(unknown, ", "), types.CodeSemantic, types.ErrorInfo{Type: "venn"})
	}
}

func (db *VennDb) getIndentMode() bool        { return db.indent }
func (db *VennDb) setIndentMode(enabled bool) { db.indent = enabled }
