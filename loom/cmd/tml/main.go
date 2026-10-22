// Command tml shows a TML document in the terminal, drawn by loom.
//
//	go install tuios.dev/loom/cmd/tml@latest
//	tml dashboard.tml
//
// In a terminal it opens the document full screen: keys and the mouse work
// in it, and q or esc quits when the document does not use them. When the
// output is not a terminal, or with --print, it draws the document once and
// writes it as text.
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"runtime/debug"
	"strings"

	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/term"
	"tuios.dev/loom"
	"tuios.dev/tml"
)

const usage = `Usage: tml [flags] FILE

Show a TML document in the terminal. FILE is a TML text or TML JSON file, or
- for standard input.

In a terminal the document opens full screen. Keys and the mouse work in it.
Press q or esc to quit, when the document does not use the key. Press ctrl+c
to quit at any time. A close action in the document quits too, and tml prints
the closing value as JSON.

When the output is not a terminal, or with --print, tml draws the document
once and writes it as text.

Flags:
`

const examples = `
Examples:
  tml dashboard.tml
  tml --check report.tml
  tml --print --width 100 --height 30 report.tml > report.txt
  answer=$(tml confirm.tml)
`

type options struct {
	print, check, plain bool
	width, height       int
	depth               string
}

// version is set by the release build. go install leaves it empty, and the
// module version comes from the build info.
var version string

func versionString() string {
	if version != "" {
		return version
	}
	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" {
		return info.Main.Version
	}
	return "dev"
}

func main() {
	code, err := run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr)
	if err != nil && !errors.Is(err, flag.ErrHelp) {
		fmt.Fprintln(os.Stderr, "tml:", err)
		if code == 0 {
			code = 1
		}
	}
	os.Exit(code)
}

func run(args []string, stdin io.Reader, stdout *os.File, stderr io.Writer) (int, error) {
	var o options
	fs := flag.NewFlagSet("tml", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {
		fmt.Fprint(stderr, usage)
		fs.PrintDefaults()
		fmt.Fprint(stderr, examples)
	}
	fs.BoolVar(&o.print, "print", false, "Draw the document once and write it as text")
	fs.BoolVar(&o.check, "check", false, "Check the document, print each problem, and exit 1 on an error")
	fs.BoolVar(&o.plain, "plain", false, "With --print, write no colours")
	fs.IntVar(&o.width, "width", 0, "Width in cells, 0 for the terminal width (80 when it is not a terminal)")
	fs.IntVar(&o.height, "height", 0, "Height in cells, 0 for the terminal height (24 when it is not a terminal)")
	fs.StringVar(&o.depth, "depth", "auto", "Colour depth: auto, truecolor, 256, 16 or ascii")
	showVersion := fs.Bool("version", false, "Print the version and exit")
	// Flags may come before or after FILE.
	var files []string
	for {
		if err := fs.Parse(args); err != nil {
			return 2, err
		}
		args = fs.Args()
		if len(args) == 0 {
			break
		}
		files = append(files, args[0])
		args = args[1:]
	}
	if *showVersion {
		fmt.Fprintln(stdout, "tml", versionString())
		return 0, nil
	}
	if len(files) != 1 {
		fs.Usage()
		return 2, errors.New("give one file, or - for standard input")
	}
	name := files[0]
	src, err := readSource(name, stdin)
	if err != nil {
		return 1, err
	}
	lim := tml.PersonLimits()
	doc, problems, err := tml.Check(src, &tml.ValidateOptions{Limits: &lim})
	for _, p := range problems {
		fmt.Fprintf(stderr, "%s:%s\n", safe(name), safe(p.String()))
	}
	if err != nil {
		return 1, fmt.Errorf("%s is not a valid TML document", safe(name))
	}
	if o.check {
		return 0, nil
	}

	tty := term.IsTerminal(stdout.Fd())
	depth, err := pickDepth(o.depth, stdout, tty && !o.print)
	if err != nil {
		return 2, err
	}
	if o.plain {
		depth = loom.ASCII
	}
	if !tty || o.print {
		return 0, printOnce(doc, depth, o, stdout, tty)
	}
	if name == "-" {
		// Keys come from the terminal, and standard input held the document.
		return 2, errors.New("a document from standard input can only be printed: add --print, or give a file")
	}
	value, closed, err := view(doc, depth)
	if err != nil {
		return 1, err
	}
	if closed {
		out, err := json.Marshal(value)
		if err != nil {
			return 1, err
		}
		fmt.Fprintln(stdout, string(out))
	}
	return 0, nil
}

// readSource reads a document from a file, or from standard input for -, up
// to the document limit and one byte more, so a long document is refused
// with the limit named.
func readSource(name string, stdin io.Reader) ([]byte, error) {
	limit := tml.PersonLimits().DocBytes
	r := stdin
	if name != "-" {
		f, err := os.Open(name)
		if err != nil {
			return nil, err
		}
		defer func() { _ = f.Close() }()
		r = f
	}
	data, err := io.ReadAll(io.LimitReader(r, int64(limit)+1))
	if err != nil {
		return nil, err
	}
	if len(data) > limit {
		return nil, fmt.Errorf("the document is larger than %d bytes", limit)
	}
	return data, nil
}

// pickDepth is the colour depth: the flag, or the one the terminal and its
// environment give. A file or a pipe takes 256 colours, unless NO_COLOR is
// set.
func pickDepth(flagValue string, out *os.File, tty bool) (loom.Depth, error) {
	switch strings.ToLower(flagValue) {
	case "truecolor", "24bit":
		return loom.TrueColor, nil
	case "256":
		return loom.Color256, nil
	case "16":
		return loom.Color16, nil
	case "ascii", "none":
		return loom.ASCII, nil
	case "auto", "":
	default:
		return 0, fmt.Errorf("unknown depth %q: use auto, truecolor, 256, 16 or ascii", flagValue)
	}
	p := colorprofile.Detect(out, os.Environ())
	if !term.IsTerminal(out.Fd()) {
		p = colorprofile.ANSI256
		if os.Getenv("NO_COLOR") != "" {
			p = colorprofile.NoTTY
		}
	}
	switch p {
	case colorprofile.TrueColor:
		return loom.TrueColor, nil
	case colorprofile.ANSI256:
		return loom.Color256, nil
	case colorprofile.ANSI:
		return loom.Color16, nil
	}
	return loom.ASCII, nil
}

// printOnce draws the document at one size and writes it.
func printOnce(doc *tml.Node, depth loom.Depth, o options, out *os.File, tty bool) error {
	w, h := 80, 24
	if tty {
		if tw, th, err := term.GetSize(out.Fd()); err == nil && tw > 0 && th > 0 {
			w, h = tw, th-1
		}
	}
	if o.width > 0 {
		w = o.width
	}
	if o.height > 0 {
		h = o.height
	}
	// The text sits on the terminal's own background, as other output does.
	s, err := loom.New(doc, &loom.Options{Palette: loom.DefaultPalette(depth), Ground: loom.GroundNone})
	if err != nil {
		return err
	}
	buf := s.Render(w, h)
	// Row by row, so the blank rows under the document can be left out.
	var lines []string
	last := -1
	for y := 0; y < buf.Height(); y++ {
		blank := true
		for x := 0; x < buf.Width(); x++ {
			if c := buf.Cell(x, y); (c.Content != "" && c.Content != " ") || c.Bg != nil || c.Reverse {
				blank = false
				break
			}
		}
		if !blank {
			last = y
		}
	}
	text := buf.Render()
	if depth == loom.ASCII {
		text = buf.String()
	}
	rows := strings.Split(strings.TrimSuffix(text, "\n"), "\n")
	for y := 0; y <= last && y < len(rows); y++ {
		lines = append(lines, strings.TrimRight(rows[y], " "))
	}
	if len(lines) == 0 {
		return nil
	}
	_, err = io.WriteString(out, strings.Join(lines, "\n")+"\n")
	return err
}

// safe drops the characters a terminal would act on from text that came
// from the document, such as a file name or a problem that quotes it.
func safe(s string) string {
	return strings.Map(func(r rune) rune {
		if (r < 0x20 && r != '\t' && r != '\n') || r == 0x7f || (r >= 0x80 && r <= 0x9f) {
			return '�'
		}
		return r
	}, s)
}
