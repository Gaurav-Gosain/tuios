package server

import (
	"strconv"
	"strings"

	"charm.land/ssh"
	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/session"
)

// Terminals known to render the kitty graphics protocol. The client's terminal,
// not the server's, is what must display forwarded images, so this is keyed off
// the identity the client reports over the SSH connection.
var kittyCapableTerminals = map[string]bool{
	"kitty":   true,
	"ghostty": true,
	"wezterm": true,
}

// Terminals known to render sixel graphics. Kept separate from the kitty set
// because most kitty-capable terminals do not do sixel and forwarding a sixel
// stream to a terminal that cannot decode it corrupts the screen.
var sixelCapableTerminals = map[string]bool{
	"wezterm": true,
	"foot":    true,
	"contour": true,
	"mlterm":  true,
	"mintty":  true,
	"xterm":   true,
}

// detectClientGraphics inspects what a connected SSH client reports about its
// terminal and returns the graphics capabilities TUIOS should use for that
// session. Everything here is passive: it reads the TERM from the pty-req, any
// environment the client forwarded, and the pixel dimensions the client sent
// with the pty-req. It never writes a query to the session or reads its input,
// because the bubbletea program owns the session's input stream and a second
// reader on the same SSH channel would steal bytes from it.
func detectClientGraphics(sshSession ssh.Session) *session.ClientCapabilities {
	pty, _, _ := sshSession.Pty()
	return buildClientCapabilities(pty.Term, sshSession.Environ(), pty.Window)
}

// buildClientCapabilities is the pure decision function behind
// detectClientGraphics, split out so it can be tested without an SSH handshake.
func buildClientCapabilities(term string, environ []string, win ssh.Window) *session.ClientCapabilities {
	env := parseEnviron(environ)
	name := terminalName(term, env)

	caps := &session.ClientCapabilities{TerminalName: name}

	caps.KittyGraphics = kittyCapableTerminals[name]
	caps.SixelGraphics = sixelCapableTerminals[name]

	// Explicit client overrides win over identity-based guesses.
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

	// Cell pixel size comes from the pty-req pixel dimensions the client sent.
	// The pixel-mouse (SGR-pixel, DEC 1016) and kitty geometry paths need it,
	// and over SSH the only honest source is the client, not the server.
	cw, ch := cellSizeFromWindow(win)
	caps.CellWidth = cw
	caps.CellHeight = ch
	if win.WidthPixels > 0 {
		caps.PixelWidth = win.WidthPixels
	}
	if win.HeightPixels > 0 {
		caps.PixelHeight = win.HeightPixels
	}
	// An explicit cell size wins over the pty-req, and is the only source there
	// is for the many SSH clients that forward no pixel dimensions at all. It
	// is the same TUIOS_CELL_SIZE a local client reads, so the variable means
	// one thing wherever it is set.
	if cw, ch, ok := parseCellSize(env["TUIOS_CELL_SIZE"]); ok {
		caps.CellWidth, caps.CellHeight = cw, ch
	}

	return caps
}

// parseCellSize reads a WIDTHxHEIGHT cell size in pixels.
func parseCellSize(spec string) (w, h int, ok bool) {
	left, right, cut := strings.Cut(strings.ToLower(spec), "x")
	if !cut {
		return 0, 0, false
	}
	cw, err := strconv.Atoi(strings.TrimSpace(left))
	if err != nil || cw <= 0 {
		return 0, 0, false
	}
	ch, err := strconv.Atoi(strings.TrimSpace(right))
	if err != nil || ch <= 0 {
		return 0, 0, false
	}
	return cw, ch, true
}

// cellSizeFromWindow derives a cell's pixel size from the pty-req window, which
// carries both the character grid and its drawable pixel area. Returns 0,0 when
// the client sent no pixel dimensions (most SSH clients do), leaving the daemon
// to fall back to cell-based reporting rather than inventing a size.
func cellSizeFromWindow(win ssh.Window) (cw, ch int) {
	if win.Width > 0 && win.WidthPixels > 0 {
		cw = win.WidthPixels / win.Width
	}
	if win.Height > 0 && win.HeightPixels > 0 {
		ch = win.HeightPixels / win.Height
	}
	return cw, ch
}

// parseEnviron turns a "KEY=VALUE" slice into a map for lookups.
func parseEnviron(environ []string) map[string]string {
	m := make(map[string]string, len(environ))
	for _, kv := range environ {
		if i := strings.IndexByte(kv, '='); i > 0 {
			m[kv[:i]] = kv[i+1:]
		}
	}
	return m
}

// terminalName resolves a canonical terminal identity from the client's TERM
// and any forwarded environment. TERM is always present over SSH (it rides in
// the pty-req); TERM_PROGRAM and friends are only present when the client is
// configured to forward them, so they refine the guess rather than drive it.
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

// clientToHostCapabilities projects the client's reported capabilities onto the
// app-level HostCapabilities that GetHostCapabilities serves. KittyFileTransfer
// is always false: a file-medium transmission names a path on the server, which
// the remote client cannot read, so the passthrough must re-encode as direct.
func clientToHostCapabilities(c *session.ClientCapabilities) *app.HostCapabilities {
	if c == nil {
		return nil
	}
	return &app.HostCapabilities{
		KittyGraphics:     c.KittyGraphics,
		KittyAnimation:    c.KittyAnimation,
		KittyFileTransfer: false,
		SixelGraphics:     c.SixelGraphics,
		TrueColor:         true,
		TerminalName:      c.TerminalName,
		PixelWidth:        c.PixelWidth,
		PixelHeight:       c.PixelHeight,
		CellWidth:         c.CellWidth,
		CellHeight:        c.CellHeight,
	}
}
