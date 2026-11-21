package app

import (
	"slices"
	"strings"

	"github.com/Gaurav-Gosain/tuios/internal/listnav"
	"github.com/Gaurav-Gosain/tuios/internal/textedit"
	"github.com/Gaurav-Gosain/tuios/pkg/fuzzy"
)

type settingsField int

const (
	settingsFieldLabel settingsField = iota
	settingsFieldKey
	settingsFieldValue
	settingsFieldDesc
)

type settingsHit struct {
	cat, item int
	field     settingsField
	pos       []int
	score     int
}

type settingsSearchState struct {
	open    bool
	query   textedit.Line
	hits    []settingsHit
	backCat int
	back    listnav.State
}

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

func settingsDescCompact(n int) int { return n + n/2 + 1 }

func (m *OS) searchSettings(cats []settingsCategory, query string) []settingsHit {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil
	}
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
func (m *OS) SettingsSearchQuery() string { return m.settingsSearch.query.String() }

// SettingsSearchStart opens the search line, holding text to begin with. The
// tab view's place is kept so closing the search goes back to it.
func (m *OS) SettingsSearchStart(text string) {
	st := &m.settingsSearch
	if !st.open {
		st.open = true
		st.backCat, st.back = m.SettingsCategory, m.settingsList
		st.query.Set("")
	}
	if st.query.Insert(text) {
		m.settingsSearchRefiltered()
	}
}

// SettingsSearchSetQuery replaces the query and ranks the rows against it,
// putting the cursor on the best match.
//
// The ranking is done here, once per keystroke, and not per frame: the rows
// found stay where they are while one of them is changed, even when the change
// means its value no longer matches, so the row a person is working on does
// not move out from under the cursor.
func (m *OS) SettingsSearchSetQuery(query string) {
	m.settingsSearch.query.Set(query)
	m.settingsSearchRefiltered()
}

// SettingsSearchKey applies one key press to the search line (see lineKey):
// the word keys delete a word, alt+left and alt+right, ctrl+a and ctrl+e move
// the caret. Left and right are not here: in the results they change the row
// under the cursor, as on its own tab. It reports whether the key was the
// line's.
func (m *OS) SettingsSearchKey(key, text string) bool {
	changed, consumed := lineKey(&m.settingsSearch.query, key, text, cleanControl)
	if changed {
		m.settingsSearchRefiltered()
	}
	return consumed
}

// SettingsSearchPaste types a paste at the search line's caret.
func (m *OS) SettingsSearchPaste(s string) {
	if lineInsert(&m.settingsSearch.query, s, cleanControl) {
		m.settingsSearchRefiltered()
	}
}

func (m *OS) settingsSearchRefiltered() {
	st := &m.settingsSearch
	st.hits = m.searchSettings(m.settingsCategories(), st.query.String())
	m.settingsList.Refiltered(0)
}

// SettingsSearchBackspace removes the query's last character. The line stays
// open when it empties: backspace is also what resets a row on the tab view,
// and a held key running out of query must not start doing that.
func (m *OS) SettingsSearchBackspace() {
	if changed, _ := m.settingsSearch.query.Edit("backspace", "", textedit.LineOpts{}); changed {
		m.settingsSearchRefiltered()
	}
}

// SettingsSearchClose closes the search line and puts the tab view back where
// it was when the search opened.
func (m *OS) SettingsSearchClose() {
	st := &m.settingsSearch
	if !st.open {
		return
	}
	m.SettingsCategory, m.settingsList = st.backCat, st.back
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
	if m.settingsList.Cursor < 0 || m.settingsList.Cursor >= len(hits) {
		return
	}
	h := hits[m.settingsList.Cursor]
	*st = settingsSearchState{}
	m.SettingsCategory, m.settingsList.Cursor, m.settingsList.Scroll = h.cat, h.item, 0
}
