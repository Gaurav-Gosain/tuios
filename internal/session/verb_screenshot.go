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

const (
	screenshotHostDaemon = "daemon"
	screenshotHostClient = "client"
)

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
	if p.Out != "" && cs != nil && cs.viaLink {
		return nil, hintedVerbError(ErrVerbForbidden, "screenshot: a linked machine cannot choose where the file is written", &VerbHint{
			Param:  "out",
			Detail: "Nothing was written. Leave out unset, and the file goes under screenshot.directory.",
		})
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
	pic, verr := d.renderPaneShot(cs, sess, p.Window, paneShotOptions{
		format: p.Format, theme: p.Theme, frame: p.Frame, cursor: p.Cursor, scrollback: p.Scrollback, lines: p.Lines,
	})
	if verr != nil {
		return nil, verr
	}
	settings, data, grid, warnings := pic.settings, pic.data, pic.grid, pic.warnings
	path, err := capture.ResolvePath(p.Out, settings.Directory, settings.Title, settings.Format, time.Now())
	if err != nil {
		return nil, newVerbError(ErrVerbCommandFailed, err.Error())
	}
	if err := capture.Save(path, data); err != nil {
		return nil, newVerbError(ErrVerbCommandFailed, err.Error())
	}

	d.recordScreenshot(cs, path, string(settings.Format))
	return map[string]any{
		"type":     "screenshot",
		"path":     path,
		"host":     screenshotHostDaemon,
		"format":   string(settings.Format),
		"cols":     grid.Cols,
		"rows":     grid.Rows,
		"bytes":    len(data),
		"warnings": warningsOrEmpty(warnings),
	}, nil
}

type paneShotOptions struct {
	format, theme, frame string
	cursor, scrollback   bool
	lines                int
}

type paneShot struct {
	data     []byte
	settings capture.Settings
	grid     struct{ Cols, Rows int }
	warnings []string
}

func (d *Daemon) renderPaneShot(cs *connState, sess *Session, window string, o paneShotOptions) (paneShot, *verbError) {
	var out paneShot
	pty, err := d.resolvePTYForTarget(sess, window)
	if err != nil {
		return out, mapResolveErr(err, sess)
	}
	settings, warnings := d.screenshotSettings(cs, sess, o.theme)
	if o.format != "" {
		settings.Format, _ = shot.ParseFormat(o.format)
	}
	if o.frame != "" {
		settings.Frame = o.frame
	}
	if o.cursor {
		settings.Cursor = true
	}

	palette, warn := capture.Palette(settings.ThemeID)
	if warn != "" {
		warnings = append(warnings, warn)
	}
	palette = capture.WithPaneBackground(palette, settings.PaneBackground, settings.ThemeID)

	rows := 0
	if o.scrollback {
		rows = -1
		if o.lines > 0 {
			rows = o.lines
		}
	}
	grid := pty.screenshotGrid(palette, rows, settings.Cursor)
	if grid == nil {
		return out, newVerbError(ErrVerbCommandFailed, "the pane has no screen to capture")
	}

	settings.Title = d.screenshotTitle(cs, sess, window, settings.Title)
	frame, frameWarnings := capture.Frame(settings, palette, grid, false)
	warnings = append(warnings, frameWarnings...)

	data, err := shot.Render(settings.Format, grid, frame)
	if err != nil {
		return out, newVerbError(ErrVerbCommandFailed, err.Error())
	}
	out.data, out.settings, out.warnings = data, settings, warnings
	out.grid.Cols, out.grid.Rows = grid.Cols, grid.Rows
	return out, nil
}

func warningsOrEmpty(w []string) []string {
	if w == nil {
		return []string{}
	}
	return w
}

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
	themeID = followOmarchy(themeID)
	if themeID == theme.NoneID {
		themeID = ""
	}
	glyphs := opts.get("appearance.glyphs")
	s := capture.SettingsFrom(cfg, themeID, glyphs)
	s.PaneBackground = config.ResolveBackground(
		opts.get("appearance.pane_background"),
		opts.get("appearance.background"))
	return s, nil
}

func (d *Daemon) screenshotTitle(cs *connState, sess *Session, target, format string) string {
	if sess == nil || format == "" {
		return ""
	}
	state := sess.GetState()
	if state == nil {
		return ""
	}
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
	titles := config.Settings{WindowTitleFormat: d.optionsFor(cs, sess).get("appearance.window_title_format")}
	return titles.FormatScreenshotTitle(format, title, idx+1, w.Cwd)
}
