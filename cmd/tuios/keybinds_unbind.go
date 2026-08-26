package main

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// The command-line half of unbinding. The overlay is the discoverable way to do
// this; these exist so a dotfile script or an agent can do it without driving a
// TUI, and so the two paths write the same thing to config.toml.
//
// Both write an empty list on the actions that ran out of keys, which is what
// stops the default coming back at the next load. See
// internal/config/keybind_unbind.go for the encoding.

// keybindsUnbind takes keys off one action. With no key named it takes them
// all, which is how an action is left with nothing.
func keybindsUnbind(action, key string) error {
	cfg, err := loadConfigForEdit()
	if err != nil {
		return err
	}

	section := keybindSectionOf(cfg, action)
	if section == "" {
		return &diagnosticError{
			What:  fmt.Sprintf("No action is called %q.", action),
			Cause: "The action name has to match the one in config.toml exactly.",
			Fix:   "Run `tuios keybinds doctor` to list every action and the table it lives in.",
			Extra: nearestActionHint(cfg, action),
		}
	}

	var removed []config.Removal
	if key == "" {
		removed, _ = cfg.Keybindings.UnbindAction(section, action)
	} else if r, ok := cfg.Keybindings.UnbindKey(section, action, key); ok {
		removed = []config.Removal{r}
	}

	if len(removed) == 0 {
		if key == "" {
			fmt.Printf("%s already has no key.\n", action)
		} else {
			fmt.Printf("%s is not bound to %s.\n", action, key)
		}
		return nil
	}

	if err := config.SaveUserConfig(cfg); err != nil {
		return fmt.Errorf("failed to save config: %w", err)
	}

	var keys []string
	for _, r := range removed {
		keys = append(keys, r.Key)
	}
	fmt.Printf("Took %s off %s in [keybindings.%s].\n", strings.Join(keys, ", "), action, section)
	if left := cfg.Keybindings.SectionFor(section)[action]; len(left) == 0 {
		fmt.Printf("config.toml now says %s = [], so the default stays off.\n", action)
	} else {
		fmt.Printf("%s is still on %s.\n", action, strings.Join(left, ", "))
	}
	reportRunningDaemon()
	return nil
}

// keybindsFree takes one key off every action, so the program in the pane gets
// it.
func keybindsFree(key string) error {
	cfg, err := loadConfigForEdit()
	if err != nil {
		return err
	}
	registry := config.NewKeybindRegistry(cfg)
	removed := cfg.Keybindings.FreeKey(key)

	if len(removed) > 0 {
		if err := config.SaveUserConfig(cfg); err != nil {
			return fmt.Errorf("failed to save config: %w", err)
		}
		fmt.Printf("Took %s off %d action(s):\n", key, len(removed))
		for _, r := range removed {
			fmt.Printf("  %-28s [keybindings.%s]%s\n", r.Action, r.Section, unboundNote(r))
		}
	} else {
		fmt.Printf("No action was bound to %s.\n", key)
	}

	// Read after the edit, so what is reported is what the config now says.
	registry.Reload(cfg)
	if held := registry.StillHeldBy(key); len(held) > 0 {
		fmt.Printf("\n%s still does not reach the pane: %s.\n", key, strings.Join(held, ", and "))
		return nil
	}
	fmt.Printf("\nThe program in the pane gets %s now.\n", key)
	reportRunningDaemon()
	return nil
}

// unboundNote marks the actions that ran out of keys, since those are the ones
// the file now spells as an empty list.
func unboundNote(r config.Removal) string {
	if r.LeftUnbound {
		return "  (now unbound)"
	}
	return ""
}

// loadConfigForEdit loads the config the way the app does. A load failure is
// fatal here rather than falling back to the defaults: writing the defaults
// over a config that failed to parse would throw the user's file away.
func loadConfigForEdit() (*config.UserConfig, error) {
	cfg, err := config.LoadUserConfig()
	if err != nil {
		return nil, &diagnosticError{
			What:  "The config file could not be read.",
			Cause: err.Error(),
			Fix:   "Run `tuios config path` to find it, then fix the error it reports.",
		}
	}
	return cfg, nil
}

// keybindSectionOf is the config table an action lives in, or "".
func keybindSectionOf(cfg *config.UserConfig, action string) string {
	for _, name := range config.SectionNames() {
		if _, ok := cfg.Keybindings.SectionFor(name)[action]; ok {
			return name
		}
	}
	return ""
}

// nearestActionHint is the closest action name to a misspelled one, as a line
// to print under the error.
func nearestActionHint(cfg *config.UserConfig, action string) []string {
	var names []string
	for _, name := range config.SectionNames() {
		for a := range cfg.Keybindings.SectionFor(name) {
			names = append(names, a)
		}
	}
	sort.Strings(names)
	if near := closestName(action, names); near != "" {
		return []string{"Did you mean " + near + "?"}
	}
	return nil
}

// reportRunningDaemon says what a change means for panes that are already open.
// The config is read at startup, so a daemon that is already running goes on
// using what it read.
func reportRunningDaemon() {
	fmt.Fprintln(os.Stdout, "Open sessions keep the keys they started with. Reload the config from the command palette, or restart tuios.")
}
