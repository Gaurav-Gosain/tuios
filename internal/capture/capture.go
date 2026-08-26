// Package capture is the glue between a screenshot request and the shot
// renderer: it turns the [screenshot] config section, a theme id and a glyph
// set id into the concrete palette and frame that shot.Render wants, and it
// names and writes the file.
//
// It sits outside internal/shot on purpose. shot is a leaf: it knows cells,
// colours and geometry and nothing about tuios. This package knows tuios and
// nothing about rasterisation, so the daemon and the client can both ask for
// the same picture without either of them re-deriving a default.
package capture

import (
	"fmt"
	"image/color"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/shot"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
)

// Settings is one resolved screenshot request: the config section with any
// per-call overrides already applied, so nothing downstream has to know which
// value came from where.
type Settings struct {
	Format shot.Format
	// ThemeID is the palette to render in. Empty means no theme is set and
	// the render falls back to the xterm guess, which Warnings names.
	ThemeID string
	// GlyphSetID supplies the window control marks for controls = glyphs.
	GlyphSetID string
	// Frame, Background, Controls, Title carry the config spellings straight
	// through to shot.BuildFrame.
	Frame      string
	Background string
	Controls   string
	Title      string
	Padding    int
	Radius     int
	Shadow     bool
	FontFamily string
	FontFile   string
	// HostFontFamily and HostBoldFamily are what the terminal answered when
	// asked which font it draws with. They are PostScript names, they are only
	// ever set by a client with a host to ask, and they are what makes a
	// default capture come out with the user's own icons in it.
	HostFontFamily string
	HostBoldFamily string
	Scale          int
	Cursor         bool
	// Directory is where a generated filename lands.
	Directory string
}

// SettingsFrom resolves the config section into a request, before overrides.
func SettingsFrom(c config.ScreenshotConfig, themeID, glyphSetID string) Settings {
	format, ok := shot.ParseFormat(c.EffectiveFormat())
	if !ok {
		format = shot.FormatPNG
	}
	return Settings{
		Format:     format,
		ThemeID:    themeID,
		GlyphSetID: glyphSetID,
		Frame:      orDefault(c.Frame, config.ScreenshotDefaultFrame),
		Background: orDefault(c.Background, config.ScreenshotDefaultBackground),
		Controls:   orDefault(c.Controls, config.ScreenshotDefaultControls),
		Title:      orDefault(c.TitleFormat, config.ScreenshotDefaultTitleFormat),
		Padding:    c.PaddingPx(),
		Radius:     c.RadiusPx(),
		Shadow:     c.ShadowEnabled(),
		FontFamily: orDefault(c.FontFamily, config.ScreenshotDefaultFontFamily),
		FontFile:   c.FontFile,
		Scale:      c.ScaleFactor(),
		Cursor:     c.Cursor,
		Directory:  c.ResolveDirectory(),
	}
}

func orDefault(v, def string) string {
	if v == "" {
		return def
	}
	return v
}

// XTermNotice is the one line a render carries when no theme is set. tuios can
// never read the host terminal's palette, so basic and indexed colours in an
// unthemed session are a guess; this says which guess, once, instead of
// letting the picture pass for the host's own colours.
const XTermNotice = "No theme is set. Basic colors use the xterm defaults."

// Palette resolves the session's theme into concrete colours for the renderer.
//
// It takes a theme id rather than reading the process theme because the daemon
// is a caller: the daemon never selects a tint of its own, so its idea of the
// "current" theme answers for nobody. warn is empty when a theme resolved and
// carries XTermNotice when the render fell back to the guess.
func Palette(themeID string) (p *shot.Palette, warn string) {
	ansiPalette, fg, bg, _, ok := theme.Colors(themeID)
	if !ok {
		return shot.XTermPalette(), XTermNotice
	}
	fallback := shot.XTermPalette()
	out := &shot.Palette{FG: toShot(fg, fallback.FG), BG: toShot(bg, fallback.BG)}
	for i, c := range ansiPalette {
		out.ANSI[i] = toShot(c, fallback.ANSI[i])
	}
	return out, ""
}

// toShot converts a theme colour to a shot colour, falling back when the value
// carries no channels of its own. A tint entry is normally a concrete hex, but
// an unthemed palette holds bare ansi indices, which resolve to nothing here.
func toShot(c color.Color, def shot.Color) shot.Color {
	if c == nil {
		return def
	}
	switch c.(type) {
	case ansi.BasicColor, ansi.IndexedColor:
		// A palette index cannot say what colour it is; only the host can.
		return def
	}
	r, g, b, a := c.RGBA()
	if a == 0 {
		return def
	}
	return shot.RGB(uint8(r>>8), uint8(g>>8), uint8(b>>8))
}

// Frame builds the resolved dressing for a render. plain forces the bare card
// with no title bar, which is what a region or full-screen capture gets: it is
// not a window, so a window title bar would be a claim about it that is not
// true.
func Frame(s Settings, p *shot.Palette, plain bool) (*shot.Frame, []string) {
	var warnings []string
	spec := shot.FrameSpec{
		Frame:      s.Frame,
		Background: s.Background,
		Padding:    s.Padding,
		Radius:     s.Radius,
		Shadow:     s.Shadow,
		Controls:   s.Controls,
		Title:      s.Title,
		FontFamily: s.FontFamily,
		Scale:      s.Scale,
	}
	if plain && spec.Frame == "window" {
		spec.Frame = "plain"
	}
	regular, bold, fontWarn := resolveFontFiles(s)
	warnings = append(warnings, fontWarn...)
	spec.FontData = regular
	spec.BoldFontData = bold
	// Only a font the user named by file is written into SVG and HTML output.
	// A font found by asking the terminal was found so the raster has icons in
	// it; copying three megabytes of it into every exported SVG is a different
	// decision, and one nobody made.
	spec.EmbedFont = s.FontFile != "" && len(regular) > 0
	in := shot.FrameInputs{Palette: p, Accents: accentsOf(p)}
	if set := theme.ResolveGlyphSet(s.GlyphSetID); set != nil {
		in.Close, in.Minimize, in.Maximize = set.Close, set.Minimize, set.Maximize
	}
	if spec.Controls == "glyphs" && in.Close == "" && in.Minimize == "" && in.Maximize == "" {
		warnings = append(warnings, "The glyph set has no window marks. The macOS lights were drawn.")
		spec.Controls = "auto"
	}
	return shot.BuildFrame(spec, in), warnings
}

// FontFallbackNotice is the one line a capture carries when it was drawn in the
// built-in face and the content needed more than that face has. It names the
// cure rather than the symptom, because "some glyphs are missing" is a thing
// the user can already see.
const FontFallbackNotice = "Icons need your terminal font. Set screenshot.font_family."

// resolveFontFiles picks the faces to rasterize with, in order of how much the
// user asked for them: an explicit font_file first, then the font the host
// terminal says it draws with, then the configured family, then nothing, which
// leaves the renderer on its embedded face.
//
// A file that will not read is a warning and a fallthrough, not a failure. The
// capture is the point; the font is how it looks.
func resolveFontFiles(s Settings) (regular, bold []byte, warnings []string) {
	if s.FontFile != "" {
		data, err := os.ReadFile(s.FontFile) // #nosec G304 - the path is the operator's own config
		if err == nil {
			return data, nil, nil
		}
		warnings = append(warnings, "The font file could not be read. Another font was used.")
	}
	if face, ok := FontByPostScriptName(s.HostFontFamily); ok {
		if data, err := os.ReadFile(face.File); err == nil { // #nosec G304 - fontconfig's own answer
			boldFace, boldOK := FontByPostScriptName(s.HostBoldFamily)
			if boldOK && boldFace.File != face.File {
				bold, _ = os.ReadFile(boldFace.File) // #nosec G304 - fontconfig's own answer
			}
			return data, bold, warnings
		}
	}
	if face, ok := FontByFamily(s.FontFamily); ok {
		if data, err := os.ReadFile(face.File); err == nil { // #nosec G304 - fontconfig's own answer
			if boldFace, boldOK := BoldFontByFamily(s.FontFamily, face.File); boldOK {
				bold, _ = os.ReadFile(boldFace.File) // #nosec G304 - fontconfig's own answer
			}
			return data, bold, warnings
		}
	}
	return nil, nil, warnings
}

// accentsOf picks the wash seeds: the theme's blue and magenta, the two
// entries a palette author treats as accent rather than as status. The wash
// derivation quiets them against the pane background, so a loud theme gets a
// quiet version of its own colours rather than a house gradient.
func accentsOf(p *shot.Palette) []shot.Color {
	return []shot.Color{p.ANSI[4], p.ANSI[5]}
}

// FileName is the generated name for a capture: a sortable timestamp plus the
// format's extension. label is the window or region name, cleaned to something
// a filesystem accepts, and is left out when it cleans away to nothing.
func FileName(label string, format shot.Format, now time.Time) string {
	stamp := now.Format("2006-01-02-150405")
	label = cleanLabel(label)
	if label == "" {
		return "tuios-" + stamp + "." + format.Ext()
	}
	return "tuios-" + label + "-" + stamp + "." + format.Ext()
}

// cleanLabel reduces a window title to a short filename-safe slug.
func cleanLabel(s string) string {
	var b strings.Builder
	lastDash := true
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			lastDash = false
		case r >= 'A' && r <= 'Z':
			b.WriteRune(r - 'A' + 'a')
			lastDash = false
		case !lastDash && b.Len() > 0:
			b.WriteByte('-')
			lastDash = true
		}
		if b.Len() >= 32 {
			break
		}
	}
	return strings.Trim(b.String(), "-")
}

// Save writes bytes to path, creating the parent directory. An empty path is
// an error rather than a guess: every caller resolves one first.
func Save(path string, data []byte) error {
	if path == "" {
		return fmt.Errorf("no output path")
	}
	if dir := filepath.Dir(path); dir != "" {
		if err := os.MkdirAll(dir, 0o750); err != nil {
			return fmt.Errorf("could not create %s: %w", dir, err)
		}
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return fmt.Errorf("could not write %s: %w", path, err)
	}
	return nil
}

// ResolvePath turns an optional explicit path and the configured directory
// into the file to write. A relative --out is relative to the process's own
// working directory, which is what a person at a shell means by it; an empty
// one lands a generated name in the configured directory.
//
// A generated name never lands on a file that already exists. The stamp in it
// has one-second resolution, and a person taking captures to compare them takes
// them faster than that, so without this the second capture of a second deleted
// the first with nothing said. An explicit --out is left exactly as given: that
// path is the caller's instruction, not a suggestion.
func ResolvePath(out, dir, label string, format shot.Format, now time.Time) (string, error) {
	if out != "" {
		abs, err := filepath.Abs(out)
		if err != nil {
			return "", fmt.Errorf("could not resolve %s: %w", out, err)
		}
		return abs, nil
	}
	if dir == "" {
		dir = config.ScreenshotConfig{}.ResolveDirectory()
	}
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return "", fmt.Errorf("could not create %s: %w", dir, err)
	}
	return freeName(dir, FileName(label, format, now)), nil
}

// freeNameLimit is how many same-second captures get a name of their own. Past
// it the name repeats, because a directory scan that never ends is worse than
// an overwritten file, and nobody takes a thousand captures in one second.
const freeNameLimit = 1000

// freeName returns name, or name with a counter before its extension when the
// directory already holds it.
//
// The name is claimed as it is chosen, with an exclusive create, rather than
// tested for absence and used a moment later. Two captures now overlap on
// purpose: the preview panel opens on the gesture and the write finishes in a
// command, so a second capture can be started while the first is still being
// written. Looking and then writing would let both look at the same empty
// second and both pick the same name, and the second write would silently take
// the first one's file. An empty file is left behind only if the write that
// follows fails outright, which is the case where there was no capture anyway.
func freeName(dir, name string) string {
	path := filepath.Join(dir, name)
	if claimName(path) {
		return path
	}
	ext := filepath.Ext(name)
	stem := strings.TrimSuffix(name, ext)
	for n := 2; n < freeNameLimit; n++ {
		candidate := filepath.Join(dir, fmt.Sprintf("%s-%d%s", stem, n, ext))
		if claimName(candidate) {
			return candidate
		}
	}
	return path
}

// claimName creates path if nothing holds it, and reports whether it did.
func claimName(path string) bool {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return false
	}
	_ = f.Close()
	return true
}
