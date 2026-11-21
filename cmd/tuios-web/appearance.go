package main

import (
	_ "embed"
	"encoding/base64"

	"github.com/Gaurav-Gosain/sip"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
	tint "github.com/lrstanley/bubbletint/v2"
)

const webTitle = "tuios"

//go:embed favicon.svg
var faviconSVG []byte

var webFavicon = "data:image/svg+xml;base64," + base64.StdEncoding.EncodeToString(faviconSVG)

func sipColor(c *tint.Color) sip.Color {
	if c == nil {
		return ""
	}
	return sip.Color(c.Hex())
}

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

	if th.Cursor != "" {
		th.CursorAccent = th.Background
	}
	return th
}

func browserAppearance() sip.Appearance {
	return sip.Appearance{
		Title:   webTitle,
		Favicon: webFavicon,
		Theme:   browserTheme(theme.Current()),
	}
}
