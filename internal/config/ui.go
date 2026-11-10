package config

// UIConfig is the [ui] table: the language the client's own prose reads in.
//
// It names one thing, prose, and prose only. A CLI line, a configuration path
// and a key label are things a person types, so they stay as they are in
// every language; the help pane keeps drawing them untranslated.
type UIConfig struct {
	// Language is a language code, "en" or "fr" today. Empty follows the
	// client's locale: LC_ALL, then LC_MESSAGES, then LANG.
	Language string `toml:"language"`
}

// UILanguages are the language codes this build answers to. Empty is the
// default and follows the locale.
var UILanguages = []string{"", "en", "fr"}
