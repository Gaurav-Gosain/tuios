// Ported from pele src/diagrams/info/render.ts at 8aff183be57021b533f319a29341a529be934b08.

package info

import (
	"math"

	"tuios.dev/sketch/internal/svg"
	"tuios.dev/sketch/internal/text"
	"tuios.dev/sketch/internal/types"
)

// version is the version of pele that sketch ports, which pele reads from its package.json.
const version = "1.12.0"

func renderInfo(model *InfoModel, _ types.Config, options types.RenderOptions) types.Rendered {
	size := types.Or(options.FontSize, 16)
	measurer := options.Measurer
	if measurer == nil {
		measurer = text.DefaultMeasurer(options.FontFamily, options.FontFamilyMono)
	}
	pad := types.Or(options.Padding, 8)

	title := text.LayoutLabel(model.title, false, measurer, size, 4000, text.Bold)
	titleHeight := 0.0
	if title.Height > 0 {
		titleHeight = title.Height + 8
	}
	line := text.LayoutLabel("Pele v"+version, false, measurer, size, 4000, 0)

	width := math.Ceil(math.Max(title.Width, line.Width) + float64(2*pad))
	height := math.Ceil(titleHeight + line.Height + float64(2*pad))
	content := svg.LabelSvg(title, width/2, pad+title.Height/2, ` class="pele-title" font-weight="var(--_tw)"`, nil) +
		svg.LabelSvg(line, width/2, pad+titleHeight+line.Height/2, ` class="pele-version" fill="var(--_m)"`, nil)

	rendered := types.Rendered{
		SVG:    svg.SvgDocument("info", width, height, size, options, svg.Accessible{AccTitle: model.accTitle, AccDescr: model.accDescr}, content),
		Width:  width,
		Height: height,
		Links:  []types.LinkInfo{},
	}
	if options.Geometry != nil {
		lines := title.Plain()
		rendered.Geometry = &types.Geometry{Width: width, Height: height, Info: &types.GeoInfo{Title: lines, Text: "Pele v" + version}}
	}
	return rendered
}
