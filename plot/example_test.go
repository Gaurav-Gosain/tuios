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

func ExampleMirror() {
	m := plot.Mirror{
		Up:   plot.Series{Name: "down", Values: []float64{1, 3, 8, 4, 2, 6, 9, 5}},
		Down: plot.Series{Name: "up", Values: []float64{1, 1, 2, 6, 1, 0, 2, 1}},
	}
	g := m.Draw(4, 6, plot.Braille)
	fmt.Println(g.String())
	// Each half grades by height: the far row has the series role, the
	// rows toward the baseline take its fades.
	fmt.Println(g.At(3, 1).Role, g.At(3, 2).Role)
	// Output:
	// down
	//  ⡆⢀⡇
	// ⣰⣿⣼⣿
	// ⠉⢿⠁⠏
	//  ⢸
	// up
	// series1 series1/2
}

func ExampleFade() {
	r := plot.Fade(plot.Series1, 2)
	base, k := r.Faded()
	fmt.Println(r, base, k)
	// Output:
	// series1/2 series1 2
}

func ExampleInlineBar() {
	// A share column: every row has the same Max, so the bars compare.
	for _, v := range []float64{30, 12.4, 3.1} {
		g := plot.InlineBar{Value: v, Max: 30}.Draw(8, 1, plot.Blocks)
		fmt.Println(g.String())
	}
	// Output:
	// ▄▄▄▄▄▄▄▄
	// ▄▄▄▖
	// ▄
}

func ExampleDivergingBar() {
	// Download grows left of the centre and upload right.
	g := plot.DivergingBar{Left: 24, Right: 9, Max: 24}.Draw(11, 1, plot.Blocks)
	fmt.Println(g.String())
	// Output:
	// ▄▄▄▄▄┊▄▄
}

func ExampleMap() {
	mp := plot.Map{
		Values:  map[string]float64{"US": 410, "DE": 120, "BR": 6, "JP": 18},
		Markers: []plot.Marker{{Lat: 1.35, Lon: 103.82}}, // Singapore
	}
	g := mp.Draw(24, 5, plot.Braille)
	fmt.Println(g.String())
	for _, it := range mp.Legend(plot.Braille) {
		fmt.Println(it.Role, it.Label)
	}
	// Output:
	// ⣰⣶⢶⣴⣶⣾⣟⣏⠻⠟⠋⢀⣠⣴⣤⣴⣴⣶⣶⣶⣶⡶⢦⢤
	//  ⢀ ⠘⠿⡿⠿⠋   ⣼⣿⣽⣿⣿⢿⣿⣿⡿⠻⠋
	//       ⠱⣷⣶⣤ ⠙⠹⣿⣿⠁ ⠁⠘⠵⢀⠤
	//        ⡿⠛⠁   ⠛⠁⠁   ⠘⠛⠻⠂
	//
	// heat1 0
	// heat2 ≥ 82
	// heat3 ≥ 160
	// heat4 ≥ 250
	// heat5 ≥ 330
}
