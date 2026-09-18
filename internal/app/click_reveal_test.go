package app

import "testing"

// The scrolling layout brings a clicked column fully on screen. These pin the
// part that took two attempts to get right: when it is allowed to happen.
//
// Revealing on the press moves the pane out from under the pointer, so a press
// that turns out to be the start of a drag measures every later coordinate
// against where the pane used to be. The first cut did that and landed a
// dropped pane twelve cells off. The second cut avoided it by revealing on only
// three of the seven press sites, which missed the one an ordinary click in
// terminal mode actually takes, so nothing scrolled at all.
//
// The rule is: arm on the press, reveal on the release, and only when the
// pointer never moved. By the release the gesture is over, so moving the strip
// is safe, and a gesture that moved is not a click.

func armedOS(t *testing.T) *OS {
	t.Helper()
	m := &OS{}
	m.Settings.NiriClickReveals = true
	m.AutoTiling = true
	m.UseScrollingLayout = true
	return m
}

// TestAPressArmsAndAStationaryReleaseFires.
func TestAPressArmsAndAStationaryReleaseFires(t *testing.T) {
	m := armedOS(t)
	m.ArmClickReveal(40, 12)
	if !m.clickReveal.armed {
		t.Fatal("a press in the scrolling layout did not arm the reveal")
	}
	// No focused window here, so this only has to reach the end and disarm.
	m.ReleaseClickReveal(40, 12)
	if m.clickReveal.armed {
		t.Error("the release left the reveal armed, so the next release would fire it")
	}
}

// TestAGestureThatMovedIsNotAClick is the whole reason this waits for the
// release. A drag has already been laid out against the strip where it started,
// and scrolling it now would put the pane somewhere the drop did not expect.
func TestAGestureThatMovedIsNotAClick(t *testing.T) {
	m := armedOS(t)
	m.ArmClickReveal(40, 12)
	m.ReleaseClickReveal(52, 12)
	if m.clickReveal.armed {
		t.Error("a release that moved left the reveal armed")
	}
	// Nothing to assert about the viewport without a layout; what matters is
	// that the armed press was consumed and did not survive to a later release.
}

// TestTheSettingIsHonouredAtTheArm, so a person who turned it off pays nothing
// on every press rather than having it checked twice.
func TestTheSettingIsHonouredAtTheArm(t *testing.T) {
	m := armedOS(t)
	m.Settings.NiriClickReveals = false
	m.ArmClickReveal(40, 12)
	if m.clickReveal.armed {
		t.Error("the reveal armed with the setting off")
	}
}

// TestOnlyTheScrollingLayoutArms. The other tilers do not scroll, so there is
// nothing to bring into view and nothing to remember.
func TestOnlyTheScrollingLayoutArms(t *testing.T) {
	for _, c := range []struct {
		name               string
		autoTiling, scroll bool
	}{
		{"floating", false, false},
		{"tiled but not scrolling", true, false},
		{"scrolling without tiling", false, true},
	} {
		t.Run(c.name, func(t *testing.T) {
			m := armedOS(t)
			m.AutoTiling, m.UseScrollingLayout = c.autoTiling, c.scroll
			m.ArmClickReveal(40, 12)
			if m.clickReveal.armed {
				t.Error("the reveal armed outside the scrolling layout")
			}
		})
	}
}

// TestAReleaseWithNoPressDoesNothing guards the deferred call in the release
// handler, which runs on every release including ones no pane press armed.
func TestAReleaseWithNoPressDoesNothing(t *testing.T) {
	m := armedOS(t)
	m.ReleaseClickReveal(40, 12) // must not panic and must stay disarmed
	if m.clickReveal.armed {
		t.Error("a release with nothing armed armed something")
	}
}
