// Ported from pele src/svg/cluster.ts at 458322a5b8abb1ae57ddf127c32017ab9fb14a92.

package svg

import (
	"math"

	"tuios.dev/sketch/internal/js"
	"tuios.dev/sketch/internal/layout"
	"tuios.dev/sketch/internal/text"
)

// Crossings holds the x of every route point, by its rounded y: the edges that cross a cluster's top border.
type Crossings map[float64][]float64

func MarkCrossings(crossings Crossings, route []float64) {
	for k := 0; k < len(route); k += 3 {
		key := js.Round(route[k+1])
		crossings[key] = append(crossings[key], route[k])
	}
}

// ClusterTitleX picks where a cluster title sits along the top edge so that edges entering there do not cross it.
func ClusterTitleX(c *layout.CNode, width float64, crossings Crossings, pad float64) float64 {
	if width == 0 {
		return c.X
	}
	top := c.Y - c.H/2
	left := c.X - c.W/2
	half := width / 2
	candidates := [3]float64{left + pad - 6 + half, c.X, left + c.W - pad + 6 - half}
	xs, ok := crossings[js.Round(top)]
	if !ok {
		return candidates[0]
	}
	best := candidates[0]
	bestGap := -1.0
	for _, x := range candidates {
		gap := math.Inf(1)
		for _, cx := range xs {
			if cx > left && cx < left+c.W {
				gap = math.Min(gap, math.Abs(cx-x)-half)
			}
		}
		if gap >= 6 {
			return x
		}
		if gap > bestGap+0.5 {
			bestGap = gap
			best = x
		}
	}
	return best
}

// TitleCrossed is whether an edge that enters at the top runs through a title placed at `x`.
func TitleCrossed(c *layout.CNode, width, x float64, crossings Crossings) bool {
	if width <= 0 {
		return false
	}
	xs, ok := crossings[js.Round(c.Y-c.H/2)]
	if !ok {
		return false
	}
	left := c.X - c.W/2
	for _, cx := range xs {
		if cx > left && cx < left+c.W && math.Abs(cx-x) < width/2+4 {
			return true
		}
	}
	return false
}

// StruckTitle is a title that an edge runs through, to be drawn over the edges: the text, on an outline in the
// color of the cluster behind it, so that only the line is seen to stop short of the letters.
// `parents` and `index` find how many clusters deep the title is, each of which tints the background.
func StruckTitle(label text.Label, x, y float64, attrs string, id string, parents []int, index int, icons IconResolver) string {
	depth := 1
	for p := parents[index]; p >= 0 && depth < 8; p = parents[p] {
		depth++
	}
	// Each cluster is half its fill over what is behind it.
	fill := "color-mix(in srgb,var(--_a) " + Num(100*(1-math.Pow(0.5, float64(depth)))) + "%,var(--_bg))"
	return `<g class="pele-cluster-title" data-id="` + id + `">` +
		LabelSvg(label, x, y, ` fill="none" stroke="`+fill+`" stroke-width="5" stroke-linejoin="round"`, icons) +
		LabelSvg(label, x, y, attrs, icons) +
		`</g>`
}
