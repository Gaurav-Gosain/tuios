package app

import (
	"slices"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/i18n"
)

// TestSettingsCorpusCoversEveryOption ties the locale corpus to the
// registry. A row of the settings page draws its label from the
// [settings.label] table and its description from [settings.desc], and a
// path the corpus does not carry degrades to a label inferred from the
// path and to an empty description: readable, but a string the corpus never
// agreed to and the first thing a new locale would leave behind. Nothing
// failed when the two parted, because no test tied them. An option with no
// label entry or no desc entry fails here.
func TestSettingsCorpusCoversEveryOption(t *testing.T) {
	m := &OS{Settings: config.Global}
	for _, o := range config.Options() {
		if !m.loc().Has("label", o.Path) {
			t.Errorf("the locale corpus carries no label for %q", o.Path)
		}
		if !m.loc().Has("desc", o.Path) {
			t.Errorf("the locale corpus carries no desc for %q", o.Path)
		}
	}
}

// TestSettingsCorpusDescMatchesRegistry pins every [settings.desc] entry to
// the registry's Description byte for byte. The corpus was seeded from
// those strings, and the two can drift in both directions: a Description
// edited without its en.toml entry leaves the page describing an option
// the registry no longer does, and an edited entry leaves them disagreeing
// the other way. A desc entry is also the text a translator starts from,
// so a drift here quietly poisons every locale built on it.
func TestSettingsCorpusDescMatchesRegistry(t *testing.T) {
	m := &OS{Settings: config.Global}
	for _, o := range config.Options() {
		if got, want := m.loc().Desc(o.Path), o.Description; got != want {
			t.Errorf("desc(%q) = %q, want the registry's %q", o.Path, got, want)
		}
	}
}

// TestSettingsCorpusCoversEveryCategory ties the categories of the settings
// page to the [settings.category] table. A category names itself from the
// table by its English key, and a key no table carries falls back to the
// key itself, which reads and so hides the gap while English is the only
// locale. This fails on the first category the corpus does not carry, and
// on an English entry that is not its own key: the English table is the
// identity, the names the keys are chosen by.
func TestSettingsCorpusCoversEveryCategory(t *testing.T) {
	m := &OS{Settings: config.Global, Width: 120, Height: 40, SidebarAgentsSeen: true}
	open := true
	m.settingsAgentsOpen = &open
	loc := m.loc()
	if loc.Lang() != "en" {
		t.Fatalf("the settings language is %q, and the identity check below is a fact about English alone", loc.Lang())
	}
	for _, cat := range m.settingsCategories() {
		if !loc.Has("category", cat.Key) {
			t.Errorf("the locale corpus carries no category name for the key %q", cat.Key)
		}
		if name := loc.Category(cat.Key); name == "" {
			t.Errorf("category %q resolves to an empty name", cat.Key)
		} else if name != cat.Key {
			t.Errorf("the English entry of %q is %q, not the key itself", cat.Key, name)
		}
	}
}

// TestLanguageOptionContract pins appearance.language to the locales the
// binary embeds. The registry takes its accepted values from i18n.Codes(),
// the codes of the locale files embedded in the binary, so a locale that
// lands is settable the moment the binary is rebuilt, and a hand-written
// list in the registry is a locale the page cannot name. The default is
// config.DefaultLanguage, whose value is the code of the English template
// every lookup falls back to. Both facts fail here when the registry line
// is edited. The strictness is the point: a new locale must stay settable,
// and the default must stay the one language the binary always carries.
func TestLanguageOptionContract(t *testing.T) {
	o, ok := config.LookupOption("appearance.language")
	if !ok {
		t.Fatal("the registry no longer carries appearance.language")
	}
	if got, want := o.Accepted, i18n.Codes(); !slices.Equal(got, want) {
		t.Errorf("appearance.language accepts %v, want the embedded locales %v", got, want)
	}
	if got, want := o.Default, config.DefaultLanguage; got != want {
		t.Errorf("appearance.language defaults to %q, want config.DefaultLanguage (%q)", got, want)
	}
	if got, want := o.Default, "en"; got != want {
		t.Errorf("appearance.language defaults to %q, want %q, the code of the template i18n falls back to", got, want)
	}
}

// TestSettingsRowKeysResolve walks the keys of the tables the settings page
// draws whole lines from, row, hint, notify and value, and fails on an
// entry that draws nothing. The corpus was written by hand beside the
// calls that draw it, and an empty or mistyped entry draws a blank line or
// a raw key where a sentence belongs, and nothing else fails, because the
// key itself still resolves. Keys the page no longer draws are the
// corpus's own business and pass here; the guard is that every key the
// corpus carries draws.
func TestSettingsRowKeysResolve(t *testing.T) {
	m := &OS{Settings: config.Global}
	loc := m.loc()
	for _, key := range loc.Keys("row") {
		if loc.Row(key) == "" {
			t.Errorf("row %q draws an empty line", key)
		}
	}
	for _, key := range loc.Keys("hint") {
		// A hint falls back to its key, so the page never draws an empty
		// line; the empty entry behind the fallback is what this catches.
		if loc.Hint(key) == "" {
			t.Errorf("hint %q draws an empty line", key)
		}
	}
	for _, key := range loc.Keys("notify") {
		if loc.Notify(key) == "" {
			t.Errorf("notify %q draws an empty line", key)
		}
	}
	for _, key := range loc.Keys("value") {
		// A value key is a plain word or a table of plural forms, and a
		// count picks a form by the language's plural rules. The counts
		// below reach the plural categories of every language the binary
		// carries, and a key that answers empty at one of them has no word
		// where the page looks for one. A key no table carries at all
		// answers the key itself, which reads, so it passes; the empty
		// entry is what this catches.
		for _, n := range []int{0, 1, 2, 5} {
			if loc.ValueN(key, n) == "" {
				t.Errorf("value %q draws an empty string at the count %d", key, n)
			}
		}
	}
}
