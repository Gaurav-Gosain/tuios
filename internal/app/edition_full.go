//go:build !slim

package app

import "github.com/Gaurav-Gosain/tuios/internal/config"

// The full build leaves out no settings category and no palette entry. See
// edition_slim.go.

func slimSettingsCategories(c []settingsCategory) []settingsCategory { return c }

func slimPaletteItems(items []CommandPaletteItem) []CommandPaletteItem { return items }

func slimPrefixBindings(b []config.Keybinding) []config.Keybinding { return b }

func slimHelpCategories(c []HelpCategory) []HelpCategory { return c }
