package app

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/Gaurav-Gosain/tuios/internal/config"
)

func searchOS(t *testing.T) *OS {
	t.Helper()
	useTempConfig(t)
	m := &OS{Settings: config.Global, Width: 120, Height: 44, UserConfig: config.DefaultConfig()}
	m.OpenSettings()
	return m
}

// searchFor opens the search line with query and returns the rows found.
func searchFor(m *OS, query string) ([]settingItem, []settingsHit) {
	m.SettingsSearchStart("")
	m.SettingsSearchSetQuery(query)
	return m.settingsSearchRows(m.settingsCategories())
}

func TestSettingsSearchRanksTheNameFirst(t *testing.T) {
	m := searchOS(t)
	items, hits := searchFor(m, "pane gap")
	if len(items) == 0 {
		t.Fatal("pane gap found nothing")
	}
	if items[0].Label != "Pane gap" {
		t.Errorf("the first result is %q, want the row called Pane gap", items[0].Label)
	}
	if hits[0].field != settingsFieldLabel {
		t.Errorf("Pane gap was found by field %d, want its name", hits[0].field)
	}
	// Every name hit ranks above every hit on anything else.
	seenOther := false
	for _, h := range hits {
		if h.field != settingsFieldLabel {
			seenOther = true
		} else if seenOther {
			t.Fatalf("a name hit ranks below a hit on another field: %+v", hits)
		}
	}
}

func TestSettingsSearchHighlightsTheMatchedLetters(t *testing.T) {
	m := searchOS(t)
	items, hits := searchFor(m, "pgap")
	if len(items) == 0 || items[0].Label != "Pane gap" {
		t.Fatalf("pgap did not rank Pane gap first: %v", labelsOf(items))
	}
	var lit strings.Builder
	for _, p := range hits[0].pos {
		lit.WriteByte(items[0].Label[p])
	}
	if got := strings.ToLower(lit.String()); got != "pgap" {
		t.Errorf("the highlight lights %q, want the letters typed, pgap", got)
	}
}

func TestSettingsSearchMatchesTheConfigKey(t *testing.T) {
	m := searchOS(t)
	items, hits := searchFor(m, "dockbar_position")
	if len(items) == 0 {
		t.Fatal("the config key found nothing")
	}
	if items[0].Path != "appearance.dockbar_position" || hits[0].field != settingsFieldKey {
		t.Errorf("first result %q by field %d, want appearance.dockbar_position by its key", items[0].Path, hits[0].field)
	}
	// Spaces stand for the key's underscores.
	items, _ = searchFor(m, "dockbar position")
	if len(items) == 0 || items[0].Path != "appearance.dockbar_position" {
		t.Errorf("dockbar position with a space did not find the key: %v", labelsOf(items))
	}
}

func TestSettingsSearchMatchesTheValueInForce(t *testing.T) {
	m := searchOS(t)
	m.setOption("appearance.border_style", "double")
	items, hits := searchFor(m, "double")
	found := false
	for i, it := range items {
		if it.Path == "appearance.border_style" {
			found = true
			if hits[i].field != settingsFieldValue {
				t.Errorf("Border style was found by field %d, want its value", hits[i].field)
			}
		}
	}
	if !found {
		t.Errorf("searching for the value double did not find Border style: %v", labelsOf(items))
	}
}

func TestSettingsSearchMatchesADescriptionWordButNotScatteredLetters(t *testing.T) {
	m := searchOS(t)
	items, hits := searchFor(m, "punctuation")
	idx := -1
	for i, it := range items {
		if it.Path == "appearance.word_characters" {
			idx = i
		}
	}
	if idx < 0 {
		t.Fatalf("a word from its description did not find Word characters: %v", labelsOf(items))
	}
	if hits[idx].field != settingsFieldDesc {
		t.Errorf("Word characters was found by field %d, want its description", hits[idx].field)
	}

	// Three letters that appear, far apart, in nearly every sentence.
	items, _ = searchFor(m, "zqj")
	if len(items) != 0 {
		t.Errorf("zqj found %d rows; a description must match close together, not letter by letter", len(items))
	}
}

func TestSettingsSearchEditsATextRowInPlace(t *testing.T) {
	m := searchOS(t)
	items, _ := searchFor(m, "preferred shell")
	if len(items) == 0 || items[0].Label != "Preferred shell" {
		t.Fatalf("preferred shell did not find the row first: %v", labelsOf(items))
	}
	runSave(t, m.SettingsActivate())
	if !m.SettingsEditActive() {
		t.Fatal("enter on a text result did not open its editor")
	}
	m.SettingsEditBuffer = "/bin/zsh"
	runSave(t, m.SettingsEditCommit())
	if m.UserConfig.Appearance.PreferredShell != "/bin/zsh" {
		t.Errorf("the edit landed as %q", m.UserConfig.Appearance.PreferredShell)
	}
}

// TestSettingsSearchRowsAreClickable: the rows are drawn one line lower while
// the search line is up, and the hit rects move with them.
func TestSettingsSearchRowsAreClickable(t *testing.T) {
	m := searchOS(t)
	searchFor(m, "gap")
	content, geo, rows := m.renderSettings()
	if len(rows) == 0 {
		t.Fatal("no hit rows")
	}
	lines := strings.Split(ansi.Strip(content), "\n")
	items := m.settingsCurrentItems()
	for _, r := range rows {
		line := lines[r.Rect.Y0]
		if !strings.Contains(line, items[r.Idx].Label) {
			t.Errorf("hit row %d at y=%d is over %q, not %q", r.Idx, r.Rect.Y0, line, items[r.Idx].Label)
		}
	}
	_ = geo
}

func labelsOf(items []settingItem) []string {
	out := make([]string, len(items))
	for i, it := range items {
		out[i] = it.Label
	}
	return out
}
