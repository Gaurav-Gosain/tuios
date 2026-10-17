package shot_test

import (
	"fmt"
	"os"

	"tuios.dev/shot"
)

// Build a grid cell by cell and render it to PNG. A nil frame renders the
// bare grid.
func ExampleRender() {
	g := shot.NewGrid(20, 1, shot.RGB(0xe5, 0xe5, 0xe5), shot.RGB(0, 0, 0))
	for x, r := range []rune("hello, world") {
		g.Cells[0][x].Cluster = string(r)
	}
	g.Cells[0][0].Bold = true
	png, err := shot.Render(shot.FormatPNG, g, nil)
	if err != nil {
		panic(err)
	}
	if err := os.WriteFile("hello.png", png, 0o644); err != nil {
		panic(err)
	}
}

// Dress a grid as a window, with the gradient and the controls in the
// palette's colours.
func ExampleBuildFrame() {
	p := shot.XTermPalette()
	g := shot.NewGrid(40, 10, p.FG, p.BG)
	frame := shot.BuildFrame(shot.FrameSpec{
		Frame:   "window",
		Title:   "build",
		Padding: 24,
		Radius:  10,
		Shadow:  true,
		Scale:   2,
	}, shot.FrameInputs{Palette: p})
	svg, err := shot.Render(shot.FormatSVG, g, frame)
	if err != nil {
		panic(err)
	}
	fmt.Println(len(svg) > 0)
}

// Find the glyphs that no face of a frame has, before the render draws them
// as boxes. A program with a font database adds a fallback face for each.
func ExampleMissingGlyphs() {
	g := shot.NewGrid(8, 1, shot.RGB(0xe5, 0xe5, 0xe5), shot.RGB(0, 0, 0))
	g.Cells[0][0].Cluster = "あ"
	g.Cells[0][0].Width = 2
	g.Cells[0][1].Width = 0
	frame := &shot.Frame{}
	missing, err := shot.MissingGlyphs(g, frame)
	if err != nil {
		panic(err)
	}
	for _, r := range missing {
		// Ask the font database for a face that has r, then:
		//   frame.Fallback = append(frame.Fallback, shot.Font{Data: data})
		fmt.Printf("%U\n", r)
	}
}
