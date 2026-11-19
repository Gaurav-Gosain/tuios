package app

import "github.com/Gaurav-Gosain/tuios/internal/i18n"

// loc is the localizer the settings page draws its words through: the language
// the session's config names, English for the empty code a bare model holds.
// Every row asks for its label, description and values through it, so a
// language change is a config change and nothing else.
func (m *OS) loc() *i18n.Localizer { return i18n.For(m.Settings.Language) }

// settingsCat builds a category. Key is the stable English name the page's
// entry points address the tab by (see OpenSettingsAt); the drawn name is that
// key as the session's language spells it.
func (m *OS) settingsCat(key string, items []settingItem) settingsCategory {
	return settingsCategory{Key: key, Name: m.loc().Category(key), Items: items}
}
