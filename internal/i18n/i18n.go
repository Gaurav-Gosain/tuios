// Package i18n holds the strings the interface prints and the table per
// language they are written in.
//
// The table is a plain map and not a translation library. tuios is one binary
// that starts in milliseconds and cross-compiles to nine targets; an i18n
// runtime brings loader machinery, file formats and a dependency tree for what
// is, here, a few hundred sentences. A map is checked by the compiler, ships in
// the binary, and cannot fail to load a file at run time.
//
// Two rules keep a translation honest as the English moves on:
//
//   - English is the source of truth. A key with no entry in the selected
//     language renders its English, so a translation that lags behind shows an
//     English line rather than an empty one or a raw key.
//   - A key's format verbs must match between languages. TestCatalogs checks
//     it, because a translator who drops a %s produces a line with the argument
//     missing and no error anywhere.
package i18n

import (
	"fmt"
	"os"
	"strings"
	"sync"
)

// Language is a language tag, spelled the way BCP 47 spells it: "en", "zh-CN".
// The tag is what a config file writes and what a diagnostic prints, so it is
// the identity of a catalog and not a display name.
type Language string

// The languages with a catalog. Adding one means adding its table to catalogs
// and its tag to Languages; nothing else in the code changes.
const (
	English           Language = "en"
	SimplifiedChinese Language = "zh-CN"
)

// languages is every tag with a catalog, English first. The order is the order
// a settings screen offers them.
var languages = []Language{English, SimplifiedChinese}

// Languages returns the tags that have a catalog.
func Languages() []Language { return append([]Language(nil), languages...) }

// EnvVar is the environment variable that overrides the configured language.
// It exists so a shell can try a language without editing the config, and so a
// wrapper can pin one for a session.
const EnvVar = "TUIOS_LANG"

// Parse reads a language tag into the Language it names, or English when it
// names none.
//
// It accepts what people and locale settings actually write: "zh", "zh_CN",
// "zh-Hans", "zh-CN", in any case. A tag with no catalog falls back to English
// rather than failing, because a wrong language is a cosmetic problem and a
// failed start is not.
func Parse(tag string) Language {
	normalized := strings.ToLower(strings.ReplaceAll(strings.TrimSpace(tag), "_", "-"))
	if normalized == "" {
		return English
	}
	for _, l := range languages {
		if strings.ToLower(string(l)) == normalized {
			return l
		}
	}
	// A region-less or script-qualified Chinese tag still means Chinese.
	if strings.HasPrefix(normalized, "zh") {
		return SimplifiedChinese
	}
	return English
}

// FromEnvAndConfig picks the language for this process: the environment
// variable when it is set, the configured value otherwise.
//
// The environment wins because it is the more specific statement. A user who
// types TUIOS_LANG for one command means that command, and the config is the
// standing answer a shell is overriding.
func FromEnvAndConfig(configured string) Language {
	if tag := os.Getenv(EnvVar); tag != "" {
		return Parse(tag)
	}
	return Parse(configured)
}

var (
	mu      sync.RWMutex
	current = English
)

// SetLanguage selects the catalog T reads from. Call it once, before anything
// renders.
//
// It is process-wide because the strings are: a pane's title, a dialog and a
// CLI diagnostic can all be drawn by different packages in the same frame, and
// a language passed down that whole call graph would touch every signature
// between them to say one thing.
func SetLanguage(l Language) {
	if _, ok := catalogs[l]; !ok {
		l = English
	}
	mu.Lock()
	current = l
	mu.Unlock()
}

// Current is the language T reads from.
func Current() Language {
	mu.RLock()
	defer mu.RUnlock()
	return current
}

// T returns the string for key in the current language, with args applied
// through fmt.Sprintf when any are given.
//
// A key missing from the selected catalog renders its English, and a key
// missing from English renders the key itself. The second case is a bug in the
// caller rather than a translation gap, and showing the key is what makes it
// findable.
func T(key string, args ...any) string {
	return TIn(Current(), key, args...)
}

// TIn is T in a named language. It is what the tests and the settings preview
// use, where the answer must not depend on what the process happens to be set
// to.
func TIn(l Language, key string, args ...any) string {
	s, ok := catalogs[l][key]
	if !ok {
		s, ok = catalogs[English][key]
	}
	if !ok {
		s = key
	}
	if len(args) == 0 {
		return s
	}
	return fmt.Sprintf(s, args...)
}

// Inflects reports whether a noun in this language takes a plural suffix.
//
// English does, and needs plural.Count for it. Chinese does not: 会话 is both
// one session and two, so appending an s the way plural.Count does would print
// "2 会话s". Code that counts something visible translates the noun and asks
// here, rather than calling plural.Count on a translated word.
func (l Language) Inflects() bool { return l != SimplifiedChinese }

// Count writes n and a noun in the current language, adding the language's
// plural suffix when it has one.
//
// It is the counting path for a translated noun. plural.Count stays as it is,
// for the English nouns still in the source, and the two agree on English.
func Count[N integer](n N, noun string) string {
	if Current().Inflects() && n != 1 {
		return fmt.Sprint(n) + " " + noun + "s"
	}
	return fmt.Sprint(n) + " " + noun
}

// integer is any count, the same set plural.Integer takes.
type integer interface {
	~int | ~int8 | ~int16 | ~int32 | ~int64 | ~uint | ~uint8 | ~uint16 | ~uint32 | ~uint64
}
