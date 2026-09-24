package app

import (
	"slices"
	"strings"

	"github.com/Gaurav-Gosain/tuios/pkg/fuzzy"
)

// The settings page is fourteen tabs and nearly two hundred rows, and the only
// way to a row was to know which tab it was filed under. The search line finds
// a row from anything a person might remember about it: what it is called,
// its config key, the value it has now, or a word from its description. The
// results come from every tab at once, each marked with the tab it lives on,
// and the row found is the real row: enter, the arrows and a click act on it
// where it stands, so a person who found a setting never has to go and find
// it again to change it.

// settingsField is the part of a row a search matched.
type settingsField int

// The fields a row is searched by, in the order a match on each ranks: a hit
// on the name a person reads beats one on the key, which beats one on the
// value in force, which beats one in the description.
const (
	settingsFieldLabel settingsField = iota
	settingsFieldKey
	settingsFieldValue
	settingsFieldDesc
)

// settingsHit is one row a search found.
type settingsHit struct {
	cat, item int
	field     settingsField
	// pos is the byte offsets the query matched in the field's text, for the
	// highlight.
	pos   []int
	score int
}

// settingsSearchState is the search line and what it found.
type settingsSearchState struct {
	// open is whether the search line is up. While it is, printable keys go to
	// the query and the list shows the results.
	open  bool
	query string
	hits  []settingsHit
	// back is where the tab view was when the search opened, so closing the
	// search puts the person back where they were.
	backCat, backSel, backScroll int
}

// settingsValueText is what a row shows as its value, as text a search can
// match: a toggle's state in the words the row uses, a field's value or the
// word for its being unset.
func (m *OS) settingsValueText(item settingItem) string {
	switch {
	case item.Control == controlBool && item.boolVal != nil:
		if item.boolVal(m) {
			return "on"
		}
		return "off"
	case item.value != nil:
		if v := item.value(m); v != "" {
			return v
		}
		return item.Unset
	}
	return ""
}

// settingsDescCompact is the widest a description match may spread for a query
// of n bytes. A fuzzy match against a whole sentence finds almost any short
// query somewhere in it one letter at a time, which would put every row in the
// results; a match that stays close to the query's own length is one a person
// would recognise as the word they typed.
func settingsDescCompact(n int) int { return n + n/2 + 1 }

// searchSettings ranks every row of every category against query. An empty
// query finds nothing: the search line is open but has not asked anything yet.
func (m *OS) searchSettings(cats []settingsCategory, query string) []settingsHit {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil
	}
	// A key spells its words with underscores and dots, and a person types
	// them with spaces.
	keyQuery := strings.ReplaceAll(query, " ", "_")

	var matcher fuzzy.Matcher
	var hits []settingsHit
	for ci, cat := range cats {
		for ii, item := range cat.Items {
			hit, ok := m.bestSettingsMatch(&matcher, item, query, keyQuery)
			if !ok {
				continue
			}
			hit.cat, hit.item = ci, ii
			hits = append(hits, hit)
		}
	}
	slices.SortStableFunc(hits, func(a, b settingsHit) int {
		if a.field != b.field {
			return int(a.field) - int(b.field)
		}
		if a.score != b.score {
			return b.score - a.score
		}
		if a.cat != b.cat {
			return a.cat - b.cat
		}
		return a.item - b.item
	})
	return hits
}

// bestSettingsMatch is the best field of one row for a query, if any matched.
func (m *OS) bestSettingsMatch(matcher *fuzzy.Matcher, item settingItem, query, keyQuery string) (settingsHit, bool) {
	try := func(field settingsField, pattern, text string, compact bool) (settingsHit, bool) {
		if text == "" {
			return settingsHit{}, false
		}
		r, ok := matcher.Find(pattern, text)
		if !ok {
			return settingsHit{}, false
		}
		if compact && r.End-r.Start > settingsDescCompact(len(pattern)) {
			return settingsHit{}, false
		}
		// The matcher owns its position buffer and reuses it on the next call.
		return settingsHit{field: field, pos: slices.Clone(r.Positions), score: r.Score}, true
	}
	if hit, ok := try(settingsFieldLabel, query, item.Label, false); ok {
		return hit, true
	}
	if hit, ok := try(settingsFieldKey, keyQuery, item.Path, false); ok {
		return hit, true
	}
	if hit, ok := try(settingsFieldValue, query, m.settingsValueText(item), false); ok {
		return hit, true
	}
	return try(settingsFieldDesc, query, item.Desc, true)
}

// settingsSearchRows is the rows the open search shows, each with the hit
// that found it. A hit whose row has since gone (a host removed from the
// Hosts tab) is dropped rather than drawn as something else.
func (m *OS) settingsSearchRows(cats []settingsCategory) ([]settingItem, []settingsHit) {
	hits := m.settingsSearch.hits
	items := make([]settingItem, 0, len(hits))
	kept := make([]settingsHit, 0, len(hits))
	for _, h := range hits {
		if h.cat < 0 || h.cat >= len(cats) || h.item < 0 || h.item >= len(cats[h.cat].Items) {
			continue
		}
		items = append(items, cats[h.cat].Items[h.item])
		kept = append(kept, h)
	}
	return items, kept
}

// SettingsSearchOpen reports whether the search line is up.
func (m *OS) SettingsSearchOpen() bool { return m.settingsSearch.open }

// SettingsSearchQuery is what the search line holds.
func (m *OS) SettingsSearchQuery() string { return m.settingsSearch.query }

// SettingsSearchStart opens the search line, holding text to begin with. The
// tab view's place is kept so closing the search goes back to it.
func (m *OS) SettingsSearchStart(text string) {
	st := &m.settingsSearch
	if !st.open {
		st.open = true
		st.backCat, st.backSel, st.backScroll = m.SettingsCategory, m.SettingsSelected, m.SettingsScroll
		st.query = ""
	}
	m.SettingsSearchSetQuery(st.query + text)
}

// SettingsSearchSetQuery replaces the query and ranks the rows against it,
// putting the cursor on the best match.
//
// The ranking is done here, once per keystroke, and not per frame: the rows
// found stay where they are while one of them is changed, even when the change
// means its value no longer matches, so the row a person is working on does
// not move out from under the cursor.
func (m *OS) SettingsSearchSetQuery(query string) {
	st := &m.settingsSearch
	st.query = query
	st.hits = m.searchSettings(m.settingsCategories(), query)
	m.SettingsSelected = 0
	m.SettingsScroll = 0
}

// SettingsSearchBackspace removes the query's last character. The line stays
// open when it empties: backspace is also what resets a row on the tab view,
// and a held key running out of query must not start doing that.
func (m *OS) SettingsSearchBackspace() {
	q := []rune(m.settingsSearch.query)
	if len(q) == 0 {
		return
	}
	m.SettingsSearchSetQuery(string(q[:len(q)-1]))
}

// SettingsSearchClose closes the search line and puts the tab view back where
// it was when the search opened.
func (m *OS) SettingsSearchClose() {
	st := &m.settingsSearch
	if !st.open {
		return
	}
	m.SettingsCategory, m.SettingsSelected, m.SettingsScroll = st.backCat, st.backSel, st.backScroll
	*st = settingsSearchState{}
}

// SettingsSearchJump closes the search on the row under the cursor, on its own
// tab, so the rows it sits among are in view.
func (m *OS) SettingsSearchJump() {
	st := &m.settingsSearch
	if !st.open {
		return
	}
	_, hits := m.settingsSearchRows(m.settingsCategories())
	if m.SettingsSelected < 0 || m.SettingsSelected >= len(hits) {
		return
	}
	h := hits[m.SettingsSelected]
	*st = settingsSearchState{}
	m.SettingsCategory, m.SettingsSelected, m.SettingsScroll = h.cat, h.item, 0
}

// settingsSelectedHit is the hit under the cursor while the search is open.
func (m *OS) settingsSelectedHit(cats []settingsCategory) (settingsHit, bool) {
	if !m.settingsSearch.open {
		return settingsHit{}, false
	}
	_, hits := m.settingsSearchRows(cats)
	if m.SettingsSelected < 0 || m.SettingsSelected >= len(hits) {
		return settingsHit{}, false
	}
	return hits[m.SettingsSelected], true
}
