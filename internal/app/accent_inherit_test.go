package app

import (
	"image/color"
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/sessiontree"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
)

// accentInheritOS is a rail whose attached session has a colour of its own, so
// its panes wear one without carrying one. That is the shape the picker has to
// open on: what is on screen, not what is stored.
func accentInheritOS(t *testing.T) (*OS, sessiontree.Tree) {
	t.Helper()
	m, tree := sessionColorOS(t, 120, 40)
	truecolorForTest(t)
	m.SidebarFocused = true
	// Render once so the hues are arbitrated the way the rail has them: the
	// picker has to agree with the row the user right-clicked, not with an
	// unarbitrated guess.
	m.sidebarPanelLinesForTree(tree)
	return m, tree
}

// TestAccentPickerSeedsOnTheColourThePaneWears is the whole bug: a pane with no
// accent of its own is wearing its session's colour, and the picker used to open
// on the theme's default accent instead, which is a colour nobody is looking at.
func TestAccentPickerSeedsOnTheColourThePaneWears(t *testing.T) {
	withSessionColors(t, true)
	m, _ := accentInheritOS(t)

	want, ok := m.SessionColor("main")
	if !ok {
		t.Fatal("the attached session has no colour, so there is nothing to inherit")
	}
	m.OpenAccentPicker("aaaaaaaa1111")
	if got := m.AccentPicker.Cur; got != want.RGB() {
		t.Errorf("the picker opened on %s, want the session's colour %s", hexString(got), want.Hex())
	}
	if m.AccentPicker.Src != accentSourceSession {
		t.Error("the picker opened on the session's colour without saying it was inherited")
	}
	m.CloseAccentPicker()

	// An explicit session accent outranks the automatic colour, and the picker
	// follows the same precedence rather than a second copy of it.
	m.SessionAccent = "brightmagenta"
	m.OpenAccentPicker("aaaaaaaa1111")
	if got, want := m.AccentPicker.Cur, SlotAccent(5).RGB(); got != want {
		t.Errorf("with an explicit session accent the picker opened on %s, want %s", hexString(got), hexString(want))
	}
	m.CloseAccentPicker()
	m.SessionAccent = ""

	// A pane pinned to a colour opens on that colour, session or no session.
	green := RGBAccent(color.RGBA{R: 0x33, G: 0x99, B: 0x66, A: 0xff})
	m.SetWindowAccent("aaaaaaaa1111", green)
	m.OpenAccentPicker("aaaaaaaa1111")
	if got := m.AccentPicker.Cur; got != green.RGB() {
		t.Errorf("a pinned pane opened the picker on %s, want its own accent %s", hexString(got), green.Hex())
	}
	if m.AccentPicker.Src == accentSourceSession {
		t.Error("a pinned pane opened the picker claiming an inherited colour")
	}
}

// TestAccentPickerSeedWithSessionColoursOff holds the documented fallback: with
// the feature off a pane inherits nothing, so the picker opens on the chrome's
// accent as it always has.
func TestAccentPickerSeedWithSessionColoursOff(t *testing.T) {
	withSessionColors(t, false)
	m, _ := accentInheritOS(t)

	m.OpenAccentPicker("aaaaaaaa1111")
	s := m.AccentPicker
	if got, want := s.Cur, toRGBA(theme.UI().Accent); got != want {
		t.Errorf("the picker seeded %s, want the chrome accent %s", hexString(got), hexString(want))
	}
	if s.HadPrev || s.Src != accentSourceNone {
		t.Errorf("with session colours off the picker claims a colour it is not wearing (had=%v src=%v)", s.HadPrev, s.Src)
	}
}

// TestAccentCancelLeavesTheTargetExactlyAsItWas: opening the picker and backing
// out is a no-op in both directions. An inheriting pane keeps inheriting, and a
// pane pinned to a theme slot keeps the slot rather than being frozen to the
// colour that slot happens to resolve to today.
func TestAccentCancelLeavesTheTargetExactlyAsItWas(t *testing.T) {
	withSessionColors(t, true)
	m, _ := accentInheritOS(t)

	m.OpenAccentPicker("aaaaaaaa1111")
	m.CloseAccentPicker()
	if a, ok := m.WindowAccent("aaaaaaaa1111"); ok {
		t.Errorf("cancelling pinned the inheriting pane to %s", a.Hex())
	}

	m.SetWindowAccent("bbbbbbbb2222", SlotAccent(3))
	m.OpenAccentPicker("bbbbbbbb2222")
	m.AccentPickerCell(4, 2)
	m.CloseAccentPicker()
	if a, _ := m.WindowAccent("bbbbbbbb2222"); a != SlotAccent(3) {
		t.Errorf("cancelling left the accent as %+v, want the slot it had", a)
	}
}

// TestAccentApplyWithoutMovingKeepsInheritance is the trap the seeding opens: the
// picker now shows the inherited colour, so applying it would look like a
// no-change and quietly pin the pane, cutting it off from its session forever
// after. Applying a colour the target is already wearing writes nothing.
func TestAccentApplyWithoutMovingKeepsInheritance(t *testing.T) {
	withSessionColors(t, true)
	m, _ := accentInheritOS(t)

	m.OpenAccentPicker("aaaaaaaa1111")
	m.AccentPickerApply()
	if a, ok := m.WindowAccent("aaaaaaaa1111"); ok {
		t.Errorf("applying without moving pinned the pane to %s", a.Hex())
	}

	// The same rule keeps a slot a slot: applying the colour it resolves to today
	// would silently stop it following the theme.
	m.SetWindowAccent("bbbbbbbb2222", SlotAccent(3))
	m.OpenAccentPicker("bbbbbbbb2222")
	m.AccentPickerApply()
	if a, _ := m.WindowAccent("bbbbbbbb2222"); a != SlotAccent(3) {
		t.Errorf("applying without moving turned the slot into %+v", a)
	}

	// Moving first still pins, which is the point of the picker.
	m.OpenAccentPicker("aaaaaaaa1111")
	m.AccentPickerHueCell(3)
	m.AccentPickerCell(6, 1)
	want := m.AccentPicker.Cur
	m.AccentPickerApply()
	if a, ok := m.WindowAccent("aaaaaaaa1111"); !ok || a != RGBAccent(want) {
		t.Errorf("picking a colour stored %+v, want %s", a, hexString(want))
	}
}

// TestAccentClearReturnsToInheriting pins what clearing means now that there is
// something under the accent: the pane goes back to following its session, not
// to having no colour.
func TestAccentClearReturnsToInheriting(t *testing.T) {
	withSessionColors(t, true)
	m, _ := accentInheritOS(t)

	m.SetWindowAccent("aaaaaaaa1111", RGBAccent(color.RGBA{R: 0xff, A: 0xff}))
	m.OpenAccentPicker("aaaaaaaa1111")
	m.AccentPickerClear()
	if _, ok := m.WindowAccent("aaaaaaaa1111"); ok {
		t.Fatal("clearing left an accent on the pane")
	}
	want, _ := m.SessionColor("main")
	got, src := m.effectiveAccent("aaaaaaaa1111", "main")
	if src != accentSourceSession || got != want {
		t.Errorf("after clearing the pane wears %+v (%v), want the session's %+v", got, src, want)
	}
}

// TestAccentEntryPointsSeedIdentically: the rail's accent key and the context
// menu's row open the same picker on the same colour, because one of them
// seeding differently is exactly how a user learns not to trust either.
func TestAccentEntryPointsSeedIdentically(t *testing.T) {
	withSessionColors(t, true)
	m, _ := accentInheritOS(t)

	m.OpenAccentPicker("aaaaaaaa1111")
	direct := m.AccentPicker
	m.CloseAccentPicker()

	idx := navIndexOfWindow(m, "aaaaaaaa1111")
	if idx < 0 {
		t.Fatal("the rail has no window row for the pane")
	}
	m.SidebarCursor = idx
	m.SidebarAccentCursor()
	if m.AccentPickerTargetID != "aaaaaaaa1111" {
		t.Fatalf("the rail's accent key targeted %q", m.AccentPickerTargetID)
	}
	if got := m.AccentPicker; got != direct {
		t.Errorf("the rail's accent key seeded %+v, want the same state as the menu (%+v)", got, direct)
	}
}

// TestAccentPickerShowsInheritedNotPinned: inherited and pinned look identical
// on the rail, so the picker is where they have to be told apart. The readout
// names the session as the source rather than printing a hex the pane does not
// own.
func TestAccentPickerShowsInheritedNotPinned(t *testing.T) {
	withSessionColors(t, true)
	m, _ := accentInheritOS(t)

	m.OpenAccentPicker("aaaaaaaa1111")
	text := strings.Join(pickerLines(t, m), "\n")
	if !strings.Contains(text, "session") {
		t.Errorf("the picker does not say the colour comes from the session:\n%s", text)
	}
	if strings.Contains(text, "none") {
		t.Errorf("the picker calls an inherited colour none:\n%s", text)
	}
	m.CloseAccentPicker()

	m.SetWindowAccent("aaaaaaaa1111", RGBAccent(color.RGBA{R: 0x33, G: 0x99, B: 0x66, A: 0xff}))
	m.OpenAccentPicker("aaaaaaaa1111")
	text = strings.Join(pickerLines(t, m), "\n")
	if strings.Contains(text, "session") {
		t.Errorf("a pinned pane's picker claims the colour is the session's:\n%s", text)
	}
	if !strings.Contains(text, "#339966") {
		t.Errorf("the picker does not show the pinned colour it opened on:\n%s", text)
	}
}
