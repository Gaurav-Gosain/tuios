package i18n

import (
	"reflect"
	"regexp"
	"testing"
)

// verb matches one format verb, so the tests can compare the verbs a key has in
// each language. A translator who drops a %s, or turns %d into %s, produces a
// line with an argument missing or two arguments rendered as one, and nothing
// else in the program reports it: Sprintf writes %!s(MISSING) into the pane and
// the interface carries on. This is the only place that can catch it.
var verb = regexp.MustCompile(`%[-+# 0]*[0-9]*(?:\.[0-9]+)?[a-zA-Z%]`)

func verbs(s string) []string {
	var out []string
	for _, m := range verb.FindAllString(s, -1) {
		if m == "%%" {
			continue
		}
		out = append(out, m)
	}
	return out
}

// TestCatalogs checks the invariants that make a table safe to ship: every key
// in a translation exists in English, and its format verbs match.
func TestCatalogs(t *testing.T) {
	for lang, table := range catalogs {
		if lang == English {
			continue
		}
		for key, translated := range table {
			source, ok := english[key]
			if !ok {
				t.Errorf("%s: key %q has no English entry, so nothing can fall back to it", lang, key)
				continue
			}
			want, got := verbs(source), verbs(translated)
			if !reflect.DeepEqual(want, got) {
				t.Errorf("%s: key %q has verbs %v, English has %v", lang, key, got, want)
			}
		}
	}
}

// TestEnglishIsComplete guards the fallback: a key absent from English renders
// as itself, which is how a caller finds it, but no key in a translation may be
// the only one that has it.
func TestEnglishIsComplete(t *testing.T) {
	for _, table := range catalogs {
		for key := range table {
			if _, ok := english[key]; !ok {
				t.Errorf("key %q is missing from English", key)
			}
		}
	}
}

// TestFallback covers the two fallbacks: an untranslated key renders English,
// and a key nobody has renders as itself.
func TestFallback(t *testing.T) {
	SetLanguage(English)
	t.Cleanup(func() { SetLanguage(English) })

	SetLanguage(SimplifiedChinese)
	if got := T("cli.label.fix"); got != "解决办法：" {
		t.Errorf("translated key: got %q", got)
	}

	// A key only English has.
	delete(simplifiedChinese, "cli.common.and_more")
	t.Cleanup(func() { simplifiedChinese["cli.common.and_more"] = "另有 %d 个" })
	if got := T("cli.common.and_more", 3); got != "and 3 more" {
		t.Errorf("untranslated key: got %q, want the English", got)
	}

	if got := T("no.such.key"); got != "no.such.key" {
		t.Errorf("unknown key: got %q, want the key back", got)
	}
}

// TestParse covers the spellings a locale setting actually produces. Chinese
// appears as zh, zh_CN, zh-Hans and zh-CN depending on who wrote it, and all of
// them must select the one Chinese catalog there is.
func TestParse(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want Language
	}{
		{"", English},
		{"en", English},
		{"en-US", English},
		{"zh", SimplifiedChinese},
		{"zh-CN", SimplifiedChinese},
		{"zh_CN", SimplifiedChinese},
		{"zh-Hans", SimplifiedChinese},
		{"ZH-cn", SimplifiedChinese},
		{"  zh-CN  ", SimplifiedChinese},
		{"de", English}, // no catalog, and a wrong language beats a failed start
	} {
		if got := Parse(tc.in); got != tc.want {
			t.Errorf("Parse(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestFromEnvAndConfig covers the precedence: the environment is the more
// specific statement and wins over the config.
func TestFromEnvAndConfig(t *testing.T) {
	t.Setenv(EnvVar, "")
	if got := FromEnvAndConfig("zh-CN"); got != SimplifiedChinese {
		t.Errorf("config only: got %q", got)
	}
	t.Setenv(EnvVar, "en")
	if got := FromEnvAndConfig("zh-CN"); got != English {
		t.Errorf("env over config: got %q", got)
	}
}

// TestCount covers inflection. Chinese has no plural suffix, so counting a
// translated noun must not append an s the way plural.Count does.
func TestCount(t *testing.T) {
	SetLanguage(English)
	t.Cleanup(func() { SetLanguage(English) })
	if got := Count(1, "session"); got != "1 session" {
		t.Errorf("English one: got %q", got)
	}
	if got := Count(2, "session"); got != "2 sessions" {
		t.Errorf("English many: got %q", got)
	}

	SetLanguage(SimplifiedChinese)
	if got := Count(1, "会话"); got != "1 会话" {
		t.Errorf("Chinese one: got %q", got)
	}
	if got := Count(2, "会话"); got != "2 会话" {
		t.Errorf("Chinese many: got %q, want no suffix", got)
	}
}

// TestSetLanguageRejectsUnknown covers the guard: a tag with no catalog leaves
// the previous language rather than leaving T with nothing to read.
func TestSetLanguageRejectsUnknown(t *testing.T) {
	SetLanguage(English)
	t.Cleanup(func() { SetLanguage(English) })
	SetLanguage("kl-GL")
	if got := Current(); got != English {
		t.Errorf("unknown language: got %q, want English", got)
	}
}
