package app

import (
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// TestTapeSettingsCategoryTogglesAutoReview verifies the Tape settings category
// exists and its "Auto-open review" toggle flips the persisted config value.
func TestTapeSettingsCategoryTogglesAutoReview(t *testing.T) {
	m := NewOS(OSOptions{UserConfig: config.DefaultConfig()})

	var tape *settingsCategory
	for i := range m.settingsCategories() {
		c := m.settingsCategories()[i]
		if c.Name == "Tape" {
			tape = &c
			break
		}
	}
	if tape == nil {
		t.Fatal("no Tape settings category")
	}

	var review *settingItem
	for i := range tape.Items {
		if tape.Items[i].Label == "Auto-open review" {
			review = &tape.Items[i]
			break
		}
	}
	if review == nil {
		t.Fatal("no Auto-open review toggle in the Tape category")
	}

	if review.boolVal(m) {
		t.Fatal("auto_review should default to false")
	}
	review.adjust(m, 1)
	if !m.UserConfig.Tape.AutoReview {
		t.Fatal("toggling the row did not set Tape.AutoReview")
	}
	if !review.boolVal(m) {
		t.Fatal("toggle value did not reflect the new state")
	}
	review.adjust(m, 1)
	if m.UserConfig.Tape.AutoReview {
		t.Fatal("toggling again did not clear Tape.AutoReview")
	}
}

// TestTapeSettingsAutorunCycles verifies the Autorun enum row cycles the config.
func TestTapeSettingsAutorunCycles(t *testing.T) {
	m := NewOS(OSOptions{UserConfig: config.DefaultConfig()})
	var autorun *settingItem
	for _, c := range m.settingsCategories() {
		if c.Name != "Tape" {
			continue
		}
		for i := range c.Items {
			if c.Items[i].Label == "Autorun" {
				autorun = &c.Items[i]
			}
		}
	}
	if autorun == nil {
		t.Fatal("no Autorun row in the Tape category")
	}
	if got := autorun.value(m); got != config.TapeAutorunAsk {
		t.Fatalf("default autorun row = %q, want ask", got)
	}
	autorun.adjust(m, 1)
	if m.UserConfig.Tape.Autorun == config.TapeAutorunAsk {
		t.Fatal("adjusting the autorun row did not change the config")
	}
}
