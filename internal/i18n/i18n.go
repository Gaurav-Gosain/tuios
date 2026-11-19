// Package i18n holds the interface strings of the settings page as locale
// dictionaries embedded in the binary, one TOML file per language. Every key
// is looked up in the language in force first and in the English template
// second, so a locale that is missing a key stays readable rather than
// blank.
package i18n

import (
	"cmp"
	"embed"
	"errors"
	"io/fs"
	"log"
	"slices"
	"strconv"
	"strings"
	"sync"

	"github.com/Gaurav-Gosain/tuios/internal/errfmt"
	"github.com/pelletier/go-toml/v2"
)

// localeFiles holds every locale dictionary the binary ships, one TOML file
// per language, filed under its own code.
//
//go:embed locales/*.toml
var localeFiles embed.FS

// Language is one locale the binary carries: the code the config names the
// language by, and the human name its locale file gives in [meta].
type Language struct {
	Code string
	Name string
}

// sectionTables are the [settings.<name>] tables that hold one string per
// key in every locale. The value table is built separately, because it mixes
// plain words with tables of plural forms.
var sectionTables = []string{
	"category",
	"label",
	"desc",
	"placeholder",
	"unset",
	"row",
	"hint",
	"notify",
}

// Localizer answers the interface strings of one language: the tables of its
// own locale, with the English template beside them for the keys its locale
// does not carry. A nil *Localizer answers from the English template,
// because the settings page calls its Localizer on every frame and a model
// built bare must not panic on the way there.
type Localizer struct {
	lang     string
	sections map[string]map[string]string
	words    map[string]string
	forms    map[string]map[string]string
	en       *Localizer
}

// load parses every embedded locale once and hands out the parsed tables.
// The files are part of the binary, so a file that does not parse is a
// broken build; the callers still owe every user an answer, so a broken
// file is reported as a warning and skipped, and English is always present:
// the parsed template, or an empty one when nothing parsed.
var load = sync.OnceValues(func() (map[string]*Localizer, []Language) {
	byCode, langs, err := parseAll()
	if err != nil {
		log.Printf("Warning: the embedded locale dictionaries: %v", err)
	}
	if _, ok := byCode["en"]; !ok {
		log.Printf("Warning: no English locale template is embedded; every lookup falls back to its key")
		byCode["en"] = &Localizer{lang: "en"}
	}
	en := byCode["en"]
	for _, l := range byCode {
		if l != en {
			l.en = en
		}
	}
	return byCode, langs
})

// parseAll reads every embedded locale file. Each file is parsed on its own,
// so one broken file costs its own language and no other: the file is
// skipped, its error joins the rest, and the caller reports the lot once.
// A code a second file claims is a parse error of the second file.
func parseAll() (map[string]*Localizer, []Language, error) {
	names, err := fs.Glob(localeFiles, "locales/*.toml")
	if err != nil {
		return nil, nil, errfmt.Errorf("list the embedded locales: %w", err)
	}
	byCode := make(map[string]*Localizer, len(names))
	langs := make([]Language, 0, len(names))
	var errs []error
	for _, name := range names {
		data, err := localeFiles.ReadFile(name)
		if err != nil {
			errs = append(errs, errfmt.Errorf("read the embedded locale %s: %w", name, err))
			continue
		}
		l, lang, err := parseLocale(name, data)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if _, dup := byCode[lang.Code]; dup {
			errs = append(errs, errfmt.Errorf(
				"the locale %s claims the code %q, which another file here already claims", name, lang.Code))
			continue
		}
		byCode[lang.Code] = l
		langs = append(langs, lang)
	}
	slices.SortFunc(langs, func(a, b Language) int { return cmp.Compare(a.Code, b.Code) })
	return byCode, langs, errors.Join(errs...)
}

// Languages returns every locale embedded in the binary, sorted by code.
func Languages() []Language {
	_, langs := load()
	return slices.Clone(langs)
}

// Codes returns the code of every embedded locale, sorted, the values the
// config's appearance.language accepts.
func Codes() []string {
	_, langs := load()
	codes := make([]string, len(langs))
	for i, lang := range langs {
		codes[i] = lang.Code
	}
	return codes
}

// For returns the Localizer of lang. An empty lang, or a code the binary
// does not carry, gives English. For never returns nil.
func For(lang string) *Localizer {
	byCode, _ := load()
	if l, ok := byCode[lang]; ok {
		return l
	}
	return byCode["en"]
}

// english returns the English template, the dictionary a nil Localizer
// answers from.
func english() *Localizer {
	byCode, _ := load()
	return byCode["en"]
}

// Lang returns the code of the language the Localizer answers in, "en" when
// it answers from the English template.
func (l *Localizer) Lang() string {
	if l == nil {
		return "en"
	}
	return l.lang
}

// Category returns the name of one category of settings, from the
// [settings.category] table. A key no table carries is the key itself,
// which still reads, because the categories are named by their keys.
func (l *Localizer) Category(key string) string {
	if s := l.section("category", key); s != "" {
		return s
	}
	return key
}

// Label returns the label of a config path, from the [settings.label]
// table. A path no table carries falls back to the name inferred from the
// path: its last segment with the underscores opened out and the first
// letter capitalised, the scheme the settings page drew before it had a
// dictionary.
func (l *Localizer) Label(path string) string {
	if s := l.section("label", path); s != "" {
		return s
	}
	return inferredLabel(path)
}

// Desc returns the description of a config path, from the [settings.desc]
// table.
func (l *Localizer) Desc(path string) string {
	return l.section("desc", path)
}

// Placeholder returns the placeholder a text option shows while it is
// empty, from the [settings.placeholder] table.
func (l *Localizer) Placeholder(path string) string {
	return l.section("placeholder", path)
}

// Unset returns the text an option with no value shows, from the
// [settings.unset] table. A path no table carries shows "(default)".
func (l *Localizer) Unset(path string) string {
	if s := l.section("unset", path); s != "" {
		return s
	}
	return "(default)"
}

// Row returns one whole line of the settings page, from the [settings.row]
// table.
func (l *Localizer) Row(key string) string {
	return l.section("row", key)
}

// Rowf returns a row with its placeholders filled. subs is name, value
// pairs: a "{name}" in the row takes the value after its name, and a
// placeholder no pair names stays as written.
func (l *Localizer) Rowf(key string, subs ...string) string {
	return interpolate(l.section("row", key), subs...)
}

// Value returns one word or short value name, from the [settings.value]
// table.
func (l *Localizer) Value(key string) string {
	if l == nil {
		l = english()
	}
	if v, ok := l.words[key]; ok {
		return v
	}
	if l.en != nil {
		if v, ok := l.en.words[key]; ok {
			return v
		}
	}
	return ""
}

// Valuef returns a value with its placeholders filled, taking the same
// name, value pairs as Rowf.
func (l *Localizer) Valuef(key string, subs ...string) string {
	return interpolate(l.Value(key), subs...)
}

// ValueN returns a count-carrying value from the [settings.value] table,
// where a key is either a word that takes "{n}", or a table of plural
// forms, one per CLDR category, such as n_sections = { one = "{n} section",
// other = "{n} sections" }. The form is the one pluralCategory picks for n,
// then the entry's "other", then both again in the English template, and
// the count always fills "{n}". A key no table carries is the key itself.
func (l *Localizer) ValueN(key string, n int) string {
	if l == nil {
		l = english()
	}
	count := strconv.Itoa(n)
	for _, loc := range []*Localizer{l, l.en} {
		if loc == nil {
			continue
		}
		if t, ok := loc.forms[key][pluralCategory(l.lang, n)]; ok {
			return interpolate(t, "n", count)
		}
		if t, ok := loc.forms[key]["other"]; ok {
			return interpolate(t, "n", count)
		}
		if t, ok := loc.words[key]; ok {
			return interpolate(t, "n", count)
		}
	}
	return key
}

// Hint returns one hint line of the settings page, from the [settings.hint]
// table. A key no table carries is the key itself, which still reads as a
// hint of what the key names.
func (l *Localizer) Hint(key string) string {
	if s := l.section("hint", key); s != "" {
		return s
	}
	return key
}

// Notify returns one notification, from the [settings.notify] table.
func (l *Localizer) Notify(key string) string {
	return l.section("notify", key)
}

// Notifyf returns a notification with its placeholders filled, taking the
// same name, value pairs as Rowf.
func (l *Localizer) Notifyf(key string, subs ...string) string {
	return interpolate(l.section("notify", key), subs...)
}

// Keys returns the sorted keys of one section of the dictionary: "category",
// "label", "desc", "placeholder", "unset", "row", "value", "hint" or
// "notify". For "value" the keys are the entries of the value table, a key
// of plural forms among them. A key the language in force shares with the
// English template counts once, and a key only the template carries is a
// key too, because a lookup in either is a key the settings page can draw.
// A section no dictionary carries answers an empty slice, not a panic, and
// a nil receiver answers for the English template. The slice is fresh on
// every call, so a caller may keep or change it without touching the tables
// inside.
func (l *Localizer) Keys(section string) []string {
	if l == nil {
		l = english()
	}
	seen := make(map[string]struct{})
	add := func(table map[string]string) {
		for key := range table {
			seen[key] = struct{}{}
		}
	}
	if section == "value" {
		for key := range l.words {
			seen[key] = struct{}{}
		}
		for key := range l.forms {
			seen[key] = struct{}{}
		}
		if l.en != nil {
			for key := range l.en.words {
				seen[key] = struct{}{}
			}
			for key := range l.en.forms {
				seen[key] = struct{}{}
			}
		}
	} else {
		add(l.sections[section])
		if l.en != nil {
			add(l.en.sections[section])
		}
	}
	keys := make([]string, 0, len(seen))
	for key := range seen {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	return keys
}

// Has says whether the dictionary carries a key in one section, the
// sections Keys names. A key only the English template carries counts,
// because every lookup falls back to the template, so Has(section, key) is
// true exactly when Keys(section) holds the key. For "value", a key of
// plural forms counts as much as a plain word. A nil receiver answers for
// the English template.
func (l *Localizer) Has(section, key string) bool {
	if l == nil {
		l = english()
	}
	for _, loc := range []*Localizer{l, l.en} {
		if loc == nil {
			continue
		}
		if section == "value" {
			if _, ok := loc.words[key]; ok {
				return true
			}
			if _, ok := loc.forms[key]; ok {
				return true
			}
			continue
		}
		if _, ok := loc.sections[section][key]; ok {
			return true
		}
	}
	return false
}

// section looks a key up in one [settings.<name>] table of the language in
// force, then in the English template. A nil receiver answers from English.
func (l *Localizer) section(name, key string) string {
	if l == nil {
		l = english()
	}
	if v, ok := l.sections[name][key]; ok {
		return v
	}
	if l.en != nil {
		if v, ok := l.en.sections[name][key]; ok {
			return v
		}
	}
	return ""
}

// inferredLabel names a config path the dictionary does not carry: its
// last segment, with underscores opened out and the first letter
// capitalised. A path with no segment to name answers the path itself.
func inferredLabel(path string) string {
	segment := path
	if i := strings.LastIndex(path, "."); i >= 0 {
		segment = path[i+1:]
	}
	words := strings.ReplaceAll(segment, "_", " ")
	if words == "" {
		return path
	}
	return strings.ToUpper(words[:1]) + words[1:]
}

// interpolate fills the "{name}" placeholders of a template from subs,
// given as name, value pairs. A placeholder no pair names stays as written.
func interpolate(t string, subs ...string) string {
	for i := 0; i+1 < len(subs); i += 2 {
		t = strings.ReplaceAll(t, "{"+subs[i]+"}", subs[i+1])
	}
	return t
}

// parseLocale reads one locale file into a Localizer and the Language its
// [meta] section names. The value table is the one that mixes types: a
// plain entry is a word, a table of entries is a key's plural forms.
func parseLocale(name string, data []byte) (*Localizer, Language, error) {
	var doc map[string]any
	if err := toml.Unmarshal(data, &doc); err != nil {
		return nil, Language{}, errfmt.Errorf("parse the locale %s: %w", name, err)
	}
	code, langName, err := localeMeta(name, doc)
	if err != nil {
		return nil, Language{}, err
	}
	set, _ := doc["settings"].(map[string]any)
	l := &Localizer{
		lang:     code,
		sections: make(map[string]map[string]string, len(sectionTables)),
		words:    map[string]string{},
		forms:    map[string]map[string]string{},
	}
	for _, table := range sectionTables {
		entries, err := textTable(name, "settings."+table, set[table])
		if err != nil {
			return nil, Language{}, err
		}
		l.sections[table] = entries
	}
	values, _ := set["value"].(map[string]any)
	for key, v := range values {
		switch v := v.(type) {
		case string:
			l.words[key] = v
		case map[string]any:
			forms, err := textTable(name, "settings.value."+key, v)
			if err != nil {
				return nil, Language{}, err
			}
			l.forms[key] = forms
		default:
			return nil, Language{}, errfmt.Errorf(
				"the locale %s: settings.value.%s must be text or a table of plural forms", name, key)
		}
	}
	return l, Language{Code: code, Name: langName}, nil
}

// localeMeta reads the [meta] section of a locale file: the code the config
// names the language by, and its human name.
func localeMeta(name string, doc map[string]any) (string, string, error) {
	meta, _ := doc["meta"].(map[string]any)
	code, _ := meta["code"].(string)
	if code == "" {
		return "", "", errfmt.Errorf("the locale %s: its [meta] carries no code", name)
	}
	langName, _ := meta["name"].(string)
	return code, langName, nil
}

// textTable copies a table of TOML entries into a string table, rejecting
// any entry that is not text. where names the table in the error.
func textTable(name, where string, raw any) (map[string]string, error) {
	entries, _ := raw.(map[string]any)
	table := make(map[string]string, len(entries))
	for key, v := range entries {
		s, ok := v.(string)
		if !ok {
			return nil, errfmt.Errorf("the locale %s: %s.%s must be text", name, where, key)
		}
		table[key] = s
	}
	return table, nil
}
