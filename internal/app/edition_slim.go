//go:build slim

package app

import (
	"slices"

	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// slimHidden names the settings categories, palette entries, help tabs and
// prefix menu lines of the features tuios-slim leaves out, so none of them
// offers a control that does nothing. "Screenshot" and "Tape" are also the
// prefix menu lines and "Tape" the help tab.
var slimHidden = map[string]bool{
	// Settings categories.
	"Saver":      true,
	"Screenshot": true,
	"Tape":       true,
	"Hosts":      true,

	// Palette entries.
	"Start the screen saver":        true,
	"Screenshot this window":        true,
	"Screenshot a region":           true,
	"Screenshot the screen":         true,
	"Tape: review the project tape": true,
	"Open the tape manager":         true,
}

// slimSettingsCategories leaves out the categories in slimHidden.
func slimSettingsCategories(c []settingsCategory) []settingsCategory {
	return slices.DeleteFunc(c, func(c settingsCategory) bool { return slimHidden[c.Name] })
}

// slimPaletteItems leaves out the entries in slimHidden.
func slimPaletteItems(items []CommandPaletteItem) []CommandPaletteItem {
	return slices.DeleteFunc(items, func(it CommandPaletteItem) bool { return slimHidden[it.Name] })
}

// slimPrefixBindings leaves out the prefix menu lines in slimHidden.
func slimPrefixBindings(b []config.Keybinding) []config.Keybinding {
	return slices.DeleteFunc(b, func(k config.Keybinding) bool { return slimHidden[k.Description] })
}

// slimHelpCategories leaves out the help tabs in slimHidden.
func slimHelpCategories(c []HelpCategory) []HelpCategory {
	return slices.DeleteFunc(c, func(c HelpCategory) bool { return slimHidden[c.Name] })
}
