# shot

[![Go Reference](https://pkg.go.dev/badge/tuios.dev/shot.svg)](https://pkg.go.dev/tuios.dev/shot)

`tuios.dev/shot` renders a terminal screen to a picture. It takes a grid of
cells and writes PNG, SVG, HTML, ANSI or plain text, with an optional window
frame around it.

- PNG is drawn in Go, with no browser, no cgo and no external tool.
- SVG uses real `<text>` and `<rect>` elements, so design tools and browsers
  open it, and the text stays selectable.
- Box drawing, block elements, braille and powerline glyphs are drawn as
  shapes, so borders join up in any font.
- A glyph the font does not have comes from a fallback face. A glyph that no
  face has draws as a dotted box, so a gap in the picture is never silent.

The `mosaic` package does the opposite: it draws a picture as cells of block
glyphs, the way chafa's symbol mode does.

tuios uses shot for `tuios screenshot` and the screenshot keys.

## Install

```sh
go get tuios.dev/shot
```

The go command reads the import path from tuios.dev. Go 1.25 or later can
fetch it directly. With the default module proxy, any Go version can fetch it.

## Render an ANSI string to PNG and SVG

shot does not parse escape sequences. A terminal emulator does that, and
`FromScreen` reads its screen. This example uses
[tuios.dev/vt](https://pkg.go.dev/tuios.dev/vt).

```go
package main

import (
	"log"
	"os"

	"tuios.dev/shot"
	"tuios.dev/vt"
)

func main() {
	// A terminal emulator reads what a program printed.
	term := vt.NewEmulator(46, 6)
	defer term.Close()
	term.Write([]byte("\x1b[35m◆\x1b[0m \x1b[1mmain\x1b[0m \x1b[2m▸\x1b[0m ~/dev/tuios\r\n" +
		"$ go test ./shot/...\r\n" +
		"\x1b[32mok\x1b[0m  tuios.dev/shot         0.012s\r\n" +
		"\x1b[32mok\x1b[0m  tuios.dev/shot/mosaic  0.004s\r\n" +
		"\x1b[44;30m ▤ 2 packages \x1b[0m \x1b[33m╭─╮\x1b[0m \x1b[2m⣿⣶⣤⣀\x1b[0m\r\n$ "))

	// The palette turns the basic and indexed colours into RGB.
	p := shot.XTermPalette()
	p.FG, p.BG = shot.RGB(0xcd, 0xd6, 0xf4), shot.RGB(0x1e, 0x1e, 0x2e)
	p.ANSI[2], p.ANSI[3] = shot.RGB(0xa6, 0xe3, 0xa1), shot.RGB(0xf9, 0xe2, 0xaf)
	p.ANSI[4], p.ANSI[5] = shot.RGB(0x89, 0xb4, 0xfa), shot.RGB(0xcb, 0xa6, 0xf7)

	// FromScreen reads the screen into a grid of resolved cells.
	grid := shot.FromScreen(term, p)

	// BuildFrame dresses the grid as a window on a gradient.
	frame := shot.BuildFrame(shot.FrameSpec{
		Frame: "window", Title: "tuios", Padding: 24, Radius: 10, Shadow: true, Scale: 2,
	}, shot.FrameInputs{Palette: p})

	for _, format := range []shot.Format{shot.FormatPNG, shot.FormatSVG} {
		out, err := shot.Render(format, grid, frame)
		if err != nil {
			log.Fatal(err)
		}
		if err := os.WriteFile("screen."+format.Ext(), out, 0o644); err != nil {
			log.Fatal(err)
		}
	}
}
```

## Build a grid cell by cell

A program that keeps its own screen model fills a grid directly. Each cell
holds its text, its colours as RGB and its attributes.

```go
g := shot.NewGrid(20, 1, shot.RGB(0xe5, 0xe5, 0xe5), shot.RGB(0, 0, 0))
for x, r := range "hello, world" {
	g.Cells[0][x].Cluster = string(r)
}
g.Cells[0][0].Bold = true
png, err := shot.Render(shot.FormatPNG, g, nil) // a nil frame is the bare grid
```

`MakeCell` resolves one cell of an [ultraviolet](https://github.com/charmbracelet/ultraviolet)
screen, with its style and link, through a palette.

## Fonts

PNG looks for each glyph in this order, and the first face that has the glyph
draws it:

1. `Frame.Font`, and `Frame.BoldFont` for a bold cell. Without a font, Go Mono.
2. Each face in `Frame.Fallback`, in order.
3. Go Mono, which shot embeds.

A glyph in no face draws as a dotted box. `MissingGlyphs` lists those glyphs
before you render, so you can find a font for each one and add it:

```go
font, _ := os.ReadFile("GeistMonoNerdFontMono-Regular.otf")
frame.Font = shot.Font{Data: font}
missing, _ := shot.MissingGlyphs(grid, frame) // ◆ ▸ ▤ for Geist Mono
// Find a font that has missing[0], for example with fc-match on Linux:
//   fc-match -f '%{file}' 'GeistMono Nerd Font Mono:charset=25c6'
noto, _ := os.ReadFile("/usr/share/fonts/noto/NotoSansMono-Regular.ttf")
frame.Fallback = append(frame.Fallback, shot.Font{Data: noto})
```

shot parses a fallback face only when a glyph needs it, and looks up each
glyph once per render. A glyph wider than its cell is centred on the
cell. tuios fills the fallback list from fontconfig.

SVG and HTML name `Frame.FontFamily` and let the viewer pick the fonts. With
`Frame.EmbedFont`, they also carry `Frame.Font` as an `@font-face`.

## Images

A grid can carry pictures, such as the ones a program shows with the kitty
graphics protocol or with sixel. Put each one in `Grid.Images`:

```go
g.Images = append(g.Images, shot.Image{
	Pixels: pic,                       // any image.Image
	Col: 2, Row: 1, Cols: 30, Rows: 8, // the box of cells it fills
	Source: image.Rect(0, 0, 240, 160), // the part of pic in the box
})
```

- The image fills its box. `Source` is the part of `Pixels` that goes into the
  box. An empty `Source` is the whole image.
- A `Source` that is larger than `Pixels` draws nothing past the edge of the
  image. Use this to keep an image at its own size in a larger box.
- The part of a box outside the grid is cut off.
- `Below` draws the image under the text, as kitty does for a negative
  z-index. Otherwise the image goes over the text.
- PNG draws images at the frame's cell size. SVG and HTML embed each image as
  a PNG data URI. They encode only the part of the image on the grid.
- ANSI and text output have no images.

shot does not decode a terminal's image data. The caller decodes it and
bounds its size.

## The API

- `Grid`, `Cell` and `NewGrid` are the screen. `FromScreen` and `MakeCell`
  make one from an emulator.
- `Palette`, `XTermPalette` and `XTerm256` turn basic and indexed colours into
  RGB.
- `Render` writes one `Format`. `RenderPNG`, `RenderSVG`, `RenderHTML`,
  `RenderANSI` and `RenderText` write one format each. `Raster` returns the
  PNG pixels before they are encoded, and `Downscale` shrinks them.
- `Frame` is the dressing: the window bar, the gradient, the padding, the
  shadow and the fonts. `BuildFrame` makes one from a `FrameSpec` in config
  spellings, such as `"window"` and `"#1e1e2e..#313244"`.
- `Font`, `MissingGlyphs` and `Frame.Fallback` choose the faces.
- `Image` and `Grid.Images` put pictures on the grid.

## Stability

The module is at v0, so the API can change between minor versions. Its tags
have the folder name as a prefix, such as `shot/v0.1.0`.

- Settled: `Grid`, `Cell`, `Palette`, `Render` and the five formats, and
  `FromScreen`. These change only for a strong reason.
- Can still change: the fields of `Frame` and `FrameSpec`, and the `mosaic`
  package.

The pictures can change between versions. Do not compare PNG bytes in a test.
Compare the grid, or the text and ANSI output.

Report a problem in the [tuios issue tracker](https://github.com/Gaurav-Gosain/tuios/issues).

## Licence

MIT, as tuios. The Go Mono and Go Mono Bold fonts are embedded in the module.
They are by Bigelow & Holmes for the Go project, under the licence in
[fonts/README](fonts/README).
