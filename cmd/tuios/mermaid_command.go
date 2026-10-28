package main

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"image/draw"
	"image/png"
	"math"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/charmbracelet/colorprofile"
	"github.com/spf13/cobra"
	"golang.org/x/term"
	"tuios.dev/sketch"
	"tuios.dev/sketch/raster"
	sketchtext "tuios.dev/sketch/text"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/mermaid"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
)

// mermaidOptions are the flags of tuios mermaid.
type mermaidOptions struct {
	format     string
	out        string
	scale      float64
	cols       int
	rows       int
	diagram    int
	theme      string
	background string
	fit        bool
}

// liveOptions are the flags of the live view: --watch, --split and --demo.
type liveOptions struct {
	watch, split, demo, loop bool
	medium                   string
	motion                   time.Duration
	stats                    bool
}

func newMermaidCommand() *cobra.Command {
	var o mermaidOptions
	var lo liveOptions
	cmd := &cobra.Command{
		Use:   "mermaid FILE",
		Short: "Draw Mermaid diagrams in the terminal or to a file",
		Long: `Draw the Mermaid diagrams of a file.

FILE is a Mermaid source (.mmd), a Markdown file, or - for standard input.
A Markdown file gives each of its mermaid code blocks.

In a terminal that shows kitty graphics, the diagram is a picture. Its text
has the size of the terminal's text. A diagram wider than the terminal is made
smaller to fit the width. A diagram taller than the terminal keeps its size
and goes up into the scrollback: scroll up to see its top. With --fit it is
also made smaller to fit the height. A blank line goes between two diagrams.
In other terminals the diagram is drawn with box-drawing characters and its
labels as text. A diagram type with no text drawing yet is written as
its source. A drawing wider than the terminal is written at its full width.
When the output is not a terminal, the command writes SVG.

With --watch the diagram fills the terminal and is drawn again each time the
file is saved. In a terminal with kitty graphics it moves to the new layout:
boxes slide and grow, new parts fade in, removed parts fade out. --split shows
the source beside it. --demo types a diagram a character at a time, for a
recording: the diagram of FILE, or a built-in one. Press q to quit and r to
start the demo again.

A picture has the background colour of the theme. With --background none, or
background = "none" in [mermaid], a kitty or png picture has no background, so
the diagram is on the background of your terminal. An SVG has no background.

The colours come from your tuios theme, or from --theme or [mermaid] theme. tuios.dev/sketch draws the diagrams. It
is a Go port of pele and draws flowcharts, sequence diagrams, state diagrams, class diagrams, ER diagrams, pie charts, mindmaps, gantt charts, XY charts, timelines, quadrant charts, user journeys, kanban boards, git graphs, sankey diagrams, block diagrams, architecture diagrams, requirement diagrams, C4 diagrams, packet diagrams, radar charts, treemaps, info diagrams, tree views, Ishikawa diagrams, Cynefin diagrams, event models, Venn diagrams, Wardley maps, railroad diagrams, use case diagrams, agent flows and swimlane diagrams now.`,
		Example: `  # Draw a diagram here
  tuios mermaid flow.mmd

  # Draw the diagrams of a Markdown file
  tuios mermaid README.md

  # Save a picture
  tuios mermaid flow.mmd -o flow.png

  # Draw on the background of the terminal
  tuios mermaid flow.mmd --background none

  # Make a tall diagram fit the terminal
  tuios mermaid flow.mmd --fit

  # Write SVG from standard input
  echo 'flowchart LR; A-->B' | tuios mermaid - --format svg

  # Draw again on every save, with the source beside the diagram
  tuios mermaid flow.mmd --watch --split

  # Type a diagram for a recording, in pele's Flexoki colours
  tuios mermaid --demo --theme flexoki`,
		Args: cobra.RangeArgs(0, 1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := ""
			if len(args) == 1 {
				name = args[0]
			}
			if lo.watch || lo.demo {
				if lo.watch && (name == "" || name == "-") {
					return fmt.Errorf("--watch needs a file")
				}
				th, err := mermaidTheme(o.theme)
				if err != nil {
					return err
				}
				// The live view keeps this process running; --pprof is a
				// persistent flag that it used to accept and ignore.
				startPprofServer()
				if err := startCPUProfile(); err != nil {
					return err
				}
				defer stopCPUProfile()
				return runMermaidLive(name, o, lo, th, os.Stdout)
			}
			if name == "" {
				return fmt.Errorf("give a file, or - for standard input")
			}
			return runMermaid(name, o, os.Stdout)
		},
	}
	cmd.Flags().BoolVarP(&lo.watch, "watch", "w", false, "Draw again each time the file changes, with motion between versions")
	cmd.Flags().BoolVar(&lo.split, "split", false, "With --watch, show the source beside the diagram")
	cmd.Flags().BoolVar(&lo.demo, "demo", false, "Type the diagram of FILE, or a built-in one, a character at a time")
	cmd.Flags().BoolVar(&lo.loop, "loop", false, "With --demo, start again 2 s after the end")
	cmd.Flags().DurationVar(&lo.motion, "motion", 0, "Length of the motion between versions, 0 for 260ms, -1ns for none")
	cmd.Flags().StringVar(&lo.medium, "kitty-medium", "auto", "How frames reach the terminal: auto, file or direct")
	cmd.Flags().BoolVar(&lo.stats, "stats", false, "With --watch or --demo, print frame times when you quit")
	_ = cmd.Flags().MarkHidden("stats")
	cmd.Flags().StringVarP(&o.format, "format", "f", "", "Output format: svg, png, kitty or text")
	cmd.Flags().StringVarP(&o.out, "out", "o", "", "Write to this file instead of the terminal")
	cmd.Flags().Float64Var(&o.scale, "scale", 2, "Pixels per SVG unit for png")
	cmd.Flags().IntVar(&o.cols, "cols", 0, "Width in cells, 0 for the terminal width")
	cmd.Flags().IntVar(&o.rows, "rows", 0, "Height in cells, 0 for the terminal height")
	cmd.Flags().IntVar(&o.diagram, "diagram", 0, "Draw only diagram N of the file, from 1")
	cmd.Flags().StringVar(&o.theme, "theme", "", "Colour theme: tuios, or a name such as flexoki, nord or rose-pine")
	cmd.Flags().BoolVar(&o.fit, "fit", false, "Make each kitty picture fit the terminal height too")
	cmd.Flags().StringVar(&o.background, "background", "", "Background of a kitty or png picture: theme, or none for the terminal's own")
	_ = cmd.RegisterFlagCompletionFunc("theme", func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
		return mermaid.ThemeNames(), cobra.ShellCompDirectiveNoFileComp
	})
	_ = cmd.RegisterFlagCompletionFunc("format", func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
		return []string{"svg", "png", "kitty", "text"}, cobra.ShellCompDirectiveNoFileComp
	})
	_ = cmd.RegisterFlagCompletionFunc("background", func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
		return []string{"theme", "none"}, cobra.ShellCompDirectiveNoFileComp
	})
	return cmd
}

func runMermaid(name string, o mermaidOptions, stdout *os.File) error {
	data, err := mermaid.ReadSource(name)
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
	th, err := mermaidTheme(o.theme)
	if err != nil {
		return err
	}
	if o.background, err = mermaidBackground(o.background); err != nil {
		return err
	}

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
			// The message quotes the source, so it is made safe as the source is.
			fmt.Fprintf(os.Stderr, "diagram %d: %s\n", i+1, mermaid.TermSafe(err.Error()))
			if format == "text" || format == "kitty" {
				fmt.Fprintln(stdout, mermaid.TermSafe(strings.TrimRight(src, "\n")))
			}
			continue
		}
		if err := writeMermaid(out, src, i, len(sources), format, o, th, profile, tty, stdout); err != nil {
			return err
		}
	}
	if failed > 0 {
		return fmt.Errorf("%d of %d diagrams could not be drawn", failed, len(sources))
	}
	return nil
}

// mermaidTheme is the diagram theme: the flag, else [mermaid] theme, else the
// one built from the tuios theme in [appearance] theme.
func mermaidTheme(flag string) (*sketch.Theme, error) {
	name := flag
	if cfg, err := config.LoadUserConfig(); err == nil && cfg != nil {
		if name == "" {
			name = cfg.Mermaid.Theme
		}
		if cfg.Appearance.Theme != "" {
			_ = theme.Initialize(cfg.Appearance.Theme)
		}
	}
	return mermaid.ThemeFor(name, theme.UI(), theme.GetANSIPalette(), theme.TerminalBg(), theme.TerminalFg())
}

// mermaidBackground is the background of a picture: the flag, else
// [mermaid] background, else "theme". "none" leaves it clear, so the diagram
// sits on the terminal's own background.
func mermaidBackground(flag string) (string, error) {
	name := strings.ToLower(strings.TrimSpace(flag))
	if name == "" {
		if cfg, err := config.LoadUserConfig(); err == nil && cfg != nil {
			name = strings.ToLower(strings.TrimSpace(cfg.Mermaid.Background))
		}
	}
	switch name {
	case "", "theme":
		return "theme", nil
	case "none":
		return "none", nil
	}
	return "", fmt.Errorf("unknown background %q: use theme or none", name)
}

// diagramKinds are the names the help text and the docs give each diagram type.
var diagramKinds = map[sketch.Type]string{
	"xychart":       "XY charts",
	"quadrantChart": "quadrant charts",
	"state":         "state diagrams",
	"class":         "class diagrams",
	"er":            "ER diagrams",
	"pie":           "pie charts",
	"mindmap":       "mindmaps",
	"gantt":         "gantt charts",
	"flowchart":     "flowcharts",
	"sequence":      "sequence diagrams",
	"timeline":      "timelines",
	"journey":       "user journeys",
	"kanban":        "kanban boards",
	"gitGraph":      "git graphs",
	"sankey":        "sankey diagrams",
	"block":         "block diagrams",
	"architecture":  "architecture diagrams",
	"requirement":   "requirement diagrams",
	"c4":            "C4 diagrams",
	"packet":        "packet diagrams",
	"radar":         "radar charts",
	"treemap":       "treemaps",
	"info":          "info diagrams",
	"treeView":      "tree views",
	"ishikawa":      "Ishikawa diagrams",
	"cynefin":       "Cynefin diagrams",
	"eventmodeling": "event models",
	"venn":          "Venn diagrams",
	"wardley":       "Wardley maps",
	"railroad":      "railroad diagrams",
	"usecase":       "use case diagrams",
	"agentflow":     "agent flows",
	"swimlane":      "swimlane diagrams",
}

// diagramKind is the plural name of a diagram type for a message, such as
// "XY charts". A type with no name in diagramKinds gives "<id> diagrams".
func diagramKind(t sketch.Type) string {
	if name, ok := diagramKinds[t]; ok {
		return name
	}
	return string(t) + " diagrams"
}

// numbered is the output file for diagram i of n: name, or name-2.ext and so on.
func numbered(name string, i, n int) string {
	if n == 1 {
		return name
	}
	ext := filepath.Ext(name)
	return strings.TrimSuffix(name, ext) + "-" + strconv.Itoa(i+1) + ext
}

func writeMermaid(out sketch.SVG, src string, i, n int, format string, o mermaidOptions, th *sketch.Theme, profile colorprofile.Profile, tty bool, stdout *os.File) error {
	write := func(data []byte) error {
		if o.out == "" {
			_, err := stdout.Write(data)
			return err
		}
		return os.WriteFile(numbered(o.out, i, n), data, 0o644) //nolint:gosec // a picture the person asked for, readable like any file they save
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
			color := tty && profile >= colorprofile.ANSI
			lines, err := sketchtext.Draw(d, sketchtext.Options{Cols: cols, Color: color})
			if errors.Is(err, sketchtext.ErrTooWide) {
				// Drawn at its full width: the terminal wraps or cuts the lines.
				// The note comes once that drawing is known to work.
				lines, err = sketchtext.Draw(d, sketchtext.Options{Color: color})
				if err == nil {
					fmt.Fprintf(os.Stderr, "diagram %d: the text drawing is wider than %d columns.\n", i+1, cols)
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
			// No text drawing for this type, or none that fits: say so on
			// stderr, so that piped output stays the source alone.
			switch {
			case errors.Is(err, sketchtext.ErrUnsupported):
				fmt.Fprintf(os.Stderr, "diagram %d: %s have no text drawing. The source follows.\n", i+1, mermaid.TermSafe(diagramKind(d.Type)))
			case errors.Is(err, sketchtext.ErrTooLarge):
				fmt.Fprintf(os.Stderr, "diagram %d: the text drawing is too large. The source follows.\n", i+1)
			}
		}
		_, err := fmt.Fprintln(stdout, mermaid.TermSafe(strings.TrimRight(src, "\n")))
		return err
	}

	cols, rows, cellW, cellH := terminalCells(stdout)
	if o.cols > 0 {
		cols = o.cols
	}
	if o.rows > 0 {
		rows = o.rows
	}
	background := o.background != "none"
	if format == "png" {
		img, err := raster.Draw(out.Data, raster.Options{Scale: o.scale, Background: background, Readable: true})
		if err != nil {
			return fmt.Errorf("cannot draw diagram %d: %w", i+1, err)
		}
		var b bytes.Buffer
		if err := png.Encode(&b, img); err != nil {
			return err
		}
		return write(b.Bytes())
	}

	// kitty: the diagram's text at the size of the terminal's own, and smaller
	// only when the diagram is wider than the terminal. A taller diagram runs
	// into the scrollback. With --fit it is also made to fit the height, less
	// two rows: one for the gap or the prompt, and one because tuios keeps
	// the last row of a pane clear of pictures.
	maxRows := 0
	if o.fit {
		maxRows = max(rows-2, 4)
	}
	scale := kittyScale(out.Width, out.Height, cols, maxRows, cellW, cellH)
	img, err := raster.Draw(out.Data, raster.Options{Scale: scale, Background: background, Readable: true})
	if err != nil {
		return fmt.Errorf("cannot draw diagram %d: %w", i+1, err)
	}
	box, c, r := cellBox(img, cellW, cellH, cols, maxRows, background, th)
	var b bytes.Buffer
	if err := png.Encode(&b, box); err != nil {
		return err
	}
	if i > 0 {
		// A blank line between two diagrams.
		fmt.Fprintln(stdout)
	}
	return mermaid.Kitty(stdout, b.Bytes(), c, r)
}

// diagramFontSize is the size in SVG units of a diagram's body text. sketch
// draws labels at 16px, as pele does.
const diagramFontSize = 16

// kittyScale is the pixels a diagram unit takes in a picture of the terminal.
// Its body text gets the size of the terminal's own text, about four fifths of
// a cell's height, so every diagram of a file has one text size. It is
// smaller only where the diagram would be wider than cols cells, or taller
// than rows cells when rows is above 0, or larger than a picture can be. A
// small diagram is not made larger than that.
func kittyScale(width, height float64, cols, rows, cellW, cellH int) float64 {
	width, height = math.Max(1, width), math.Max(1, height)
	scale := math.Min(0.8*float64(cellH)/diagramFontSize, float64(cols*cellW)/width)
	if rows > 0 {
		scale = math.Min(scale, float64(rows*cellH)/height)
	}
	// raster.Draw refuses a side past 32768 pixels or 40 million pixels in all.
	return math.Min(scale, math.Min(32000/math.Max(width, height), math.Sqrt(39e6/(width*height))))
}

// cellBox pads a picture to whole cells, so the box of c by r cells the
// terminal draws it into has the picture's own aspect. The padding is the
// diagram's background, or clear when there is none. The box is at most cols
// cells wide, and rows cells tall when rows is above 0.
func cellBox(img image.Image, cellW, cellH, cols, rows int, background bool, th *sketch.Theme) (image.Image, int, int) {
	b := img.Bounds()
	c := min(max(int(math.Ceil(float64(b.Dx())/float64(cellW))), 1), max(cols, 1))
	r := max(int(math.Ceil(float64(b.Dy())/float64(cellH))), 1)
	if rows > 0 {
		r = min(r, rows)
	}
	w, h := c*cellW, r*cellH
	if w == b.Dx() && h == b.Dy() {
		return img, c, r
	}
	dst := image.NewNRGBA(image.Rect(0, 0, max(w, b.Dx()), max(h, b.Dy())))
	if background && th != nil && th.Bg != nil {
		draw.Draw(dst, dst.Bounds(), &image.Uniform{th.Bg}, image.Point{}, draw.Src)
	}
	draw.Draw(dst, b.Sub(b.Min), img, b.Min, draw.Over)
	return dst, c, r
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

// guardRaw restores a terminal put in raw mode for a query if a signal comes
// before the query ends, and exits. The returned function restores it and
// ends the guard.
func guardRaw(fd int, state *term.State) func() {
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, os.Interrupt, syscall.SIGTERM, syscall.SIGHUP, syscall.SIGQUIT)
	done := make(chan struct{})
	go func() {
		select {
		case <-sigs:
			_ = term.Restore(fd, state)
			os.Exit(130)
		case <-done:
		}
	}()
	return func() {
		signal.Stop(sigs)
		close(done)
		_ = term.Restore(fd, state)
	}
}

// kittyGraphics asks the terminal whether it shows kitty graphics: a query
// image, then a primary device attributes request that every terminal answers.
// The terminal supports kitty graphics when the image query answers first.
func kittyGraphics(out *os.File) bool {
	// The answer is read from the terminal, which readAnswer cannot do on
	// Windows. A query there would leave the terminal's DA1 reply in the
	// shell's input after tuios exits, so none is sent.
	if runtime.GOOS == "windows" {
		return false
	}
	in := os.Stdin
	if !term.IsTerminal(int(in.Fd())) {
		return false
	}
	state, err := term.MakeRaw(int(in.Fd()))
	if err != nil {
		return false
	}
	defer guardRaw(int(in.Fd()), state)()
	if _, err := out.WriteString("\x1b_Gi=31,s=1,v=1,a=q,t=d,f=24;AAAA\x1b\\\x1b[c"); err != nil {
		return false
	}
	answer, err := readAnswer(in, 500*time.Millisecond)
	if err != nil {
		return false
	}
	return strings.Contains(answer, "_Gi=31;OK")
}
