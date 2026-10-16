package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// keybindsPreset prints the preset in use, or sets one. Setting a preset
// writes keybindings.preset and empties keybindings.leader_key, so the leader
// is the new preset's: a leader set for screen's ctrl+a must not follow the
// person to tmux without them knowing why.
func keybindsPreset(name string) error {
	cfg, err := loadConfigForEdit()
	if err != nil {
		return err
	}
	current := cfg.Keybindings.Preset
	if current == "" {
		current = config.PresetTuios
	}
	if name == "" {
		fmt.Printf("%s (leader %s)\n", current, cfg.Keybindings.LeaderKey)
		return nil
	}
	if _, err := config.PresetInfo(name); err != nil {
		return &diagnosticError{
			What: err.Error() + ".",
			Fix:  "Run `tuios keybinds preset list` for the presets.",
		}
	}
	if name == current {
		fmt.Printf("The %s keys are in use already.\n", name)
		return nil
	}
	cfg.Keybindings.Preset = name
	if name == config.PresetTuios {
		cfg.Keybindings.Preset = ""
	}
	cfg.Keybindings.LeaderKey = ""
	note, err := config.SaveUserConfig(cfg)
	if err != nil {
		return fmt.Errorf("failed to save config: %w", err)
	}
	printWriteNote(note)
	leader := config.DefaultKeybindingsFor(name).LeaderKey
	fmt.Printf("The %s keys are in use. The leader is %s.\n", name, leader)
	if name != config.PresetTuios {
		fmt.Printf("Press %s then ctrl+t for the tuios keys that %s moved.\n", leader, name)
		fmt.Printf("Run `tuios keybinds cheatsheet` to see what changed.\n")
	}
	return nil
}

// keybindsPresetList prints every preset, one line each.
func keybindsPresetList() error {
	for _, name := range config.Presets() {
		about, err := config.PresetInfo(name)
		if err != nil {
			return err
		}
		leader := config.DefaultKeybindingsFor(name).LeaderKey
		samples := make([]string, len(about.Samples))
		for i, s := range about.Samples {
			samples[i] = leader + " " + s
		}
		fmt.Printf("%-8s %-24s %s\n", name, about.Summary, strings.Join(samples, "   "))
	}
	return nil
}

// keybindsPresetShow prints a preset's file as tuios ships it.
func keybindsPresetShow(name string) error {
	data, err := config.PresetSource(name)
	if err != nil {
		return err
	}
	_, err = os.Stdout.Write(data)
	return err
}

// keybindsCheatSheet prints a preset's cheat sheet: the same keys, the keys
// that moved and the keys tuios does not have.
func keybindsCheatSheet(name string, asJSON bool) error {
	if name == "" {
		cfg, err := loadConfigForEdit()
		if err != nil {
			return err
		}
		name = cfg.Keybindings.Preset
	}
	if name == "" || name == config.PresetTuios {
		return &diagnosticError{
			What: "The tuios keys are in use, so there is no cheat sheet to show.",
			Fix:  "Run `tuios keybinds cheatsheet tmux` to see the tmux preset.",
		}
	}
	sheet, err := config.PresetCheatSheet(name)
	if err != nil {
		return err
	}
	if asJSON {
		return printJSON(sheet)
	}
	if sheet.Model != "" {
		fmt.Println(sheet.Model)
		fmt.Println()
	}
	section := func(title string, lines []config.CheatSheetLine) {
		if title != "" {
			fmt.Println(title)
		}
		for _, l := range lines {
			if len(l.Keys) > 26 {
				// A long chord list gets a line of its own, so the
				// descriptions stay in one column.
				fmt.Printf("  %s\n  %-26s %s\n", l.Keys, "", l.Does)
				continue
			}
			fmt.Printf("  %-26s %s\n", l.Keys, l.Does)
		}
		fmt.Println()
	}
	section("Same keys", sheet.Same)
	fmt.Println("Keys that moved")
	fmt.Println("  A moved key keeps its letter. Press it after the tuios menu key.")
	section("", sheet.Moved)
	fmt.Println("Keys tuios does not have")
	for _, m := range sheet.Missing {
		fmt.Println("  " + m)
	}
	for _, n := range sheet.Notes {
		fmt.Println()
		fmt.Println(n)
	}
	return nil
}
