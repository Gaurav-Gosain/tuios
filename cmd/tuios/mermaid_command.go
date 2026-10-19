package main

import (
	"bytes"
	"errors"
	"fmt"
	"image/png"
	"io"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/colorprofile"
	"github.com/spf13/cobra"
	"golang.org/x/term"
	"tuios.dev/sketch"
	"tuios.dev/sketch/raster"
	sketchtext "tuios.dev/sketch/text"

	"github.com/Gaurav-Gosain/tuios/internal/mermaid"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
)

// mermaidOptions are the flags of tuios mermaid.
type mermaidOptions struct {
	format  string
	out     string
	scale   float64
	cols    int
	rows    int
	diagram int
}

func newMermaidCommand() *cobra.Command {
	var o mermaidOptions
	cmd := &cobra.Command{
		Use:   "mermaid FILE",
		Short: "Draw Mermaid diagrams in the terminal or to a file",
		Long: `Draw the Mermaid diagrams of a file.

FILE is a Mermaid source (.mmd), a Markdown file, or - for standard input.
A Markdown file gives each of its mermaid code blocks.

In a terminal that shows kitty graphics, the diagram is a picture. In other
terminals it is drawn with box-drawing characters and its labels as text. A
diagram type with no text drawing yet, or one too wide for the terminal, is
written as its source. When the output is not a terminal, the command writes
SVG.

The colours come from your tuios theme. tuios.dev/sketch draws the diagrams. It
is a Go port of pele and draws flowcharts and sequence diagrams now.`,
		Example: `  # Draw a diagram here
  tuios mermaid flow.mmd

  # Draw the diagrams of a Markdown file
  tuios mermaid README.md

  # Save a picture
  tuios mermaid flow.mmd -o flow.png

  # Write SVG from standard input
  echo 'flowchart LR; A-->B' | tuios mermaid - --format svg`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runMermaid(args[0], o, os.Stdout)
		},
	}
	cmd.Flags().StringVarP(&o.format, "format", "f", "", "Output format: svg, png, kitty or text")
	cmd.Flags().StringVarP(&o.out, "out", "o", "", "Write to this file instead of the terminal")
	cmd.Flags().Float64Var(&o.scale, "scale", 2, "Pixels per SVG unit for png")
	cmd.Flags().IntVar(&o.cols, "cols", 0, "Width in cells, 0 for the terminal width")
	cmd.Flags().IntVar(&o.rows, "rows", 0, "Height in cells, 0 for the terminal height")
	cmd.Flags().IntVar(&o.diagram, "diagram", 0, "Draw only diagram N of the file, from 1")
	_ = cmd.RegisterFlagCompletionFunc("format", func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
		return []string{"svg", "png", "kitty", "text"}, cobra.ShellCompDirectiveNoFileComp
	})
	return cmd
}

func runMermaid(name string, o mermaidOptions, stdout *os.File) error {
	var data []byte
	var err error
	if name == "-" {
		data, err = io.ReadAll(io.LimitReader(os.Stdin, 1<<22))
	} else {
		data, err = os.ReadFile(name)
	}
	if err != nil {
		return fmt.Errorf("cannot read %s: %w", name, err)
	}
	sources := mermaid.Sources(name, data)
	if len(sources) == 0 {
		return fmt.Errorf("%s has no Mermaid diagram", name)
	}
	if o.diagram > 0 {
		if o.diagram > len(sources) {
			return fmt.Errorf("%s has %d diagrams, not %d", name, len(sources), o.diagram)
		}
		sources = sources[o.diagram-1 : o.diagram]
	}

	tty := term.IsTerminal(int(stdout.Fd()))
	profile := colorprofile.Detect(stdout, os.Environ())
	if !tty {
		// A file or a pipe takes any colour.
		profile = colorprofile.TrueColor
	}
	theme.SetColorProfile(profile)
	th := mermaid.Theme(theme.UI(), theme.GetANSIPalette(), theme.TerminalBg(), theme.TerminalFg())

	format := strings.ToLower(o.format)
	if format == "" && o.out != "" {
		switch strings.ToLower(filepath.Ext(o.out)) {
		case ".svg":
			format = "svg"
		case ".png":
			format = "png"
		default:
			return fmt.Errorf("give --format svg or png for %s", o.out)
		}
	}
	if format == "" {
		switch {
		case !tty:
			format = "svg"
		case kittyGraphics(stdout):
			format = "kitty"
		default:
			format = "text"
		}
	}
	switch format {
	case "svg", "png", "kitty", "text":
	default:
		return fmt.Errorf("unknown format %q: use svg, png, kitty or text", o.format)
	}
	if o.out != "" && format != "svg" && format != "png" {
		return fmt.Errorf("--out takes svg or png, not %s", format)
	}

	failed := 0
	for i, src := range sources {
		out, err := sketch.Render(src, sketch.RenderOptions{Theme: th})
		if err != nil {
			failed++
			fmt.Fprintf(os.Stderr, "diagram %d: %v\n", i+1, err)
			if format == "text" || format == "kitty" {
				fmt.Fprintln(stdout, strings.TrimRight(src, "\n"))
			}
			continue
		}
		if err := writeMermaid(out, src, i, len(sources), format, o, profile, tty, stdout); err != nil {
			return err
		}
	}
	if failed > 0 {
		return fmt.Errorf("%d of %d diagrams could not be drawn", failed, len(sources))
	}
	return nil
}

// numbered is the output file for diagram i of n: name, or name-2.ext and so on.
func numbered(name string, i, n int) string {
	if n == 1 {
		return name
	}
	ext := filepath.Ext(name)
	return strings.TrimSuffix(name, ext) + "-" + strconv.Itoa(i+1) + ext
}

func writeMermaid(out sketch.SVG, src string, i, n int, format string, o mermaidOptions, profile colorprofile.Profile, tty bool, stdout *os.File) error {
	write := func(data []byte) error {
		if o.out == "" {
			_, err := stdout.Write(data)
			return err
		}
		return os.WriteFile(numbered(o.out, i, n), data, 0o644)
	}
	switch format {
	case "svg":
		return write([]byte(out.Data + "\n"))
	case "text":
		if i > 0 {
			fmt.Fprintln(stdout)
		}
		cols, _, _, _ := terminalCells(stdout)
		if o.cols > 0 {
			cols = o.cols
		}
		if d, err := sketch.Parse(src, sketch.ParseOptions{}); err == nil {
			lines, err := sketchtext.Draw(d, sketchtext.Options{Cols: cols, Color: tty && profile >= colorprofile.ANSI})
			if err == nil {
				for _, line := range lines {
					if _, err := fmt.Fprintln(stdout, line); err != nil {
						return err
					}
				}
				return nil
			}
		}
		// No text drawing for this type, or none that fits: the source.
		_, err := fmt.Fprintln(stdout, strings.TrimRight(src, "\n"))
		return err
	}

	cols, rows, cellW, cellH := terminalCells(stdout)
	if o.cols > 0 {
		cols = o.cols
	}
	if o.rows > 0 {
		rows = o.rows
	}
	scale := o.scale
	if format == "kitty" {
		// As large as the terminal allows, leaving two rows for the prompt, and no
		// larger than twice the SVG size.
		scale = math.Min(2, math.Min(float64(cols*cellW)/math.Max(1, out.Width), float64(max(rows-2, 4)*cellH)/math.Max(1, out.Height)))
	}
	img, err := raster.Draw(out.Data, raster.Options{Scale: scale, Background: true})
	if err != nil {
		return fmt.Errorf("cannot draw diagram %d: %w", i+1, err)
	}
	switch format {
	case "png":
		var b bytes.Buffer
		if err := png.Encode(&b, img); err != nil {
			return err
		}
		return write(b.Bytes())
	case "kitty":
		var b bytes.Buffer
		if err := png.Encode(&b, img); err != nil {
			return err
		}
		c := int(math.Ceil(float64(img.Bounds().Dx()) / float64(cellW)))
		return mermaid.Kitty(stdout, b.Bytes(), c)
	}
	return nil
}

// terminalCells is the width of the terminal in cells and the size of a cell
// in pixels, with a common guess where the terminal does not say.
func terminalCells(f *os.File) (cols, rows, cellW, cellH int) {
	cols, rows, cellW, cellH = 100, 40, 10, 20
	if w, h, err := term.GetSize(int(f.Fd())); err == nil && w > 0 && h > 0 {
		cols, rows = w, h
	}
	if pw, ph, c, r, ok := windowPixels(f); ok && c > 0 && r > 0 && pw > 0 && ph > 0 {
		cellW, cellH = pw/c, ph/r
	}
	return cols, rows, max(cellW, 1), max(cellH, 1)
}

var errNoAnswer = errors.New("no answer")

// kittyGraphics asks the terminal whether it shows kitty graphics: a query
// image, then a primary device attributes request that every terminal answers.
// The terminal supports kitty graphics when the image query answers first.
func kittyGraphics(out *os.File) bool {
	in := os.Stdin
	if !term.IsTerminal(int(in.Fd())) {
		return false
	}
	state, err := term.MakeRaw(int(in.Fd()))
	if err != nil {
		return false
	}
	defer func() { _ = term.Restore(int(in.Fd()), state) }()
	if _, err := out.WriteString("\x1b_Gi=31,s=1,v=1,a=q,t=d,f=24;AAAA\x1b\\\x1b[c"); err != nil {
		return false
	}
	answer, err := readAnswer(in, 500*time.Millisecond)
	if err != nil {
		return false
	}
	return strings.Contains(answer, "_Gi=31;OK")
}
