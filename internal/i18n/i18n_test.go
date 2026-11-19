package i18n

import (
	"fmt"
	"os"
	"slices"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/testutil"
)

// TestMain isolates the whole test binary from the developer's own XDG
// directories. See testutil.RunIsolated for why this cannot be a per-test
// helper.
func TestMain(m *testing.M) { os.Exit(testutil.RunIsolated(m)) }

// TestEmbeddedLocales pins the embedded locales themselves: each parses,
// its [meta] code is the code its file name says, and the catalogue they
// feed holds every locale the binary ships, in a stable order.
func TestEmbeddedLocales(t *testing.T) {
	t.Run("en.toml parses and its meta names it", func(t *testing.T) {
		data, err := localeFiles.ReadFile("locales/en.toml")
		if err != nil {
			t.Fatalf("read the embedded en.toml: %v", err)
		}
		_, lang, err := parseLocale("en.toml", data)
		if err != nil {
			t.Fatalf("en.toml does not parse: %v", err)
		}
		if lang.Code != "en" {
			t.Errorf("en.toml says its code is %q, want the file name %q", lang.Code, "en")
		}
		if lang.Name != "English" {
			t.Errorf("en.toml says its name is %q, want %q", lang.Name, "English")
		}
	})
	t.Run("ru.toml parses and its meta names it", func(t *testing.T) {
		data, err := localeFiles.ReadFile("locales/ru.toml")
		if err != nil {
			t.Fatalf("read the embedded ru.toml: %v", err)
		}
		_, lang, err := parseLocale("ru.toml", data)
		if err != nil {
			t.Fatalf("ru.toml does not parse: %v", err)
		}
		if lang.Code != "ru" {
			t.Errorf("ru.toml says its code is %q, want the file name %q", lang.Code, "ru")
		}
		if lang.Name != "Русский" {
			t.Errorf("ru.toml says its name is %q, want %q", lang.Name, "Русский")
		}
	})
	t.Run("languages and codes hold both locales, in a stable order", func(t *testing.T) {
		want := []Language{{Code: "en", Name: "English"}, {Code: "ru", Name: "Русский"}}
		if got := Languages(); !slices.Equal(got, want) {
			t.Errorf("Languages() = %v, want %v", got, want)
		}
		if got, want := Codes(), []string{"en", "ru"}; !slices.Equal(got, want) {
			t.Errorf("Codes() = %v, want %v", got, want)
		}
		if got, again := Languages(), Languages(); !slices.Equal(got, again) {
			t.Errorf("Languages() = %v, then %v: the order is not stable", got, again)
		}
	})
}

// TestFor pins who answers: a locale for its own code, English for an
// empty string and for a code the binary does not carry, and one shared
// Localizer per code, parsed once.
func TestFor(t *testing.T) {
	t.Run("english, empty and unknown codes give english", func(t *testing.T) {
		for _, code := range []string{"en", "", "xx-unknown"} {
			if got := For(code).Lang(); got != "en" {
				t.Errorf("For(%q).Lang() = %q, want %q", code, got, "en")
			}
		}
	})
	t.Run("russian answers for its own code", func(t *testing.T) {
		if got := For("ru").Lang(); got != "ru" {
			t.Errorf("For(ru).Lang() = %q, want %q", got, "ru")
		}
	})
	t.Run("a code is parsed once and shared", func(t *testing.T) {
		for _, code := range []string{"en", "ru"} {
			first, second := For(code), For(code)
			if first != second {
				t.Errorf("For built a second %q Localizer for the same code", code)
			}
		}
	})
}

// TestEnglishDictionary reads the example entry of every section through
// the Localizer, the way the settings page will.
func TestEnglishDictionary(t *testing.T) {
	l := For("en")
	cases := []struct {
		name string
		got  string
		want string
	}{
		{"category", l.Category("Appearance"), "Appearance"},
		{"label", l.Label("appearance.gap"), "Pane gap"},
		{"desc", l.Desc("appearance.gap"), "Cells of empty ground kept between two neighbouring tiled panes"},
		{"placeholder", l.Placeholder("appearance.preferred_shell"), "/bin/bash"},
		{"unset", l.Unset("appearance.preferred_shell"), "(auto-detect)"},
		{"row", l.Row("theme.edit.label"), "Edit theme"},
		{"value", l.Value("on"), "on"},
		{"hint", l.Hint("move"), "move"},
		{"notify", l.Notify("save_failed"), "Could not save settings: {err}"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.got != tc.want {
				t.Errorf("got %q, want %q", tc.got, tc.want)
			}
		})
	}
}

// TestRussianDictionary reads the Russian locale through the Localizer, the
// way the settings page will: one example per section, the counted values
// through the CLDR forms pluralCategory picks for Russian, and every key it
// carries held against the English template, whose keys are the contract
// every locale translates.
func TestRussianDictionary(t *testing.T) {
	l := For("ru")
	cases := []struct {
		name string
		got  string
		want string
	}{
		{"lang", l.Lang(), "ru"},
		{"category", l.Category("Appearance"), "Оформление"},
		{"label", l.Label("appearance.gap"), "Отступ между панелями"},
		{"desc", l.Desc("appearance.gap"), "Пустых ячеек между двумя соседними панелями плитки"},
		{"placeholder", l.Placeholder("appearance.preferred_shell"), "/bin/bash"},
		{"unset", l.Unset("appearance.preferred_shell"), "(авто-определение)"},
		{"row", l.Row("settings.title"), "Настройки"},
		{"value", l.Value("on"), "вкл"},
		{"hint", l.Hint("move"), "двигать"},
		{"notify", l.Notify("save_failed"), "Не удалось сохранить настройки: {err}"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.got != tc.want {
				t.Errorf("got %q, want %q", tc.got, tc.want)
			}
		})
	}
	counted := []struct {
		n    int
		got  string
		want string
	}{
		{1, l.ValueN("n_sections", 1), "1 секция"},
		{3, l.ValueN("n_sections", 3), "3 секции"},
		{5, l.ValueN("n_sections", 5), "5 секций"},
		{0, l.ValueN("n_sections", 0), "0 секций"},
		{21, l.ValueN("n_sections", 21), "21 секция"},
		{11, l.ValueN("n_sections", 11), "11 секций"},
	}
	for _, tc := range counted {
		t.Run(fmt.Sprintf("n_sections %d", tc.n), func(t *testing.T) {
			if tc.got != tc.want {
				t.Errorf("ValueN(n_sections, %d) = %q, want %q", tc.n, tc.got, tc.want)
			}
		})
	}
	t.Run("every ru key is a key the English template carries", func(t *testing.T) {
		en := For("en")
		for _, table := range sectionTables {
			for key := range l.sections[table] {
				if _, ok := en.sections[table][key]; !ok {
					t.Errorf("ru carries %s %q, which the English template does not", table, key)
				}
			}
		}
		for key := range l.words {
			if _, ok := en.words[key]; !ok {
				t.Errorf("ru carries the value %q, which the English template does not", key)
			}
		}
		for key := range l.forms {
			if _, ok := en.forms[key]; !ok {
				t.Errorf("ru carries the counted value %q, which the English template does not", key)
			}
		}
	})
}

// TestUnknownKeys pins what a key the dictionary does not carry answers, so
// a page that names a key the corpus has not reached yet still draws
// readable English rather than a blank.
func TestUnknownKeys(t *testing.T) {
	l := For("en")
	cases := []struct {
		name string
		got  string
		want string
	}{
		{"label is inferred from the path", l.Label("appearance.unknown_key"), "Unknown key"},
		{"label of a bare path is inferred too", l.Label("unknown_key"), "Unknown key"},
		{"desc is empty", l.Desc("appearance.unknown_key"), ""},
		{"placeholder is empty", l.Placeholder("appearance.unknown_key"), ""},
		{"unset shows the default", l.Unset("appearance.unknown_key"), "(default)"},
		{"row is empty", l.Row("nope"), ""},
		{"value is empty", l.Value("nope"), ""},
		{"notify is empty", l.Notify("nope"), ""},
		{"category is the key", l.Category("nope"), "nope"},
		{"a key that is not words is still the key", l.Category("НетТакой"), "НетТакой"},
		{"hint is the key", l.Hint("nope"), "nope"},
		{"counted value is the key", l.ValueN("nope", 3), "nope"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.got != tc.want {
				t.Errorf("got %q, want %q", tc.got, tc.want)
			}
		})
	}
}

// TestInterpolation pins how the name, value pairs fill a template, and
// what a template answers when the pairs do not match it.
func TestInterpolation(t *testing.T) {
	l := For("en")
	t.Run("a named pair fills its placeholder", func(t *testing.T) {
		got, want := l.Notifyf("save_failed", "err", "boom"), "Could not save settings: boom"
		if got != want {
			t.Errorf("Notifyf(save_failed, err, boom) = %q, want %q", got, want)
		}
	})
	t.Run("an odd number of subs fills nothing", func(t *testing.T) {
		got, want := l.Notifyf("save_failed", "err"), "Could not save settings: {err}"
		if got != want {
			t.Errorf("Notifyf(save_failed, err) = %q, want %q", got, want)
		}
	})
	t.Run("a placeholder no pair names stays as written", func(t *testing.T) {
		got, want := l.Notifyf("save_failed", "name", "x"), "Could not save settings: {err}"
		if got != want {
			t.Errorf("Notifyf(save_failed, name, x) = %q, want %q", got, want)
		}
	})
	t.Run("a row with no placeholder is returned whole", func(t *testing.T) {
		got, want := l.Rowf("theme.edit.label", "move", "right"), "Edit theme"
		if got != want {
			t.Errorf("Rowf(theme.edit.label, move, right) = %q, want %q", got, want)
		}
	})
	t.Run("a value with no placeholder is returned whole", func(t *testing.T) {
		got, want := l.Valuef("on"), "on"
		if got != want {
			t.Errorf("Valuef(on) = %q, want %q", got, want)
		}
	})
}

// TestValueN pins the counted values: the CLDR form the count picks, the
// count filling "{n}", and a plain word that pays the category no mind.
func TestValueN(t *testing.T) {
	l := For("en")
	t.Run("one", func(t *testing.T) {
		if got, want := l.ValueN("n_sections", 1), "1 section"; got != want {
			t.Errorf("ValueN(n_sections, 1) = %q, want %q", got, want)
		}
	})
	t.Run("other", func(t *testing.T) {
		if got, want := l.ValueN("n_sections", 3), "3 sections"; got != want {
			t.Errorf("ValueN(n_sections, 3) = %q, want %q", got, want)
		}
	})
	t.Run("zero takes other", func(t *testing.T) {
		if got, want := l.ValueN("n_sections", 0), "0 sections"; got != want {
			t.Errorf("ValueN(n_sections, 0) = %q, want %q", got, want)
		}
	})
	t.Run("a plain word ignores the count", func(t *testing.T) {
		if got, want := l.ValueN("auto", 0), "Auto"; got != want {
			t.Errorf("ValueN(auto, 0) = %q, want %q", got, want)
		}
	})
}

// TestNilLocalizer pins the render path's promise: a bare model holds a nil
// Localizer, and every method still answers, in English.
func TestNilLocalizer(t *testing.T) {
	var l *Localizer
	cases := []struct {
		name string
		got  string
		want string
	}{
		{"lang", l.Lang(), "en"},
		{"category", l.Category("Appearance"), "Appearance"},
		{"label", l.Label("appearance.gap"), "Pane gap"},
		{"desc", l.Desc("appearance.gap"), "Cells of empty ground kept between two neighbouring tiled panes"},
		{"placeholder", l.Placeholder("appearance.preferred_shell"), "/bin/bash"},
		{"unset", l.Unset("appearance.preferred_shell"), "(auto-detect)"},
		{"row", l.Row("theme.edit.label"), "Edit theme"},
		{"rowf", l.Rowf("theme.edit.label"), "Edit theme"},
		{"value", l.Value("on"), "on"},
		{"valuef", l.Valuef("off"), "off"},
		{"counted value", l.ValueN("n_sections", 2), "2 sections"},
		{"hint", l.Hint("move"), "move"},
		{"notify", l.Notify("save_failed"), "Could not save settings: {err}"},
		{"notifyf", l.Notifyf("save_failed", "err", "boom"), "Could not save settings: boom"},
		{"unknown key still answers its fallback", l.Label("appearance.unknown_key"), "Unknown key"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.got != tc.want {
				t.Errorf("got %q, want %q", tc.got, tc.want)
			}
		})
	}
}

// TestPluralCategory pins the cardinal rules behind a counted value, case
// by case, including the teens Russian shares no form with.
func TestPluralCategory(t *testing.T) {
	cases := []struct {
		lang string
		n    int
		want string
	}{
		{"en", 1, "one"},
		{"en", 2, "other"},
		{"ru", 1, "one"},
		{"ru", 2, "few"},
		{"ru", 5, "many"},
		{"ru", 21, "one"},
		{"ru", 11, "many"},
		{"ru", 23, "few"},
	}
	for _, tc := range cases {
		t.Run(fmt.Sprintf("%s %d", tc.lang, tc.n), func(t *testing.T) {
			if got := pluralCategory(tc.lang, tc.n); got != tc.want {
				t.Errorf("pluralCategory(%q, %d) = %q, want %q", tc.lang, tc.n, got, tc.want)
			}
		})
	}
}

// TestPartialLocale parses a synthetic locale file, the shape a locale
// that is still being translated has: a few keys of its own, the rest left
// to the English template, and counted values in CLDR categories English
// itself has no form of. The test builds it from TOML text and points it
// at the embedded English template by hand, so the embedded set is not
// touched and no second locale file is planted.
func TestPartialLocale(t *testing.T) {
	const doc = `
[meta]
code = "ru"
name = "Synthetic"

[settings.category]
"Appearance" = "Vid"

[settings.value]
n_items = { one = "{n} item", few = "{n} itema", many = "{n} itemov" }
`
	l, _, err := parseLocale("synthetic.toml", []byte(doc))
	if err != nil {
		t.Fatalf("the synthetic locale does not parse: %v", err)
	}
	l.en = For("en")

	t.Run("its own key wins", func(t *testing.T) {
		if got, want := l.Category("Appearance"), "Vid"; got != want {
			t.Errorf("Category(Appearance) = %q, want %q", got, want)
		}
	})
	t.Run("a key it does not carry comes from en", func(t *testing.T) {
		cases := []struct {
			name string
			got  string
			want string
		}{
			{"category", l.Category("Daemon"), "Daemon"},
			{"label", l.Label("appearance.gap"), "Pane gap"},
			{"desc", l.Desc("appearance.gap"), "Cells of empty ground kept between two neighbouring tiled panes"},
			{"placeholder", l.Placeholder("appearance.preferred_shell"), "/bin/bash"},
			{"unset", l.Unset("appearance.preferred_shell"), "(auto-detect)"},
			{"value", l.Value("on"), "on"},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				if tc.got != tc.want {
					t.Errorf("got %q, want %q", tc.got, tc.want)
				}
			})
		}
	})
	t.Run("a counted value it carries takes its own forms", func(t *testing.T) {
		cases := []struct {
			n    int
			got  string
			want string
		}{
			{1, l.ValueN("n_items", 1), "1 item"},
			{2, l.ValueN("n_items", 2), "2 itema"},
			{5, l.ValueN("n_items", 5), "5 itemov"},
		}
		for _, tc := range cases {
			if tc.got != tc.want {
				t.Errorf("ValueN(n_items, %d) = %q, want %q", tc.n, tc.got, tc.want)
			}
		}
	})
	t.Run("a counted value it does not carry degrades to en other", func(t *testing.T) {
		// pluralCategory("ru", 2) is "few", a form the English entry of
		// n_sections has no word for, so the entry's "other" answers.
		if got, want := l.ValueN("n_sections", 2), "2 sections"; got != want {
			t.Errorf("ValueN(n_sections, 2) = %q, want %q", got, want)
		}
	})
	t.Run("keys and has answer over both dictionaries", func(t *testing.T) {
		categories := l.Keys("category")
		if !slices.Contains(categories, "Appearance") {
			t.Errorf("Keys(category) = %v, which does not hold the locale's own Appearance", categories)
		}
		if !slices.Contains(categories, "Daemon") {
			t.Errorf("Keys(category) = %v, which does not hold the template's Daemon", categories)
		}
		if !l.Has("value", "n_items") {
			t.Error("Has(value, n_items) is false: the locale carries the key's forms")
		}
		if !l.Has("value", "on") {
			t.Error("Has(value, on) is false: the template carries the word")
		}
		if l.Has("category", "Nope") || l.Has("value", "nope") {
			t.Error("a key neither dictionary carries answers Has true")
		}
	})
}

// TestBrokenLocale pins that a file that does not parse is an error the
// caller reports, not a panic the whole binary pays for.
func TestBrokenLocale(t *testing.T) {
	t.Run("invalid TOML is a parse error", func(t *testing.T) {
		if _, _, err := parseLocale("broken.toml", []byte("[meta]\ncode = ")); err == nil {
			t.Error("parseLocale accepted invalid TOML")
		}
	})
	t.Run("a missing meta code is a parse error", func(t *testing.T) {
		if _, _, err := parseLocale("broken.toml", []byte("[meta]\nname = \"Nope\"\n")); err == nil {
			t.Error("parseLocale accepted a locale with no code")
		}
	})
}

// TestKeysAndHas pins the introspection of the dictionaries: Keys lists
// what a section carries, sorted and fresh, and Has says whether a
// section holds a key. Both answer over the same union the lookups fall
// back through, so Has(section, key) is true exactly when Keys(section)
// holds the key.
func TestKeysAndHas(t *testing.T) {
	en := For("en")

	t.Run("desc keys hold the corpus, sorted", func(t *testing.T) {
		desc := en.Keys("desc")
		if !slices.Contains(desc, "appearance.language") {
			t.Errorf("Keys(desc) holds no appearance.language, an option the settings page carries")
		}
		if !slices.Contains(desc, "daemon.log_level") {
			t.Errorf("Keys(desc) holds no daemon.log_level, an option the settings page carries")
		}
		if !slices.IsSorted(desc) {
			t.Error("Keys(desc) is not sorted")
		}
	})
	t.Run("value keys count plural forms among the words", func(t *testing.T) {
		values := en.Keys("value")
		if !slices.Contains(values, "on") {
			t.Errorf("Keys(value) = %v, which does not hold the plain word on", values)
		}
		if !slices.Contains(values, "n_sections") {
			t.Errorf("Keys(value) = %v, which does not hold the forms of n_sections", values)
		}
	})
	t.Run("has agrees with keys", func(t *testing.T) {
		if !en.Has("label", "appearance.language") || !en.Has("desc", "appearance.language") {
			t.Error("Has holds no appearance.language, an option the corpus was seeded for")
		}
		if !en.Has("value", "on") || !en.Has("value", "n_sections") {
			t.Error("Has(value, on or n_sections) is false")
		}
		if en.Has("desc", "no.such_option") {
			t.Error("Has(desc, no.such_option) is true; no table carries that path")
		}
		if en.Has("value", "no_such_word") {
			t.Error("Has(value, no_such_word) is true; no table carries that key")
		}
	})
	t.Run("a section no table names answers empty, not a panic", func(t *testing.T) {
		if got := en.Keys("nope"); len(got) != 0 {
			t.Errorf("Keys(nope) = %v, want empty", got)
		}
		if en.Has("nope", "anything") {
			t.Error("Has(nope, anything) is true; no section of that name exists")
		}
	})
	t.Run("the slice is fresh on every call", func(t *testing.T) {
		first := en.Keys("hint")
		if len(first) == 0 {
			t.Fatal("Keys(hint) is empty; the corpus the test needs is gone")
		}
		first[0] = "mutated"
		if again := en.Keys("hint"); slices.Contains(again, "mutated") {
			t.Error("Keys(hint) hands out one slice twice: a caller's change reached the tables inside")
		}
	})
	t.Run("a nil Localizer answers for the English template", func(t *testing.T) {
		var l *Localizer
		if got := l.Keys("desc"); !slices.Contains(got, "appearance.language") {
			t.Errorf("a nil Keys(desc) holds no appearance.language; want the template's keys: %v", got)
		}
		if !l.Has("desc", "appearance.language") {
			t.Error("a nil Has(desc, appearance.language) is false; want the template's answer")
		}
		if l.Has("desc", "no.such_option") {
			t.Error("a nil Has(desc, no.such_option) is true; the template carries no such path")
		}
	})
}
