package main

import (
	"github.com/Gaurav-Gosain/sip"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
	tint "github.com/lrstanley/bubbletint/v2"
)

// webTitle is what the browser tab says before a session names itself.
const webTitle = "tuios"

// sipColor turns one theme colour into the colour sip hands the browser.
//
// A nil colour has to come back empty, because empty is sip's "keep your own
// default" and that is the whole mechanism for a partial palette.
//
// The rule is load-bearing rather than defensive. fillDefaults never fills in
// SelectionBg, so every theme imported from kitty, ghostty, alacritty or
// wezterm arrives with it nil, and of the 342 built-in tints thirteen name no
// selection colour and twelve of those name no cursor colour either.
// theme.ColorToString, the obvious helper, answers "#000000" for nil, which
// would paint a black selection block onto a theme that never asked for one.
func sipColor(c *tint.Color) sip.Color {
	if c == nil {
		return ""
	}
	// Hex is the only form sip accepts, and the only one a browser cannot
	// misread. tint.Color.Hex always writes #rrggbb.
	return sip.Color(c.Hex())
}

// browserTheme is one tuios theme written as sip's palette.
//
// Every colour the theme does not carry is left empty, so sip keeps its own
// there. The result is a patch over sip's palette, not a replacement for it.
func browserTheme(t *tint.Tint) sip.Theme {
	if t == nil {
		return sip.Theme{}
	}

	var pal sip.ANSIPalette
	for i, c := range theme.ANSIOrder(t) {
		pal[i] = sipColor(c)
	}

	th := sip.Theme{
		Foreground:          sipColor(t.Fg),
		Background:          sipColor(t.Bg),
		Cursor:              sipColor(t.Cursor),
		SelectionBackground: sipColor(t.SelectionBg),
	}.WithANSI(pal)

	// The glyph under a block cursor is painted in cursorAccent, so a cursor
	// with no accent beside it is a character that disappears into its own
	// cursor. The ground is what the cell would have shown anyway, which is
	// what a terminal config writes here by hand. Tied to the cursor: with no
	// cursor colour of our own, sip's cursor and sip's accent stay a matched
	// pair.
	if th.Cursor != "" {
		th.CursorAccent = th.Background
	}
	return th
}

// browserAppearance is how the page looks for this server.
//
// The title is always sent. It is not a colour, so no terminal has to settle
// it, and without one every tuios tab says "Sip".
//
// The palette is sent only when the user picked a theme. With no theme tuios
// leaves indexed colours indexed and the browser resolves them, which is the
// same contract a real terminal has, and theme.GetANSIPalette answers with
// ansi.BasicColor indices whose RGBA in this process is the xterm default:
// #800000 for red, #808000 for yellow. Those are a guess. Sending them would
// paint a browser with sixteen colours nobody chose, so an unthemed server
// sends no colour at all and sip keeps its own.
//
// Four of sip's settings are deliberately left alone. CursorStyle and
// CursorBlink are the browser's defaults and tuios overrides both at runtime
// with DECSCUSR, so a value here would only decide the first frame. FontSize
// belongs to the reader, who has a slider for it. Scrollback is sip's browser
// buffer, and tuios repaints the whole screen: the lines above it are stale
// frames, and the scrollback a user browses is tuios's own, inside the pane.
//
// This blob travels once, at the handshake. A theme the user changes while a
// browser is attached reaches the panes, because tuios resolves indexed
// colours itself, but not the browser's own palette, cursor or page ground.
// Those follow on the next page load.
func browserAppearance() sip.Appearance {
	return sip.Appearance{
		Title: webTitle,
		Theme: browserTheme(theme.Current()),
	}
}
