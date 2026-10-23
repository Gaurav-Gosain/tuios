package session

import (
	"encoding/json"
	"slices"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/capture"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
	"tuios.dev/shot"
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
func (d *Daemon) verbScreenshot(cs *connState, params json.RawMessage) (any, *verbError) {
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

	sess, verr := d.resolveVerbSession(cs, p.Session)
	if verr != nil {
		return nil, verr
	}
	pty, err := d.resolvePTYForTarget(sess, p.Window)
	if err != nil {
		return nil, mapResolveErr(err, sess)
	}

	settings, warnings := d.screenshotSettings(cs, sess, p.Theme)
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
	// The pane as the session draws it: a painted pane background is the
	// ground its default cells sit on.
	palette = capture.WithPaneBackground(palette, settings.PaneBackground, settings.ThemeID)

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
	frame, frameWarnings := capture.Frame(settings, palette, grid, false)
	warnings = append(warnings, frameWarnings...)

	data, err := shot.Render(settings.Format, grid, frame)
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

// screenshotSettings resolves the [screenshot] section for a session. The
// session's overrides win, then config.toml, then the defaults: the order the
// client draws by. See sessionOptions.
func (d *Daemon) screenshotSettings(cs *connState, sess *Session, themeOverride string) (capture.Settings, []string) {
	opts := d.optionsFor(cs, sess)
	cfg := config.ScreenshotConfig{
		Format:      opts.get("screenshot.format"),
		Directory:   opts.get("screenshot.directory"),
		Frame:       opts.get("screenshot.frame"),
		Background:  opts.get("screenshot.background"),
		Controls:    opts.get("screenshot.controls"),
		TitleFormat: opts.get("screenshot.title_format"),
		FontFamily:  opts.get("screenshot.font_family"),
		FontFile:    opts.get("screenshot.font_file"),
	}
	if v, ok := opts.getInt("screenshot.padding"); ok {
		cfg.Padding = &v
	}
	if v, ok := opts.getInt("screenshot.radius"); ok {
		cfg.Radius = &v
	}
	if v, ok := opts.getInt("screenshot.scale"); ok {
		cfg.Scale = &v
	}
	if v, ok := opts.getBool("screenshot.shadow"); ok {
		cfg.Shadow = &v
	}
	if v, ok := opts.getBool("screenshot.cursor"); ok {
		cfg.Cursor = v
	}

	themeID := themeOverride
	if themeID == "" {
		themeID = opts.get(optionTheme)
	}
	// No theme follows Omarchy where it is installed, as the client does.
	themeID = followOmarchy(themeID)
	if themeID == theme.NoneID {
		themeID = ""
	}
	glyphs := opts.get("appearance.glyphs")
	s := capture.SettingsFrom(cfg, themeID, glyphs)
	// The pane's own option wins, and unset follows the default for every
	// surface, the same precedence the client paints by.
	s.PaneBackground = config.ResolveBackground(
		opts.get("appearance.pane_background"),
		opts.get("appearance.background"))
	return s, nil
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
