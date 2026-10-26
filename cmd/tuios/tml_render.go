package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/charmbracelet/x/ansi"
	"github.com/spf13/cobra"
	"golang.org/x/term"
	"tuios.dev/tml"

	"github.com/Gaurav-Gosain/tuios/internal/theme"
	"github.com/Gaurav-Gosain/tuios/internal/tmldraw"
)

// tmlRenderOptions is the `tuios tml render` command line.
type tmlRenderOptions struct {
	width  int
	height int
	state  string
	tab    string
	styled bool
	ascii  bool
	popup  bool
}

// tmlRenderDefaultWidth is the width tml render draws at when standard
// output is not a terminal.
const tmlRenderDefaultWidth = 100

// tmlRenderTallest bounds the height tml render draws a document at when no
// height is given: the document then gets the rows it takes, up to this.
const tmlRenderTallest = 400

func newTMLRenderCommand() *cobra.Command {
	var o tmlRenderOptions
	cmd := &cobra.Command{
		Use:   "render FILE",
		Short: "Print a TML document drawn as text",
		Long: `Print a TML document drawn as text, as tuios draws it in a pane. It opens
nothing and needs no daemon.

FILE is a TML text or TML JSON file, or - for standard input. tuios checks the
document first. When the document has errors, the command prints each problem
with the fix and exits with status 1.

The width is the terminal's width, or 100 when standard output is not a
terminal. Without --height, the document gets the rows it takes. The text has
the margin a pane keeps around a surface.

Read the output before you show a document, and after each change. Look for
cut text, empty charts and boxes that do not line up.`,
		Example: `  # Check how a dashboard looks before you show it
  tuios tml render dash.tml

  # Draw it at the size of a small pane, on its second tab
  tuios tml render dash.tml --width 60 --height 20 --tab log

  # Draw it with other values in its state
  tuios tml render dash.tml --state '{"runs":12}'`,
		Args: cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			return runTMLRender(args[0], o, os.Stdin, os.Stdout, os.Stderr)
		},
	}
	cmd.Flags().IntVar(&o.width, "width", 0, "Width in cells (default: the terminal's width, else 100)")
	cmd.Flags().IntVar(&o.height, "height", 0, "Height in cells (default: the rows the document takes)")
	cmd.Flags().StringVar(&o.state, "state", "", "A JSON object to use as the document's state")
	cmd.Flags().StringVar(&o.tab, "tab", "", "Open the tab with this id")
	cmd.Flags().BoolVarP(&o.styled, "ansi", "e", false, "Keep the colours and styles as ANSI escape codes, as capture-pane --ansi does")
	cmd.Flags().BoolVar(&o.ascii, "ascii", false, "Draw with ASCII characters only")
	cmd.Flags().BoolVar(&o.popup, "popup", false, "Draw it as a popup: view.placement reads popup")
	return cmd
}

func runTMLRender(name string, o tmlRenderOptions, stdin io.Reader, stdout, stderr io.Writer) error {
	src, err := readTMLSource(name, stdin)
	if err != nil {
		return err
	}
	if err := checkTMLSource(name, src, stderr); err != nil {
		return &statusError{code: 1}
	}
	lim := tml.PersonLimits()
	doc, err := tml.Parse(src, &tml.ParseOptions{Limits: &lim})
	if err != nil {
		return fmt.Errorf("%s: %w", name, err)
	}
	draw := tmldraw.Options{
		Width:     o.width,
		Height:    o.height,
		Inset:     true,
		Placement: "pane",
		Tab:       o.tab,
		Styled:    o.styled,
		Palette:   tmldraw.Palette(theme.UI(), o.ascii),
	}
	if o.popup {
		draw.Placement = "popup"
	}
	if o.state != "" {
		var probe map[string]any
		if err := json.Unmarshal([]byte(o.state), &probe); err != nil || probe == nil {
			return fmt.Errorf("--state is not a JSON object: %s", strings.TrimSpace(o.state))
		}
		draw.State = json.RawMessage(o.state)
	}
	if draw.Width <= 0 {
		draw.Width = tmlRenderDefaultWidth
		if f, ok := stdout.(*os.File); ok && term.IsTerminal(int(f.Fd())) {
			if w, _, err := term.GetSize(int(f.Fd())); err == nil && w > 0 {
				draw.Width = w
			}
		}
	}
	natural := draw.Height <= 0
	if natural {
		draw.Height = tmlRenderTallest
	}
	out, err := tmldraw.Draw(doc, draw)
	if err != nil {
		return fmt.Errorf("%s: %w", name, err)
	}
	if natural {
		out = trimBlankTail(out)
	}
	fmt.Fprintln(stdout, out)
	return nil
}

// trimBlankTail drops the blank lines at the end of out but one, which is
// the margin under the document.
func trimBlankTail(out string) string {
	lines := strings.Split(out, "\n")
	end := len(lines)
	for end > 0 && strings.TrimSpace(ansi.Strip(lines[end-1])) == "" {
		end--
	}
	if end < len(lines) {
		end++
	}
	return strings.Join(lines[:end], "\n")
}
