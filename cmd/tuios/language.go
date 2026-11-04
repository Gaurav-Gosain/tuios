package main

import (
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/i18n"
)

// initLanguage selects the catalog every string in this process is drawn from.
//
// It runs at the top of main, before any command, because the strings it
// governs include the ones printed when something has already failed: a user
// who set the language should not read the reason their session would not start
// in a different one.
//
// A config that cannot be read is not fatal here. This is the earliest point in
// the process and the config may be the very thing that is broken, so the
// failure leaves English in place, which is what the messages are written in,
// and the command reports the config problem itself a moment later.
func initLanguage() {
	var configured string
	if cfg, err := config.LoadUserConfig(); err == nil && cfg != nil {
		configured = cfg.LanguageTag()
	}
	i18n.SetLanguage(i18n.FromEnvAndConfig(configured))
}
