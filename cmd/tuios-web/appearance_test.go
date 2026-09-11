package main

import (
	"testing"

	"github.com/Gaurav-Gosain/sip"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
	tint "github.com/lrstanley/bubbletint/v2"
)

// useTheme selects a theme for one test and puts the process back afterwards.
// theme.Initialize writes a package global that every later test would read.
func useTheme(t *testing.T, id string) *tint.Tint {
	t.Helper()
	if err := theme.Initialize(id); err != nil {
		t.Fatalf("theme.Initialize(%q): %v", id, err)
	}
	t.Cleanup(func() { _ = theme.Initialize("") })
	if id == "" {
		return nil
	}
	cur := theme.Current()
	if cur == nil || cur.ID != id {
		t.Fatalf("theme %q did not become the current theme, got %v", id, cur)
	}
	return cur
}

// TestSipColorKeepsNilEmpty is the rule the whole partial palette rests on.
func TestSipColorKeepsNilEmpty(t *testing.T) {
	if got := sipColor(nil); got != "" {
		t.Errorf("a nil theme colour became %q, and sip reads anything but the empty string as a colour the user chose", got)
	}
	if got := sipColor(tint.FromHex("#cc241d")); got != "#cc241d" {
		t.Errorf("sipColor wrote %q, want %q", got, "#cc241d")
	}
}

// TestBrowserThemeLeavesUnsetColoursEmpty checks the case most of the roster is
// in. tokyo_night sets neither a cursor nor a selection colour, and a theme
// that maps those to a zero colour paints a black cursor and a black selection
// block onto a page that should have kept sip's own.
func TestBrowserThemeLeavesUnsetColoursEmpty(t *testing.T) {
	cur := useTheme(t, "tokyo_night")
	if cur.Cursor != nil || cur.SelectionBg != nil {
		t.Fatalf("tokyo_night now carries a cursor or selection colour, pick another theme for this test")
	}

	th := browserTheme(cur)
	for _, c := range []struct {
		name string
		got  sip.Color
	}{
		{"cursor", th.Cursor},
		{"cursorAccent", th.CursorAccent},
		{"selectionBackground", th.SelectionBackground},
	} {
		if c.got != "" {
			t.Errorf("%s came out %q for a theme that does not set it, want empty so sip keeps its default", c.name, c.got)
		}
	}

	// The colours the theme does carry still travel.
	if th.Background != "#16161e" {
		t.Errorf("background came out %q, want %q", th.Background, "#16161e")
	}
}

// TestBrowserThemeCarriesEveryColour walks the sixteen in index order. The
// swap this guards against is silent: bubbletint calls index 5 Purple and
// xterm calls it magenta, so a hand-written list puts it in brightMagenta and
// nothing but a screenshot says so.
func TestBrowserThemeCarriesEveryColour(t *testing.T) {
	cur := useTheme(t, "gruvbox_dark")
	th := browserTheme(cur)

	want := map[string]struct {
		src *tint.Color
		got sip.Color
	}{
		"foreground":    {cur.Fg, th.Foreground},
		"background":    {cur.Bg, th.Background},
		"cursor":        {cur.Cursor, th.Cursor},
		"selection":     {cur.SelectionBg, th.SelectionBackground},
		"black":         {cur.Black, th.Black},
		"red":           {cur.Red, th.Red},
		"green":         {cur.Green, th.Green},
		"yellow":        {cur.Yellow, th.Yellow},
		"blue":          {cur.Blue, th.Blue},
		"magenta":       {cur.Purple, th.Magenta},
		"cyan":          {cur.Cyan, th.Cyan},
		"white":         {cur.White, th.White},
		"brightBlack":   {cur.BrightBlack, th.BrightBlack},
		"brightRed":     {cur.BrightRed, th.BrightRed},
		"brightGreen":   {cur.BrightGreen, th.BrightGreen},
		"brightYellow":  {cur.BrightYellow, th.BrightYellow},
		"brightBlue":    {cur.BrightBlue, th.BrightBlue},
		"brightMagenta": {cur.BrightPurple, th.BrightMagenta},
		"brightCyan":    {cur.BrightCyan, th.BrightCyan},
		"brightWhite":   {cur.BrightWhite, th.BrightWhite},
	}
	for name, c := range want {
		if c.src == nil {
			t.Fatalf("gruvbox_dark no longer sets %s, pick another theme for this test", name)
		}
		if string(c.got) != c.src.Hex() {
			t.Errorf("%s came out %q, want the theme's %q", name, c.got, c.src.Hex())
		}
	}

	// The accent is what the glyph under a block cursor is painted in.
	if th.CursorAccent != th.Background {
		t.Errorf("cursorAccent is %q and the background is %q, so the character under the cursor does not sit on the ground it would have had", th.CursorAccent, th.Background)
	}
}

// TestBrowserAppearanceWithoutAThemeSendsNoColour pins the other half of the
// rule. With no theme tuios leaves indexed colours indexed for the far end to
// resolve, and the RGBA those indices carry in this process is the xterm
// default rather than anything the user chose.
func TestBrowserAppearanceWithoutAThemeSendsNoColour(t *testing.T) {
	useTheme(t, "")
	if theme.IsEnabled() {
		t.Fatal("theming is still on after an empty theme name")
	}

	a := browserAppearance()
	if !a.Theme.IsZero() {
		t.Errorf("an unthemed server sent a palette: %+v", a.Theme)
	}
	for i, c := range a.Theme.ANSI() {
		if c != "" {
			t.Errorf("ANSI %d came out %q, and nobody chose it", i, c)
		}
	}
	// Named, because this is the value the guess would have produced: xterm's
	// index 1 resolves to #800000 in this process.
	if a.Theme.Red == "#800000" {
		t.Error("the browser was sent xterm's own red, which is a guess only the user's terminal can settle")
	}
}

// TestBrowserAppearanceNamesTheTab keeps the title out of the theme rule. It is
// not a colour, so no terminal has to settle it, and without one the tab says
// "Sip" whatever the server is running.
func TestBrowserAppearanceNamesTheTab(t *testing.T) {
	useTheme(t, "")
	if got := browserAppearance().Title; got != "tuios" {
		t.Errorf("the tab is named %q, want %q", got, "tuios")
	}
	if browserAppearance().IsZero() {
		t.Error("the appearance reads as unset, so sip sends no options blob and the tab keeps sip's name")
	}
}

// TestEveryThemeReachesTheBrowser runs the whole roster through. A colour sip
// refuses stops the server from starting, so one bad theme in 342 is a tuios
// that will not serve.
func TestEveryThemeReachesTheBrowser(t *testing.T) {
	theme.EnsureRegistry()
	t.Cleanup(func() { _ = theme.Initialize("") })

	var withNilSelection, withNilCursor int
	for _, cur := range tint.Tints() {
		th := browserTheme(cur)
		if err := th.Validate(); err != nil {
			t.Errorf("theme %s: %v", cur.ID, err)
		}
		if cur.SelectionBg == nil {
			withNilSelection++
			if th.SelectionBackground != "" {
				t.Errorf("theme %s sets no selection colour but sent %q", cur.ID, th.SelectionBackground)
			}
		}
		if cur.Cursor == nil {
			withNilCursor++
			if th.Cursor != "" || th.CursorAccent != "" {
				t.Errorf("theme %s sets no cursor but sent cursor %q accent %q", cur.ID, th.Cursor, th.CursorAccent)
			}
		}
	}
	if withNilSelection == 0 || withNilCursor == 0 {
		t.Errorf("no theme in the roster leaves a colour unset (%d without a selection, %d without a cursor), so this test proves nothing about the nil rule", withNilSelection, withNilCursor)
	}
	t.Logf("%d themes, %d without a selection colour, %d without a cursor colour", len(tint.Tints()), withNilSelection, withNilCursor)
}

// TestBrowserThemeOfNoThemeIsEmpty covers the argument browserTheme is given
// when theming is off: theme.Current() answers nil and nothing may be read off
// it.
func TestBrowserThemeOfNoThemeIsEmpty(t *testing.T) {
	if th := browserTheme(nil); !th.IsZero() {
		t.Errorf("a nil theme produced a palette: %+v", th)
	}
}
