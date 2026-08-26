package tfx

import (
	"fmt"
	"strconv"
	"strings"
)

// Color is a 24-bit RGB colour. Upstream also carries the original constructor
// argument so that Color(255) and Color("ffffff") hash apart; that only
// mattered for Python dict keying during parity checks, so it is dropped here.
type Color struct {
	R uint8
	G uint8
	B uint8
}

// RGB builds a Color from channel values.
func RGB(r, g, b uint8) Color { return Color{R: r, G: g, B: b} }

// ParseColor reads a colour from a hex string, with or without a leading hash.
func ParseColor(hex string) (Color, error) {
	s := strings.TrimPrefix(strings.TrimSpace(hex), "#")
	if len(s) != 6 {
		return Color{}, fmt.Errorf("colour must be six hex digits, got %q", hex)
	}
	value, err := strconv.ParseUint(s, 16, 32)
	if err != nil {
		return Color{}, fmt.Errorf("colour must be six hex digits, got %q", hex)
	}
	return Color{R: uint8(value >> 16), G: uint8(value >> 8), B: uint8(value)}, nil
}

// MustParseColor is ParseColor for compile-time constants. It panics on a bad
// string, so only pass literals.
func MustParseColor(hex string) Color {
	c, err := ParseColor(hex)
	if err != nil {
		panic(err)
	}
	return c
}

// Hex renders the colour as six lowercase hex digits, with no leading hash.
func (c Color) Hex() string { return fmt.Sprintf("%02x%02x%02x", c.R, c.G, c.B) }

// ColorPair holds an optional foreground and an optional background.
type ColorPair struct {
	Fg    Color
	HasFg bool
	Bg    Color
	HasBg bool
}

// Fg builds a foreground-only pair.
func Fg(c Color) ColorPair { return ColorPair{Fg: c, HasFg: true} }

// Bg builds a background-only pair.
func Bg(c Color) ColorPair { return ColorPair{Bg: c, HasBg: true} }

// FgBg builds a pair with both channels set.
func FgBg(fg, bg Color) ColorPair {
	return ColorPair{Fg: fg, HasFg: true, Bg: bg, HasBg: true}
}

// GradientDirection is the axis a coordinate colour mapping runs along.
type GradientDirection int

// The gradient axes.
const (
	Vertical GradientDirection = iota
	Horizontal
	Radial
	Diagonal
)

var gradientDirectionNames = map[string]GradientDirection{
	"vertical":   Vertical,
	"horizontal": Horizontal,
	"radial":     Radial,
	"diagonal":   Diagonal,
}

// ParseGradientDirection looks up a gradient axis by its upstream name.
func ParseGradientDirection(name string) (GradientDirection, bool) {
	d, ok := gradientDirectionNames[name]
	return d, ok
}

// Gradient is a precomputed list of colours between a set of stops.
type Gradient struct {
	Spectrum []Color
}

// NewGradient builds a spectrum from stops and a per-pair step count.
//
// The channel deltas use integer floor division rather than a float lerp, and
// the exact end stop is appended after each pair. That is upstream's
// arithmetic. It biases every ramp slightly and gives the spectrum its
// characteristic banding, so it is reproduced rather than corrected.
func NewGradient(stops []Color, steps []int, doLoop bool) (*Gradient, error) {
	if len(stops) == 0 {
		return nil, fmt.Errorf("a gradient needs at least one stop")
	}
	if len(steps) == 0 {
		return nil, fmt.Errorf("a gradient needs at least one step count")
	}
	for _, step := range steps {
		if step < 1 {
			return nil, fmt.Errorf("gradient steps must be at least 1, got %d", step)
		}
	}
	if len(stops) == 1 {
		spectrum := make([]Color, steps[0])
		for i := range spectrum {
			spectrum[i] = stops[0]
		}
		return &Gradient{Spectrum: spectrum}, nil
	}

	work := append([]Color(nil), stops...)
	if doLoop {
		work = append(work, work[0])
	}
	pairCount := len(work) - 1
	pairSteps := append([]int(nil), steps...)
	if len(pairSteps) > pairCount {
		pairSteps = pairSteps[:pairCount]
	}
	for len(pairSteps) < pairCount {
		pairSteps = append(pairSteps, pairSteps[len(pairSteps)-1])
	}

	var spectrum []Color
	for pairIndex, stepCount := range pairSteps {
		start := work[pairIndex]
		end := work[pairIndex+1]
		redDelta := floorDiv(int(end.R)-int(start.R), stepCount)
		greenDelta := floorDiv(int(end.G)-int(start.G), stepCount)
		blueDelta := floorDiv(int(end.B)-int(start.B), stepCount)
		rangeStart := 0
		if len(spectrum) > 0 {
			rangeStart = 1
		}
		for i := rangeStart; i < stepCount; i++ {
			spectrum = append(spectrum, Color{
				R: clampChannel(int(start.R) + redDelta*i),
				G: clampChannel(int(start.G) + greenDelta*i),
				B: clampChannel(int(start.B) + blueDelta*i),
			})
		}
		spectrum = append(spectrum, end)
	}
	return &Gradient{Spectrum: spectrum}, nil
}

// NewGradientSteps is NewGradient with one step count for every pair, which is
// how nearly every effect calls it.
func NewGradientSteps(stops []Color, steps int, doLoop bool) (*Gradient, error) {
	return NewGradient(stops, []int{steps}, doLoop)
}

func clampChannel(v int) uint8 {
	if v < 0 {
		return 0
	}
	if v > 255 {
		return 255
	}
	return uint8(v)
}

// ColorAtFraction picks the spectrum entry covering a position in [0,1].
func (g *Gradient) ColorAtFraction(fraction float64) Color {
	if len(g.Spectrum) == 0 {
		return Color{}
	}
	if fraction < 0 {
		fraction = 0
	}
	if fraction > 1 {
		fraction = 1
	}
	n := len(g.Spectrum)
	for i := 1; i <= n; i++ {
		if fraction <= float64(i)/float64(n) {
			return g.Spectrum[i-1]
		}
	}
	return g.Spectrum[n-1]
}

// CoordColorMap maps canvas coordinates to gradient colours.
type CoordColorMap map[Coord]Color

// At reads a coordinate, falling back to the first spectrum colour for coords
// outside the mapped rectangle. Effects index this map by input coordinate and
// a miss would otherwise show as a black character.
func (m CoordColorMap) At(coord Coord, fallback Color) Color {
	if c, ok := m[coord]; ok {
		return c
	}
	return fallback
}

// BuildCoordinateColorMapping paints a gradient across a rectangle.
func (g *Gradient) BuildCoordinateColorMapping(minRow, maxRow, minColumn, maxColumn int, direction GradientDirection) (CoordColorMap, error) {
	if maxRow < 1 || maxColumn < 1 || minRow < 1 || minColumn < 1 {
		return nil, fmt.Errorf("gradient rectangle bounds must be at least 1")
	}
	if minRow > maxRow || minColumn > maxColumn {
		return nil, fmt.Errorf("gradient rectangle minimums must not exceed its maximums")
	}
	rowOffset := minRow - 1
	columnOffset := minColumn - 1
	mapping := make(CoordColorMap, (maxRow-minRow+1)*(maxColumn-minColumn+1))
	switch direction {
	case Vertical:
		for row := minRow; row <= maxRow; row++ {
			fraction := float64(row-rowOffset) / float64(maxRow-rowOffset)
			color := g.ColorAtFraction(fraction)
			for column := minColumn; column <= maxColumn; column++ {
				mapping[C(column, row)] = color
			}
		}
	case Horizontal:
		for column := minColumn; column <= maxColumn; column++ {
			fraction := float64(column-columnOffset) / float64(maxColumn-columnOffset)
			color := g.ColorAtFraction(fraction)
			for row := minRow; row <= maxRow; row++ {
				mapping[C(column, row)] = color
			}
		}
	case Radial:
		for row := minRow; row <= maxRow; row++ {
			for column := minColumn; column <= maxColumn; column++ {
				distance, ok := FindNormalizedDistanceFromCenter(minRow, maxRow, minColumn, maxColumn, C(column, row))
				if !ok {
					continue
				}
				mapping[C(column, row)] = g.ColorAtFraction(distance)
			}
		}
	case Diagonal:
		for row := minRow; row <= maxRow; row++ {
			for column := minColumn; column <= maxColumn; column++ {
				numerator := (row-rowOffset)*2 + (column - columnOffset)
				denominator := (maxRow-rowOffset)*2 + (maxColumn - columnOffset)
				mapping[C(column, row)] = g.ColorAtFraction(float64(numerator) / float64(denominator))
			}
		}
	}
	return mapping, nil
}

// ShiftColorTowards interpolates between two colours.
//
// Upstream truncates back to an integer channel rather than rounding, so a
// shift lands one value low most of the time. Kept for the same reason as the
// gradient floor division.
func ShiftColorTowards(color, target Color, factor float64) Color {
	shift := func(start, end uint8) uint8 {
		s := float64(start) / 255
		e := float64(end) / 255
		v := int((s + (e-s)*factor) * 255)
		return clampChannel(v)
	}
	return Color{
		R: shift(color.R, target.R),
		G: shift(color.G, target.G),
		B: shift(color.B, target.B),
	}
}
