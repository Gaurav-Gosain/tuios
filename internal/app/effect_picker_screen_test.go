package app_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/apptest"
	"github.com/Gaurav-Gosain/tuios/internal/config"
)

const effectPickerTitle = "Screen saver effect"

func effectPickerOpen(f *apptest.Fixture) bool {
	_, _, ok := f.Find(effectPickerTitle)
	return ok
}

func effectSetting(f *apptest.Fixture) string { return f.M.UserConfig.Screensaver.EffectName() }

func effectPickerRows(t *testing.T, f *apptest.Fixture) []string {
	t.Helper()
	screen := f.Screen()
	whole, _ := f.Panels()
	for _, b := range whole {
		if strings.TrimSpace(panelRow(screen[b.Y0+1], b)) != effectPickerTitle {
			continue
		}
		var rows []string
		for y := b.Y0 + 2; y < b.Y1; y++ {
			row := strings.TrimSpace(panelRow(screen[y], b))
			if !strings.HasPrefix(row, "The preview shows") {
				rows = append(rows, row)
			}
		}
		return rows
	}
	t.Fatalf("the effect picker is not a whole panel on screen:\n%s", f.Text())
	return nil
}

func effectFixture(t *testing.T) *apptest.Fixture {
	t.Helper()
	return openEffectPicker(t, apptest.Config(func(c *config.UserConfig) { c.Appearance.FramePanels = true }))
}

// TestEffectPickerCommitsOnEnterAndLeavesItAloneOnEsc: enter sets the effect
// under the cursor and closes the picker; esc closes it and leaves the
// setting as it was, however far the cursor moved. Negative control: make
// apply skip setOption, or make cancel write the selection.
func TestEffectPickerCommitsOnEnterAndLeavesItAloneOnEsc(t *testing.T) {
	f := effectFixture(t)
	if effectSetting(f) == "expand" {
		t.Fatal("the setting already is expand, so neither half of this proves anything")
	}

	f.Type("expand")
	f.Keys("enter")
	if effectPickerOpen(f) {
		t.Error("enter did not close the picker")
	}
	if got := effectSetting(f); got != "expand" {
		t.Errorf("enter left the setting on %q, want expand", got)
	}

	f.Keys("esc", "esc")
	if _, _, ok := f.Find("Settings"); ok {
		t.Fatalf("settings did not close:\n%s", f.Text())
	}
	openFromSettings(t, f, "Effect", effectPickerTitle)
	f.Keys("down", "down", "down")
	f.Keys("esc")
	if effectPickerOpen(f) {
		t.Error("esc did not close the picker")
	}
	if got := effectSetting(f); got != "expand" {
		t.Errorf("esc left the setting on %q, want the applied expand", got)
	}
}

// TestEffectPickerFiltersAndRefusesAnEmptyApply: typing narrows the list,
// clearing the query puts it back, and enter with nothing under the cursor
// neither closes the picker, which would strand the query, nor changes the
// setting. Negative control: make EffectPickerApplySelection close on an
// empty list.
func TestEffectPickerFiltersAndRefusesAnEmptyApply(t *testing.T) {
	f := effectFixture(t)
	whole := effectPickerRows(t, f)

	f.Type("matr")
	if _, _, ok := f.Find("matrix"); !ok {
		t.Fatalf("the query matr does not show matrix:\n%s", f.Text())
	}
	if slices.Equal(effectPickerRows(t, f), whole) {
		t.Error("the query changed nothing in the picker")
	}
	f.Keys("backspace", "backspace", "backspace", "backspace")
	if got := effectPickerRows(t, f); !slices.Equal(got, whole) {
		t.Errorf("clearing the query did not put the whole list back:\n%q\nwant\n%q", got, whole)
	}

	before := effectSetting(f)
	f.Type("zzzznotaneffect")
	if _, _, ok := f.Find("No matching effects"); !ok {
		t.Fatalf("a nonsense query still shows effects:\n%s", f.Text())
	}
	f.Keys("enter")
	if !effectPickerOpen(f) {
		t.Error("enter with nothing selected closed the picker and stranded the query")
	}
	if got := effectSetting(f); got != before {
		t.Errorf("enter with nothing selected set the effect to %q", got)
	}
}
