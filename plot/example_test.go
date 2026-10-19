package plot_test

import (
	"fmt"

	"tuios.dev/plot"
)

func Example() {
	bar := plot.Bar{
		Values:     []float64{6.2, 4.8, 3.1},
		Labels:     []string{"session", "app", "vt"},
		Horizontal: true,
	}
	g := bar.Draw(30, 3, plot.Blocks)
	fmt.Println(g.String())
	// The role of each cell names a theme colour for the host to map.
	fmt.Println(g.At(8, 0).Role)
	// Output:
	// session ██████████████████ 6.2
	// app     █████████████▉     4.8
	// vt      █████████          3.1
	// series1
}

func ExampleSparkline() {
	g := plot.Sparkline{Values: []float64{2, 5, 3, 8, 4, 9}}.Draw(6, 1, plot.Braille)
	fmt.Println(g.String())
	g = plot.Sparkline{Values: []float64{2, 5, 3, 8, 4, 9}}.Draw(6, 1, plot.ASCII)
	fmt.Println(g.String())
	// Output:
	// ▁▄▂▇▃█
	// _~.*-#
}
