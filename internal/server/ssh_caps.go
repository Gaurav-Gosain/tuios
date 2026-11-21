package server

import (
	"strings"

	"charm.land/ssh"
	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/charmbracelet/colorprofile"
)

var kittyCapableTerminals = map[string]bool{
	"kitty":   true,
	"ghostty": true,
	"wezterm": true,
}

var sixelCapableTerminals = map[string]bool{
	"wezterm": true,
	"foot":    true,
	"contour": true,
	"mlterm":  true,
	"mintty":  true,
	"xterm":   true,
}

func detectClientGraphics(sshSession ssh.Session, imageSymbols string) *session.ClientCapabilities {
	pty, _, _ := sshSession.Pty()
	return buildClientCapabilities(pty.Term, sshSession.Environ(), pty.Window, imageSymbols)
}

func buildClientCapabilities(term string, environ []string, win ssh.Window, imageSymbols string) *session.ClientCapabilities {
	env := parseEnviron(environ)
	name := terminalName(term, env)

	caps := &session.ClientCapabilities{TerminalName: name, Term: term}

	caps.KittyGraphics = kittyCapableTerminals[name]
	caps.SixelGraphics = sixelCapableTerminals[name]

	switch env["TUIOS_KITTY_GRAPHICS"] {
	case "1":
		caps.KittyGraphics = true
	case "0":
		caps.KittyGraphics = false
	}
	switch env["TUIOS_SIXEL_GRAPHICS"] {
	case "1":
		caps.SixelGraphics = true
	case "0":
		caps.SixelGraphics = false
	}

	cw, ch := cellSizeFromWindow(win)
	caps.CellWidth = cw
	caps.CellHeight = ch
	if win.WidthPixels > 0 {
		caps.PixelWidth = win.WidthPixels
	}
	if win.HeightPixels > 0 {
		caps.PixelHeight = win.HeightPixels
	}
	if cw, ch, ok := app.ParseCellSize(env["TUIOS_CELL_SIZE"]); ok {
		caps.CellWidth, caps.CellHeight = cw, ch
	}

	caps.SymbolImages = app.DrawsSymbols(imageSymbols,
		&app.HostCapabilities{SixelGraphics: caps.SixelGraphics, KittyGraphics: caps.KittyGraphics, Term: term},
		colorprofile.Env(append(environ, "TERM="+term)))

	return caps
}

func cellSizeFromWindow(win ssh.Window) (cw, ch int) {
	if win.Width > 0 && win.WidthPixels > 0 {
		cw = win.WidthPixels / win.Width
	}
	if win.Height > 0 && win.HeightPixels > 0 {
		ch = win.HeightPixels / win.Height
	}
	return cw, ch
}

func parseEnviron(environ []string) map[string]string {
	m := make(map[string]string, len(environ))
	for _, kv := range environ {
		if i := strings.IndexByte(kv, '='); i > 0 {
			m[kv[:i]] = kv[i+1:]
		}
	}
	return m
}

func terminalName(term string, env map[string]string) string {
	termProgram := strings.ToLower(env["TERM_PROGRAM"])
	switch {
	case strings.Contains(termProgram, "ghostty"):
		return "ghostty"
	case strings.Contains(termProgram, "kitty"):
		return "kitty"
	case strings.Contains(termProgram, "wezterm"):
		return "wezterm"
	case strings.Contains(termProgram, "iterm"):
		return "iterm2"
	case strings.Contains(termProgram, "contour"):
		return "contour"
	case strings.Contains(termProgram, "foot"):
		return "foot"
	}

	if env["KITTY_WINDOW_ID"] != "" {
		return "kitty"
	}
	if env["GHOSTTY_RESOURCES_DIR"] != "" {
		return "ghostty"
	}
	if env["WEZTERM_PANE"] != "" {
		return "wezterm"
	}

	t := strings.ToLower(term)
	switch {
	case strings.Contains(t, "ghostty"):
		return "ghostty"
	case strings.Contains(t, "kitty"):
		return "kitty"
	case strings.Contains(t, "wezterm"):
		return "wezterm"
	case strings.Contains(t, "foot"):
		return "foot"
	case strings.Contains(t, "contour"):
		return "contour"
	case strings.Contains(t, "mlterm"):
		return "mlterm"
	case strings.Contains(t, "xterm"):
		return "xterm"
	}
	return ""
}

func clientToHostCapabilities(c *session.ClientCapabilities) *app.HostCapabilities {
	if c == nil {
		return nil
	}
	return &app.HostCapabilities{
		KittyGraphics:     c.KittyGraphics,
		KittyAnimation:    false,
		KittyFileTransfer: false,
		SixelGraphics:     c.SixelGraphics,
		TrueColor:         true,
		TerminalName:      c.TerminalName,
		Term:              c.Term,
		PixelWidth:        c.PixelWidth,
		PixelHeight:       c.PixelHeight,
		CellWidth:         c.CellWidth,
		CellHeight:        c.CellHeight,
	}
}
