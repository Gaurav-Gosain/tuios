// Ported from pele src/diagrams/common/fit-width.ts at 8aff183be57021b533f319a29341a529be934b08.

// Package common holds what several diagram types share.
package common

import (
	"math"

	"tuios.dev/sketch/internal/types"
)

// FitWidth draws a chart whose plot can be any width so that the whole diagram fits the width the host
// has. What surrounds the plot (labels, a legend) is only known once it is drawn, so the plot
// gives up the overflow and is drawn again. `narrow` tells the chart that it did not fit at
// its own width, for a chart that then arranges itself differently.
func FitWidth(options types.RenderOptions, natural, least float64, draw func(plot float64, narrow bool) types.Rendered) types.Rendered {
	result := draw(natural, false)
	if options.MaxWidth == nil || !(*options.MaxWidth > 0) || result.Width <= *options.MaxWidth {
		return result
	}
	available := *options.MaxWidth
	floor := math.Min(least, natural)
	plot := natural
	result = draw(plot, true)
	for round := 0; round < 3 && result.Width > available && plot > floor; round++ {
		plot = math.Max(floor, plot-(result.Width-available))
		result = draw(plot, true)
	}
	return result
}

// TitleRoom is the width a title may take before it wraps.
func TitleRoom(options types.RenderOptions) float64 {
	if options.MaxWidth != nil && *options.MaxWidth > 0 {
		return math.Max(80, *options.MaxWidth-float64(2*types.Or(options.Padding, 8)))
	}
	return 4000
}

// CapWidth is for a chart that is drawn to a set width: its own, or the host's when that is smaller.
// `around` is what the chart adds outside that width.
func CapWidth(options types.RenderOptions, natural, least, around float64) float64 {
	if options.MaxWidth == nil || !(*options.MaxWidth > 0) {
		return natural
	}
	return math.Max(math.Min(least, natural), math.Min(natural, *options.MaxWidth-around))
}

const BREAKPOINT = 640

// TurnToFit is for a diagram that can run across or down: draws it across, and down instead when the host
// allows it, the width it has is under the breakpoint, across does not fit, and down is narrower.
func TurnToFit(options types.RenderOptions, draw func(down bool) types.Rendered) types.Rendered {
	across := draw(false)
	if (options.AutoDirection != nil && !*options.AutoDirection) || options.MaxWidth == nil || !(*options.MaxWidth > 0) || across.Width <= *options.MaxWidth {
		return across
	}
	available := *options.MaxWidth
	breakpoint := float64(BREAKPOINT)
	if options.DirectionBreakpoint != nil && *options.DirectionBreakpoint > 0 {
		breakpoint = *options.DirectionBreakpoint
	}
	if available >= breakpoint {
		return across
	}
	down := draw(true)
	if down.Width < across.Width {
		return down
	}
	return across
}

func RunsAcross(direction string) bool {
	return direction == "LR" || direction == "RL"
}

// Tighten draws a diagram with room to spare, and tightly instead when that does not fit the width the
// host has and tight is narrower.
func Tighten(options types.RenderOptions, draw func(tight bool) types.Rendered) types.Rendered {
	loose := draw(false)
	if options.MaxWidth == nil || !(*options.MaxWidth > 0) || loose.Width <= *options.MaxWidth {
		return loose
	}
	tight := draw(true)
	if tight.Width < loose.Width {
		return tight
	}
	return loose
}
