package app

import (
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// TestEveryColourOptionGetsThePicker is the coverage guard. The registry says
// which options hold a colour; this says every one of them has a row that opens
// the picker. An option marked Color later and left out of colorSettings would
// stay a text field, which is the bug this whole change is about.
func TestEveryColourOptionGetsThePicker(t *testing.T) {
	m := NewOS(OSOptions{UserConfig: config.DefaultConfig()})

	rows := map[string]settingItem{}
	for _, cat := range m.settingsCategories() {
		for _, it := range cat.Items {
			rows[it.Label] = it
		}
	}

	for _, opt := range config.Options() {
		if !opt.Color {
			continue
		}
		cs, ok := lookupColorSetting(opt.Path)
		if !ok {
			t.Errorf("%s holds a colour and has no colour setting; it would still be a text field", opt.Path)
			continue
		}
		row, ok := rows[cs.Label]
		if !ok {
			t.Errorf("%s has a colour setting but no row labelled %q in the panel", opt.Path, cs.Label)
			continue
		}
		if row.Control != controlColor {
			t.Errorf("%s: row %q is control %d, want controlColor", opt.Path, cs.Label, row.Control)
		}
		if row.activate == nil {
			t.Errorf("%s: row %q has no activate hook, so Enter opens nothing", opt.Path, cs.Label)
		}
		if row.swatch == nil {
			t.Errorf("%s: row %q shows no swatch, which makes it a list of strings", opt.Path, cs.Label)
		}
	}
}
