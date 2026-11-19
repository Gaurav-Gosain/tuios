package config

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/Gaurav-Gosain/tuios/internal/jsonx"
)

// PiPConfig is the [pip] section: the picture-in-picture view, a small live
// copy of one pane drawn in a corner of the screen while another pane has the
// focus.
//
// Nothing here is session state. The view is what this client's screen shows,
// like the spotlight, so a second client attached to the same session keeps
// its own view (or none).
type PiPConfig struct {
	// Width and Height are the size of the whole box, border included: a
	// number of cells (40) or a percent of the pane area ("30%")
	// (default: 40x12). The box shrinks to fit a smaller screen.
	Width  PiPSize `toml:"width"`
	Height PiPSize `toml:"height"`
	// Corner is where the box goes first (default: bottom-right). When the
	// focused pane's cursor enters the box, the box moves to another place.
	Corner string `toml:"corner"`
}

// PiPSize is pip.width or pip.height: a whole number of cells, or a percent
// of the pane area such as "30%" (issue #2250). The file may write a number
// bare (width = 40) or quoted; go-toml hands a bare number to UnmarshalText.
// A value that ParseBoxSize does not read is kept as written, so validation
// can name it, and the view uses the default.
type PiPSize string

// UnmarshalText keeps the value as written, trimmed.
func (s *PiPSize) UnmarshalText(text []byte) error {
	*s = PiPSize(strings.TrimSpace(string(text)))
	return nil
}

// MarshalTOML writes a number of cells bare and a percent quoted, so a file
// tuios saves reads the way a person would have written it. It takes effect
// through MarshalUserConfig.
func (s PiPSize) MarshalTOML() ([]byte, error) {
	if n, percent, err := ParseBoxSize(string(s)); err == nil && !percent {
		return []byte(strconv.Itoa(n)), nil
	}
	// A JSON string is a TOML basic string: the same quotes and escapes, and
	// encoding/json never writes the one escape TOML lacks (\/).
	return jsonx.Marshal(string(s))
}

// cells is the size in cells for an area total cells long: a number of
// cells within lo and hi, or a percent of total, at least lo. A value that
// does not read is def.
func (s PiPSize) cells(total, def, lo, hi int) int {
	n, percent, err := ParseBoxSize(string(s))
	if err != nil {
		return def
	}
	if percent {
		return max(total*n/100, lo)
	}
	return min(max(n, lo), hi)
}

// The places the box can go first, in the spelling [pip] corner takes. The
// two centre places sit in the middle of the top or the bottom edge.
const (
	PiPCornerBottomRight  = "bottom-right"
	PiPCornerBottomLeft   = "bottom-left"
	PiPCornerTopRight     = "top-right"
	PiPCornerTopLeft      = "top-left"
	PiPCornerTopCenter    = "top-center"
	PiPCornerBottomCenter = "bottom-center"
)

// PiPCorners is what pip.corner accepts, shared by the registry, the validator
// and the settings page.
var PiPCorners = []string{
	PiPCornerBottomRight, PiPCornerBottomLeft, PiPCornerTopRight, PiPCornerTopLeft,
	PiPCornerTopCenter, PiPCornerBottomCenter,
}

// PiP defaults and bounds. The minimum leaves a one-row, ten-column view inside
// the border, which is the least that still shows a line of output. The
// maximum holds for a size in cells. A percent is bounded by the pane area.
const (
	PiPDefaultWidth  = 40
	PiPDefaultHeight = 12
	PiPMinWidth      = 12
	PiPMaxWidth      = 200
	PiPMinHeight     = 3
	PiPMaxHeight     = 100
)

// defaultPiPConfig returns the section DefaultConfig carries.
func defaultPiPConfig() PiPConfig {
	return PiPConfig{
		Width:  PiPSize(strconv.Itoa(PiPDefaultWidth)),
		Height: PiPSize(strconv.Itoa(PiPDefaultHeight)),
		Corner: PiPCornerBottomRight,
	}
}

// fillMissingPiP fills absent values with the defaults. A value that is
// there but wrong stays, so validatePiP can name it. The view reads it as the
// default.
func fillMissingPiP(cfg, defaultCfg *UserConfig) {
	p, d := &cfg.PiP, &defaultCfg.PiP
	if strings.TrimSpace(string(p.Width)) == "" {
		p.Width = d.Width
	}
	if strings.TrimSpace(string(p.Height)) == "" {
		p.Height = d.Height
	}
	if !validPiPCorner(p.Corner) {
		p.Corner = d.Corner
	}
}

// validatePiP warns about a size that does not read, and about a number of
// cells outside its range. Without a warning, a typo would look like the key
// doing nothing.
func validatePiP(cfg *UserConfig, result *ValidationResult) {
	for _, f := range []struct {
		key    string
		value  PiPSize
		def    int
		lo, hi int
	}{
		{"width", cfg.PiP.Width, PiPDefaultWidth, PiPMinWidth, PiPMaxWidth},
		{"height", cfg.PiP.Height, PiPDefaultHeight, PiPMinHeight, PiPMaxHeight},
	} {
		if strings.TrimSpace(string(f.value)) == "" {
			continue
		}
		n, percent, err := ParseBoxSize(string(f.value))
		var msg string
		switch {
		case err != nil:
			msg = fmt.Sprintf("%v. The view uses %d.", err, f.def)
		case !percent && (n < f.lo || n > f.hi):
			msg = fmt.Sprintf("%d is not in the range %d to %d. The view uses %d.", n, f.lo, f.hi, min(max(n, f.lo), f.hi))
		default:
			continue
		}
		result.Warnings = append(result.Warnings, ValidationError{Field: "pip", Key: f.key, Message: msg})
	}
}

// CornerName is the place the box goes to first, defaulting to bottom-right
// for an empty or unknown value.
func (p PiPConfig) CornerName() string {
	if validPiPCorner(p.Corner) {
		return p.Corner
	}
	return PiPCornerBottomRight
}

// SizeIn is the box size, border included, for a pane area of areaW by areaH
// cells, with the defaults and bounds applied, so a config that was never
// filled still draws a sane box. A percent is a share of the area.
func (p PiPConfig) SizeIn(areaW, areaH int) (w, h int) {
	return p.Width.cells(areaW, PiPDefaultWidth, PiPMinWidth, PiPMaxWidth),
		p.Height.cells(areaH, PiPDefaultHeight, PiPMinHeight, PiPMaxHeight)
}

func validPiPCorner(c string) bool { return slices.Contains(PiPCorners, c) }
