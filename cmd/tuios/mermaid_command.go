package main

import (
	"bytes"
	"errors"
	"fmt"
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
	format  string
	out     string
	scale   float64
	cols    int
	rows    int
	diagram int
	theme   string
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

In a terminal that shows kitty graphics, the diagram is a picture. In other
terminals it is drawn with box-drawing characters and its labels as text. A
diagram type with no text drawing yet, or one too wide for the terminal, is
written as its source. When the output is not a terminal, the command writes
SVG.

With --watch the diagram fills the terminal and is drawn again each time the
file is saved. In a terminal with kitty graphics it moves to the new layout:
boxes slide and grow, new parts fade in, removed parts fade out. --split shows
the source beside it. --demo types a diagram a character at a time, for a
recording: the diagram of FILE, or a built-in one. Press q to quit and r to
start the demo again.

The colours come from your tuios theme, or from --theme or [mermaid] theme. tuios.dev/sketch draws the diagrams. It
is a Go port of pele and draws flowcharts, sequence diagrams, state diagrams, class diagrams, ER diagrams, pie charts, mindmaps and gantt charts now.`,
		Example: `  # Draw a diagram here
  tuios mermaid flow.mmd

  # Draw the diagrams of a Markdown file
  tuios mermaid README.md

  # Save a picture
  tuios mermaid flow.mmd -o flow.png

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
	_ = cmd.RegisterFlagCompletionFunc("theme", func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
		return mermaid.ThemeNames(), cobra.ShellCompDirectiveNoFileComp
	})
	_ = cmd.RegisterFlagCompletionFunc("format", func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
		return []string{"svg", "png", "kitty", "text"}, cobra.ShellCompDirectiveNoFileComp
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
		if err := writeMermaid(out, src, i, len(sources), format, o, profile, tty, stdout); err != nil {
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
	scale := o.scale
	if format == "kitty" {
		// As large as the terminal allows, leaving two rows for the prompt, and no
		// larger than twice the SVG size.
		scale = math.Min(2, math.Min(float64(cols*cellW)/math.Max(1, out.Width), float64(max(rows-2, 4)*cellH)/math.Max(1, out.Height)))
	}
	img, err := raster.Draw(out.Data, raster.Options{Scale: scale, Background: true, Readable: true})
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
