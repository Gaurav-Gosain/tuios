package main

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/errfmt"
	"github.com/Gaurav-Gosain/tuios/internal/plural"
	"github.com/Gaurav-Gosain/tuios/internal/session"
)

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

	note, err := config.SaveUserConfig(cfg)
	if err != nil {
		return errfmt.Errorf("failed to save config: %w", err)
	}
	printWriteNote(note)

	var keys []string
	for _, r := range removed {
		keys = append(keys, r.Key)
	}
	fmt.Printf("Took %s off %s in [keybindings.%s].\n", strings.Join(keys, ", "), action, section)
	if left := cfg.Keybindings.SectionFor(section)[action]; len(left) == 0 {
		fmt.Printf("The config now says %s = [], so the default stays off.\n", action)
	} else {
		fmt.Printf("%s is still on %s.\n", action, strings.Join(left, ", "))
	}
	reportRunningDaemon()
	return nil
}

func keybindsFree(key string) error {
	cfg, err := loadConfigForEdit()
	if err != nil {
		return err
	}
	registry := config.NewKeybindRegistry(cfg)
	removed := cfg.Keybindings.FreeKey(key)

	if len(removed) > 0 {
		note, err := config.SaveUserConfig(cfg)
		if err != nil {
			return errfmt.Errorf("failed to save config: %w", err)
		}
		printWriteNote(note)
		fmt.Printf("Took %s off %s:\n", key, plural.Count(len(removed), "action"))
		for _, r := range removed {
			fmt.Printf("  %-28s [keybindings.%s]%s\n", r.Action, r.Section, unboundNote(r))
		}
	} else {
		fmt.Printf("No action was bound to %s.\n", key)
	}

	registry.Reload(cfg)
	if held := registry.StillHeldBy(key); len(held) > 0 {
		fmt.Printf("\n%s still does not reach the pane: %s.\n", key, strings.Join(held, ", and "))
		return nil
	}
	fmt.Printf("\nThe program in the pane gets %s now.\n", key)
	reportRunningDaemon()
	return nil
}

func unboundNote(r config.Removal) string {
	if r.LeftUnbound {
		return "  (now unbound)"
	}
	return ""
}

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

func keybindSectionOf(cfg *config.UserConfig, action string) string {
	for _, name := range cfg.Keybindings.SectionNamesFor() {
		if _, ok := cfg.Keybindings.SectionFor(name)[action]; ok {
			return name
		}
	}
	return ""
}

func nearestActionHint(cfg *config.UserConfig, action string) []string {
	var names []string
	for _, name := range cfg.Keybindings.SectionNamesFor() {
		for a := range cfg.Keybindings.SectionFor(name) {
			names = append(names, a)
		}
	}
	sort.Strings(names)
	if near := session.ClosestMatch(action, names); near != "" {
		return []string{"Did you mean " + near + "?"}
	}
	return nil
}

func reportRunningDaemon() {
	fmt.Fprintln(os.Stdout, "Open sessions keep the keys they started with. Reload the config from the command palette, or restart tuios.")
}
