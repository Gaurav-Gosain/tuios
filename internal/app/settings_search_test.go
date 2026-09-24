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

func TestSettingsSearchResultsNameTheirTab(t *testing.T) {
	m := searchOS(t)
	searchFor(m, "confirm quit")
	content, _, _ := m.renderSettings()
	plain := ansi.Strip(content)
	row := ""
	for line := range strings.SplitSeq(plain, "\n") {
		if strings.Contains(line, "Confirm quit") {
			row = line
		}
	}
	if !strings.Contains(row, "Behavior") {
		t.Errorf("the result row does not name its tab: %q", row)
	}
	if !strings.Contains(plain, "1 match") && !strings.Contains(plain, "matches") {
		t.Errorf("the search line carries no count:\n%s", plain)
	}
	if !strings.Contains(plain, "appearance.confirm_quit") {
		t.Errorf("the description box does not give the row's key:\n%s", plain)
	}
}

func TestSettingsSearchSaysWhenNothingMatches(t *testing.T) {
	m := searchOS(t)
	searchFor(m, "xyzzyq")
	plain := ansi.Strip(func() string { c, _, _ := m.renderSettings(); return c }())
	if !strings.Contains(plain, "No settings match") {
		t.Errorf("an empty result does not say so:\n%s", plain)
	}
	if !strings.Contains(plain, "no matches") {
		t.Errorf("the count does not say no matches:\n%s", plain)
	}
}

// TestSettingsSearchActsOnTheRowInPlace: the row found is the real row, so
// enter toggles it without leaving the search, and the list does not move out
// from under the cursor when the change means the row no longer matches.
func TestSettingsSearchActsOnTheRowInPlace(t *testing.T) {
	m := searchOS(t)
	items, _ := searchFor(m, "confirm quit")
	if len(items) == 0 || items[0].Label != "Confirm quit" {
		t.Fatalf("confirm quit did not find the row first: %v", labelsOf(items))
	}
	before := m.Settings.AlwaysConfirmQuit
	runSave(t, m.SettingsActivate())
	if m.Settings.AlwaysConfirmQuit == before {
		t.Fatal("enter on the result did not toggle the setting")
	}
	if !m.SettingsSearchOpen() {
		t.Error("changing a row closed the search")
	}
	if got := m.settingsCurrentItems()[m.SettingsSelected].Label; got != "Confirm quit" {
		t.Errorf("the cursor moved to %q after the change", got)
	}
	runSave(t, m.SettingsAdjust(1))
	if m.Settings.AlwaysConfirmQuit != before {
		t.Error("right on the result did not change it back")
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

func TestSettingsSearchCloseAndJump(t *testing.T) {
	m := searchOS(t)
	m.SettingsCategory, m.SettingsSelected = 2, 3
	searchFor(m, "confirm quit")
	m.SettingsSearchClose()
	if m.SettingsSearchOpen() || m.SettingsCategory != 2 || m.SettingsSelected != 3 {
		t.Errorf("closing the search left tab %d row %d open=%v, want tab 2 row 3 closed",
			m.SettingsCategory, m.SettingsSelected, m.SettingsSearchOpen())
	}

	searchFor(m, "confirm quit")
	m.SettingsSearchJump()
	if m.SettingsSearchOpen() {
		t.Fatal("tab left the search open")
	}
	cats := m.settingsCategories()
	if cats[m.SettingsCategory].Name != "Behavior" || cats[m.SettingsCategory].Items[m.SettingsSelected].Label != "Confirm quit" {
		t.Errorf("tab went to %s row %d, want Confirm quit on Behavior", cats[m.SettingsCategory].Name, m.SettingsSelected)
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
