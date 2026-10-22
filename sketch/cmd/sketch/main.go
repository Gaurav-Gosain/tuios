// Command sketch draws Mermaid diagrams: as text in a terminal, or as SVG or
// PNG to a file or a pipe. It is the diagram part of `tuios mermaid` without
// the rest of tuios.
//
//	go install tuios.dev/sketch/cmd/sketch@latest
package main

import (
	"bytes"
	"encoding/base64"
	"errors"
	"flag"
	"fmt"
	"image/png"
	"io"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"golang.org/x/term"
	"tuios.dev/sketch"
	"tuios.dev/sketch/raster"
	sketchtext "tuios.dev/sketch/text"
)

const usage = `Usage: sketch [flags] FILE

Draw the Mermaid diagrams of FILE. FILE is a Mermaid source (.mmd), a
Markdown file, or - for standard input. A Markdown file gives each of its
mermaid code blocks.

In a terminal, the diagram is drawn with box-drawing characters. When the
output is not a terminal, sketch writes SVG. Use -o to write a file: the
extension .svg or .png sets the format.

Examples:
  sketch flow.mmd
  sketch README.md
  sketch flow.mmd -o flow.png
  echo 'flowchart LR; A-->B' | sketch - -f svg > flow.svg
  sketch flow.mmd -f kitty --theme nord

Flags:
`

// maxSource is the most bytes sketch reads: far above the 50,000 characters
// sketch draws, and small enough that a stray large file cannot fill memory.
const maxSource = 4 << 20

func main() {
	if err := run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "sketch:", termSafe(err.Error()))
		os.Exit(1)
	}
}

type options struct {
	format, out, theme string
	scale              float64
	cols, diagram      int
	color              string
}

func run(args []string, stdout *os.File, stderr io.Writer) error {
	fs := flag.NewFlagSet("sketch", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var o options
	fs.StringVar(&o.format, "f", "", "Output format: text, svg, png or kitty")
	fs.StringVar(&o.out, "o", "", "Write to this file instead of standard output")
	fs.StringVar(&o.theme, "theme", "", "Colour theme for svg, png and kitty, such as flexoki, nord or rose-pine")
	fs.Float64Var(&o.scale, "scale", 2, "Pixels per SVG unit for png and kitty")
	fs.IntVar(&o.cols, "cols", 0, "Width of the text drawing in cells, 0 for the terminal width")
	fs.IntVar(&o.diagram, "diagram", 0, "Draw only diagram N of the file, from 1")
	fs.StringVar(&o.color, "color", "auto", "Colour in the text drawing: auto, always or never")
	themes := fs.Bool("themes", false, "List the theme names and exit")
	fs.Usage = func() {
		fmt.Fprint(stderr, usage)
		fs.PrintDefaults()
	}
	// Flags may come before or after FILE.
	var rest []string
	for {
		if err := fs.Parse(args); err != nil {
			if errors.Is(err, flag.ErrHelp) {
				return nil
			}
			return err
		}
		args = fs.Args()
		if len(args) == 0 {
			break
		}
		rest = append(rest, args[0])
		args = args[1:]
	}
	if *themes {
		for _, t := range sketch.Themes() {
			fmt.Fprintln(stdout, t.Name)
		}
		return nil
	}
	if len(rest) != 1 {
		fs.Usage()
		return errors.New("give one file, or - for standard input")
	}
	return draw(rest[0], o, stdout, stderr)
}

func draw(name string, o options, stdout *os.File, stderr io.Writer) error {
	data, err := readSource(name)
	if err != nil {
		return fmt.Errorf("cannot read %s: %w", name, err)
	}
	sources := sources(name, data)
	if len(sources) == 0 {
		return fmt.Errorf("%s has no Mermaid diagram", name)
	}
	if o.diagram > 0 {
		if o.diagram > len(sources) {
			return fmt.Errorf("%s has %d diagrams, not %d", name, len(sources), o.diagram)
		}
		sources = sources[o.diagram-1 : o.diagram]
	}

	var th *sketch.Theme
	if o.theme != "" {
		t, ok := sketch.ThemeNamed(o.theme)
		if !ok {
			return fmt.Errorf("no theme named %q. Run sketch -themes to list them", o.theme)
		}
		th = t
	}

	tty := term.IsTerminal(int(stdout.Fd()))
	format := strings.ToLower(o.format)
	if format == "" && o.out != "" {
		switch strings.ToLower(filepath.Ext(o.out)) {
		case ".svg":
			format = "svg"
		case ".png":
			format = "png"
		default:
			return fmt.Errorf("give -f svg or png for %s", o.out)
		}
	}
	if format == "" {
		format = "svg"
		if tty {
			format = "text"
		}
	}
	switch format {
	case "svg", "png", "kitty", "text":
	default:
		return fmt.Errorf("unknown format %q: use text, svg, png or kitty", o.format)
	}
	if o.out != "" && format != "svg" && format != "png" {
		return fmt.Errorf("-o takes svg or png, not %s", format)
	}
	var color bool
	switch o.color {
	case "always":
		color = true
	case "never":
	case "auto":
		color = tty && os.Getenv("NO_COLOR") == "" && os.Getenv("TERM") != "dumb"
	default:
		return fmt.Errorf("unknown -color %q: use auto, always or never", o.color)
	}
	cols := o.cols
	if cols == 0 && tty {
		if w, _, err := term.GetSize(int(stdout.Fd())); err == nil && w > 0 {
			cols = w
		}
	}

	failed := 0
	for i, src := range sources {
		if err := drawOne(src, i, len(sources), format, cols, color, th, o, stdout, stderr); err != nil {
			failed++
			// The message quotes the source, so it is made safe as the source is.
			fmt.Fprintf(stderr, "diagram %d: %s\n", i+1, termSafe(err.Error()))
		}
	}
	if failed > 0 {
		return fmt.Errorf("%d of %d diagrams could not be drawn", failed, len(sources))
	}
	return nil
}

func drawOne(src string, i, n int, format string, cols int, color bool, th *sketch.Theme, o options, stdout io.Writer, stderr io.Writer) error {
	write := func(data []byte) error {
		if o.out == "" {
			_, err := stdout.Write(data)
			return err
		}
		return os.WriteFile(numbered(o.out, i, n), data, 0o644) //nolint:gosec // a picture the person asked for, readable like any file they save
	}
	if format == "text" {
		if i > 0 {
			fmt.Fprintln(stdout)
		}
		d, err := sketch.Parse(src, sketch.ParseOptions{})
		if err != nil {
			return err
		}
		lines, err := sketchtext.Draw(d, sketchtext.Options{Cols: cols, Color: color})
		if errors.Is(err, sketchtext.ErrTooWide) {
			// Drawn at its full width: the terminal wraps or cuts the lines.
			lines, err = sketchtext.Draw(d, sketchtext.Options{Color: color})
			if err == nil {
				fmt.Fprintf(stderr, "diagram %d: the text drawing is wider than %d columns.\n", i+1, cols)
			}
		}
		if err == nil {
			for _, line := range lines {
				if _, err := fmt.Fprintln(stdout, line); err != nil {
					return err
				}
			}
			return nil
		}
		// No text drawing for this type, or none that fits: say so on stderr,
		// so that piped output stays the source alone.
		switch {
		case errors.Is(err, sketchtext.ErrUnsupported):
			fmt.Fprintf(stderr, "diagram %d: %s diagrams have no text drawing. The source follows.\n", i+1, termSafe(string(d.Type)))
		case errors.Is(err, sketchtext.ErrTooLarge):
			fmt.Fprintf(stderr, "diagram %d: the text drawing is too large. The source follows.\n", i+1)
		default:
			return err
		}
		_, err = fmt.Fprintln(stdout, termSafe(strings.TrimRight(src, "\n")))
		return err
	}

	out, err := sketch.Render(src, sketch.RenderOptions{Theme: th})
	if err != nil {
		return err
	}
	if format == "svg" {
		return write([]byte(out.Data + "\n"))
	}
	scale := o.scale
	cellW := 1
	if format == "kitty" {
		var cols, rows, cellH int
		cols, rows, cellW, cellH = terminalCells(stdout)
		// As large as the terminal allows, leaving two rows for the prompt, and
		// no larger than twice the SVG size.
		scale = math.Min(2, math.Min(float64(cols*cellW)/math.Max(1, out.Width), float64(max(rows-2, 4)*cellH)/math.Max(1, out.Height)))
	}
	img, err := raster.Draw(out.Data, raster.Options{Scale: scale, Background: true, Readable: true})
	if err != nil {
		return err
	}
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		return err
	}
	if format == "png" {
		return write(b.Bytes())
	}
	return kitty(stdout, b.Bytes(), int(math.Ceil(float64(img.Bounds().Dx())/float64(cellW))))
}

// terminalCells is the width and height of the terminal in cells and the size
// of a cell in pixels, with a common guess where the terminal does not say.
func terminalCells(w io.Writer) (cols, rows, cellW, cellH int) {
	cols, rows, cellW, cellH = 100, 40, 10, 20
	f, ok := w.(*os.File)
	if !ok {
		return cols, rows, cellW, cellH
	}
	if c, r, err := term.GetSize(int(f.Fd())); err == nil && c > 0 && r > 0 {
		cols, rows = c, r
	}
	if pw, ph, c, r, ok := windowPixels(f); ok && c > 0 && r > 0 && pw > 0 && ph > 0 {
		cellW, cellH = pw/c, ph/r
	}
	return cols, rows, max(cellW, 1), max(cellH, 1)
}

// kitty writes a PNG as a kitty graphics image cols cells wide. The terminal
// keeps the picture's aspect and moves the cursor below it, so the next
// diagram starts under this one.
func kitty(w io.Writer, png []byte, cols int) error {
	enc := base64.StdEncoding.EncodeToString(png)
	var b bytes.Buffer
	const chunk = 4096
	for i := 0; i < len(enc); i += chunk {
		end := min(i+chunk, len(enc))
		more := 0
		if end < len(enc) {
			more = 1
		}
		b.WriteString("\x1b_G")
		if i == 0 {
			b.WriteString("a=T,f=100,q=2,c=" + strconv.Itoa(cols) + ",")
		}
		b.WriteString("m=" + strconv.Itoa(more) + ";" + enc[i:end] + "\x1b\\")
	}
	b.WriteString("\r\n")
	_, err := w.Write(b.Bytes())
	return err
}

// readSource reads a diagram file, or standard input for "-". It reads only
// regular files, after symlinks, and at most maxSource bytes.
func readSource(name string) ([]byte, error) {
	var r io.Reader = os.Stdin
	if name != "-" {
		info, err := os.Stat(name)
		if err != nil {
			return nil, err
		}
		if !info.Mode().IsRegular() {
			return nil, errors.New("not a regular file")
		}
		f, err := os.Open(name)
		if err != nil {
			return nil, err
		}
		defer f.Close()
		r = f
	}
	data, err := io.ReadAll(io.LimitReader(r, maxSource+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxSource {
		return nil, fmt.Errorf("larger than %d bytes", maxSource)
	}
	return data, nil
}

var reFence = regexp.MustCompile("(?ms)^[ \t]*(```+|~~~+)[ \t]*mermaid[^\n]*\n(.*?)^[ \t]*(```+|~~~+)[ \t]*$")

// sources lists the diagrams in a file: each mermaid fence of a Markdown
// file, or the whole text of any other.
func sources(name string, data []byte) []string {
	text := string(data)
	lower := strings.ToLower(name)
	if strings.HasSuffix(lower, ".md") || strings.HasSuffix(lower, ".markdown") || (name == "-" && strings.Contains(text, "```mermaid")) {
		var out []string
		for _, m := range reFence.FindAllStringSubmatch(text, -1) {
			out = append(out, m[2])
		}
		return out
	}
	if strings.TrimSpace(text) == "" {
		return nil
	}
	return []string{text}
}

// numbered is the output file for diagram i of n: name, or name-2.ext and so on.
func numbered(name string, i, n int) string {
	if n == 1 {
		return name
	}
	ext := filepath.Ext(name)
	return strings.TrimSuffix(name, ext) + "-" + strconv.Itoa(i+1) + ext
}

// termSafe is s with every character a terminal would act on written as
// U+FFFD: C0 controls other than tab and newline, DEL, C1 controls, the
// bidirectional overrides, isolates and marks, and the line and paragraph
// separators. A label such as "#27;]2;x#7;" decodes to OSC 2, and a source
// may hold a raw ESC, so every string from a diagram file goes through it.
func termSafe(s string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r == '\n', r == '\t':
			return r
		case r < 0x20, r >= 0x7f && r <= 0x9f, r >= 0x202a && r <= 0x202e, r >= 0x2066 && r <= 0x2069,
			r == 0x200e, r == 0x200f, r == 0x061c, r == 0x2028, r == 0x2029:
			return '�'
		}
		return r
	}, s)
}
