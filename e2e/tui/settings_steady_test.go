package tuie2e

import (
	"testing"

	"github.com/Gaurav-Gosain/tuitest"
)

// TestSettingsPanelHoldsItsPlaceAcrossTabs moves from Appearance, the longest
// section, to Backgrounds, one of the shortest, and checks the section strip
// and the footer are on the same screen rows. Sized to the section showing,
// the centred panel jumped with every tab and the strip the eye follows went
// with it.
//
// How this could pass wrongly: a strip read before the tab change landed
// would match itself. So the second read waits for a Backgrounds row first.
func TestSettingsPanelHoldsItsPlaceAcrossTabs(t *testing.T) {
	term, _ := start(t, startOpts{})
	waitBoot(t, term)
	newWindow(t, term)

	openSettings(t, term)
	s := term.Screen()
	strip, footer := findRow(s, "Appearance"), findRow(s, "esc close")
	if strip < 0 || footer < 0 {
		t.Fatalf("the section strip or the footer is not on screen\n%s", term.Snapshot())
	}

	if err := term.SendKeys("]"); err != nil {
		t.Fatalf("next tab: %v", err)
	}
	if err := term.WaitForText("All surfaces", uiTimeout); err != nil {
		t.Fatalf("the Backgrounds tab did not open: %v\n%s", err, term.Snapshot())
	}
	s = term.Screen()
	if got := findRow(s, "Appearance"); got != strip {
		t.Errorf("the section strip moved from row %d to row %d on a tab change\n%s", strip, got, term.Snapshot())
	}
	if got := findRow(s, "esc close"); got != footer {
		t.Errorf("the footer moved from row %d to row %d on a tab change\n%s", footer, got, term.Snapshot())
	}

	if err := term.SendKeys(tuitest.Esc); err != nil {
		t.Fatalf("close settings: %v", err)
	}
}
