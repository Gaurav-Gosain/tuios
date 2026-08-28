package session

import (
	"encoding/json"
	"fmt"
	"slices"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/capture"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/shot"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
)

// The screenshot verb renders a window to a styled image and writes it.
//
// It runs in the daemon, for the reason capture-pane does: the daemon owns the
// pane's emulator and its scrollback, so `tuios screenshot -w build` answers
// on a detached session, from a script, with nobody attached. The window
// chrome in the picture is drawn by the renderer rather than scraped off a
// client's border, which looks better and removes the client dependency
// entirely.
//
// It always writes a file and returns the path. There is no bytes-in-the-
// envelope route: the protocol is line-delimited JSON, a scrollback PNG is
// megabytes, and a second delivery path is a second set of bugs. The CLI
// reaches the daemon over a unix socket, so the file it names is on the
// machine the caller is sitting at; the result says so in `host` regardless,
// so a script never has to assume it.

// screenshotHosts names the machine a written file landed on.
const (
	screenshotHostDaemon = "daemon"
	screenshotHostClient = "client"
)

// verbScreenshot renders one window and writes the file.
func (d *Daemon) verbScreenshot(_ *connState, params json.RawMessage) (any, *verbError) {
	var p struct {
		Session    string `json:"session"`
		Window     string `json:"window"`
		Format     string `json:"format"`
		Theme      string `json:"theme"`
		Frame      string `json:"frame"`
		Scrollback bool   `json:"scrollback"`
		Lines      int    `json:"lines"`
		Cursor     bool   `json:"cursor"`
		Out        string `json:"out"`
	}
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	if p.Format != "" && !slices.Contains(config.ScreenshotFormats, p.Format) {
		return nil, invalidParam("format", "unknown output format", config.ScreenshotFormats...)
	}
	if p.Frame != "" && !slices.Contains(config.ScreenshotFrames, p.Frame) {
		return nil, invalidParam("frame", "unknown frame style", config.ScreenshotFrames...)
	}
	if p.Theme != "" && !theme.Exists(p.Theme) {
		return nil, invalidParam("theme", "no theme by that name is installed")
	}

	sess, verr := d.resolveVerbSession(p.Session)
	if verr != nil {
		return nil, verr
	}
	pty, err := d.resolvePTYForTarget(sess, p.Window)
	if err != nil {
		return nil, mapResolveErr(err, sess)
	}

	settings, warnings := d.screenshotSettings(sess, p.Theme)
	if p.Format != "" {
		settings.Format, _ = shot.ParseFormat(p.Format)
	}
	if p.Frame != "" {
		settings.Frame = p.Frame
	}
	if p.Cursor {
		settings.Cursor = true
	}

	palette, warn := capture.Palette(settings.ThemeID)
	if warn != "" {
		warnings = append(warnings, warn)
	}

	// lines bounds the history above the screen. Without --scrollback there is
	// no history in the picture at all, which is what a plain capture means.
	rows := 0
	if p.Scrollback {
		rows = -1 // everything the pane holds
		if p.Lines > 0 {
			rows = p.Lines
		}
	}
	grid := pty.screenshotGrid(palette, rows, settings.Cursor)
	if grid == nil {
		return nil, newVerbError(ErrVerbCommandFailed, "the pane has no screen to capture")
	}

	settings.Title = d.screenshotTitle(sess, p.Window, settings.Title)
	frame, frameWarnings := capture.Frame(settings, palette, false)
	warnings = append(warnings, frameWarnings...)

	data, err := shot.Render(settings.Format, grid, frame, nil)
	if err != nil {
		return nil, newVerbError(ErrVerbCommandFailed, err.Error())
	}
	path, err := capture.ResolvePath(p.Out, settings.Directory, settings.Title, settings.Format, time.Now())
	if err != nil {
		return nil, newVerbError(ErrVerbCommandFailed, err.Error())
	}
	if err := capture.Save(path, data); err != nil {
		return nil, newVerbError(ErrVerbCommandFailed, err.Error())
	}

	return map[string]any{
		"type":   "screenshot",
		"path":   path,
		"host":   screenshotHostDaemon,
		"format": string(settings.Format),
		"cols":   grid.Cols,
		"rows":   grid.Rows,
		"bytes":  len(data),
		// Always present, empty when there is nothing to say, so a caller can
		// read it without checking whether the key exists.
		"warnings": warningsOrEmpty(warnings),
	}, nil
}

func warningsOrEmpty(w []string) []string {
	if w == nil {
		return []string{}
	}
	return w
}

// screenshotSettings resolves the [screenshot] section for a session: the
// session's own options where it set them, the registry defaults elsewhere.
//
// The daemon has no config of its own to read here. It holds each session's
// options, which is what set-option writes and what a client hands over on
// attach, so that is the only honest source.
func (d *Daemon) screenshotSettings(sess *Session, themeOverride string) (capture.Settings, []string) {
	cfg := config.ScreenshotConfig{
		Format:      sessOption(sess, "screenshot.format"),
		Directory:   sessOption(sess, "screenshot.directory"),
		Frame:       sessOption(sess, "screenshot.frame"),
		Background:  sessOption(sess, "screenshot.background"),
		Controls:    sessOption(sess, "screenshot.controls"),
		TitleFormat: sessOption(sess, "screenshot.title_format"),
		FontFamily:  sessOption(sess, "screenshot.font_family"),
		FontFile:    sessOption(sess, "screenshot.font_file"),
	}
	if v, ok := sessOptionInt(sess, "screenshot.padding"); ok {
		cfg.Padding = &v
	}
	if v, ok := sessOptionInt(sess, "screenshot.radius"); ok {
		cfg.Radius = &v
	}
	if v, ok := sessOptionInt(sess, "screenshot.scale"); ok {
		cfg.Scale = &v
	}
	if v, ok := sessOptionBool(sess, "screenshot.shadow"); ok {
		cfg.Shadow = &v
	}
	if v, ok := sessOptionBool(sess, "screenshot.cursor"); ok {
		cfg.Cursor = v
	}

	themeID := themeOverride
	if themeID == "" {
		themeID = sessOption(sess, "appearance.theme")
	}
	if themeID == "none" {
		themeID = ""
	}
	glyphs := sessOption(sess, "appearance.glyphs")
	return capture.SettingsFrom(cfg, themeID, glyphs), nil
}

// sessOption reads a session option, falling back to the registry default so
// an unset key and an absent one behave alike.
func sessOption(sess *Session, path string) string {
	if sess != nil {
		if v, ok := sess.GetOption(path); ok {
			return v
		}
	}
	if opt, ok := config.LookupOption(path); ok {
		return opt.Default
	}
	return ""
}

func sessOptionInt(sess *Session, path string) (int, bool) {
	v := sessOption(sess, path)
	if v == "" {
		return 0, false
	}
	var n int
	if _, err := fmt.Sscanf(v, "%d", &n); err != nil {
		return 0, false
	}
	return n, true
}

func sessOptionBool(sess *Session, path string) (bool, bool) {
	switch sessOption(sess, path) {
	case "true", "on", "1", "yes", "enabled":
		return true, true
	case "false", "off", "0", "no", "disabled":
		return false, true
	}
	return false, false
}

// screenshotTitle renders the title bar text from the window's own name
// through the configured template, so the picture carries the same title the
// window carries on screen.
func (d *Daemon) screenshotTitle(sess *Session, target, format string) string {
	if sess == nil || format == "" {
		return ""
	}
	state := sess.GetState()
	if state == nil {
		return ""
	}
	// An empty target means the focused window, the same as it does for the
	// PTY lookup. Without this the default capture would come out untitled.
	if target == "" {
		id, err := focusedWindowID(state)
		if err != nil {
			return ""
		}
		target = id
	}
	idx, err := findWindowStateIndex(state.Windows, target)
	if err != nil {
		return ""
	}
	w := state.Windows[idx]
	title := w.CustomName
	if title == "" {
		title = w.Title
	}
	return config.Global.FormatWindowTitle(title, idx+1, w.Cwd)
}
