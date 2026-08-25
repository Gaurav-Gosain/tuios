package config

import (
	"os"
	"path/filepath"
	"strings"
)

// ScreenshotConfig is the [screenshot] section: how a capture renders and
// where it lands. Pointer fields distinguish "unset" from an explicit zero,
// so `padding = 0` and `copy = false` survive a reload instead of snapping
// back to the defaults.
type ScreenshotConfig struct {
	Format      string `toml:"format"`       // png | svg | ansi | html | txt (default: png)
	Copy        *bool  `toml:"copy"`         // attempt a clipboard copy after capture (default: true)
	Preview     *bool  `toml:"preview"`      // open the preview panel after capture (default: true)
	Directory   string `toml:"directory"`    // where files land (default: ~/Pictures/tuios)
	Frame       string `toml:"frame"`        // window | plain | none (default: window)
	Background  string `toml:"background"`   // auto | none | hex | hex..hex (default: auto)
	Padding     *int   `toml:"padding"`      // px around the card, 0..128 (default: 48)
	Radius      *int   `toml:"radius"`       // card corner radius px, 0..32 (default: 10)
	Shadow      *bool  `toml:"shadow"`       // drop shadow under the card (default: true)
	Controls    string `toml:"controls"`     // auto | macos | glyphs | none (default: auto)
	TitleFormat string `toml:"title_format"` // window_title_format tokens (default: {title})
	FontFamily  string `toml:"font_family"`  // SVG/HTML font stack (default: JetBrains Mono, monospace)
	FontFile    string `toml:"font_file"`    // a .ttf to embed in SVG and rasterize PNG with
	Scale       *int   `toml:"scale"`        // PNG raster scale, 1..4 (default: 2)
	Cursor      bool   `toml:"cursor"`       // draw the cursor cell (default: false)
}

// Screenshot defaults, one source for DefaultConfig and the accessors.
const (
	ScreenshotDefaultFormat      = "png"
	ScreenshotDefaultDirectory   = "~/Pictures/tuios"
	ScreenshotDefaultFrame       = "window"
	ScreenshotDefaultBackground  = "auto"
	ScreenshotDefaultPadding     = 48
	ScreenshotDefaultRadius      = 10
	ScreenshotDefaultControls    = "auto"
	ScreenshotDefaultTitleFormat = "{title}"
	ScreenshotDefaultFontFamily  = "JetBrains Mono, monospace"
	ScreenshotDefaultScale       = 2
	ScreenshotMaxPadding         = 128
	ScreenshotMaxRadius          = 32
	ScreenshotMaxScale           = 4
)

// Enum values, shared by the registry and the verb.
var (
	ScreenshotFormats    = []string{"png", "svg", "ansi", "html", "txt"}
	ScreenshotFrames     = []string{"window", "plain", "none"}
	ScreenshotControlSet = []string{"auto", "macos", "glyphs", "none"}
)

// defaultScreenshotConfig returns the section DefaultConfig carries.
func defaultScreenshotConfig() ScreenshotConfig {
	return ScreenshotConfig{
		Format:      ScreenshotDefaultFormat,
		Directory:   ScreenshotDefaultDirectory,
		Frame:       ScreenshotDefaultFrame,
		Background:  ScreenshotDefaultBackground,
		Controls:    ScreenshotDefaultControls,
		TitleFormat: ScreenshotDefaultTitleFormat,
		FontFamily:  ScreenshotDefaultFontFamily,
	}
}

// fillMissingScreenshot fills empty strings with defaults. Pointer fields
// stay nil deliberately: the accessors below and the option registry both
// treat nil as "the default", so an absent key needs no repair.
func fillMissingScreenshot(cfg, defaultCfg *UserConfig) {
	s, d := &cfg.Screenshot, &defaultCfg.Screenshot
	if s.Format == "" {
		s.Format = d.Format
	}
	if s.Directory == "" {
		s.Directory = d.Directory
	}
	if s.Frame == "" {
		s.Frame = d.Frame
	}
	if s.Background == "" {
		s.Background = d.Background
	}
	if s.Controls == "" {
		s.Controls = d.Controls
	}
	if s.TitleFormat == "" {
		s.TitleFormat = d.TitleFormat
	}
	if s.FontFamily == "" {
		s.FontFamily = d.FontFamily
	}
}

// Effective-value accessors: nil means the default, and every reader goes
// through these so nothing re-derives the fallback.

// CopyEnabled reports whether a capture should attempt a clipboard copy.
func (s ScreenshotConfig) CopyEnabled() bool { return s.Copy == nil || *s.Copy }

// PreviewEnabled reports whether the preview panel opens after a capture.
func (s ScreenshotConfig) PreviewEnabled() bool { return s.Preview == nil || *s.Preview }

// PaddingPx is the effective card padding.
func (s ScreenshotConfig) PaddingPx() int {
	if s.Padding == nil {
		return ScreenshotDefaultPadding
	}
	return clampRange(*s.Padding, 0, ScreenshotMaxPadding)
}

// RadiusPx is the effective corner radius.
func (s ScreenshotConfig) RadiusPx() int {
	if s.Radius == nil {
		return ScreenshotDefaultRadius
	}
	return clampRange(*s.Radius, 0, ScreenshotMaxRadius)
}

// ShadowEnabled reports whether the card gets a drop shadow.
func (s ScreenshotConfig) ShadowEnabled() bool { return s.Shadow == nil || *s.Shadow }

// ScaleFactor is the effective PNG raster scale.
func (s ScreenshotConfig) ScaleFactor() int {
	if s.Scale == nil {
		return ScreenshotDefaultScale
	}
	return clampRange(*s.Scale, 1, ScreenshotMaxScale)
}

// EffectiveFormat is the format with the default applied.
func (s ScreenshotConfig) EffectiveFormat() string {
	if s.Format == "" {
		return ScreenshotDefaultFormat
	}
	return s.Format
}

// ResolveDirectory expands the configured directory to an absolute path,
// with ~ resolved against the process's home.
func (s ScreenshotConfig) ResolveDirectory() string {
	dir := s.Directory
	if dir == "" {
		dir = ScreenshotDefaultDirectory
	}
	if strings.HasPrefix(dir, "~") {
		if home, err := os.UserHomeDir(); err == nil {
			dir = filepath.Join(home, strings.TrimPrefix(dir, "~"))
		}
	}
	if abs, err := filepath.Abs(dir); err == nil {
		return abs
	}
	return dir
}

func clampRange(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
