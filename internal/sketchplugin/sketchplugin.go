// Package sketchplugin runs the program of the sketch plugin for one Mermaid
// diagram of a type this build does not draw. tuios asks it for text cells,
// which loom draws in a TML surface, the agents thread and quick look, and
// for SVG, which a picture of the agents thread rasterises in tuios's own
// colours. It links no rasteriser, so the browser build can use it.
package sketchplugin

import (
	"bytes"
	"context"
	"errors"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/errfmt"
	"github.com/Gaurav-Gosain/tuios/internal/jsonx"
	sketchtext "tuios.dev/sketch/text"
)

// Timeout bounds one run of the program.
const Timeout = 20 * time.Second

// MaxOutput bounds what the program may answer, in bytes.
const MaxOutput = 16 << 20

// ErrTooLarge is an answer past MaxOutput.
var ErrTooLarge = errors.New("the drawing is too large")

func run(prog, src string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), Timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, prog, args...) //nolint:gosec // the program of the plugin the person installed
	cmd.Stdin = strings.NewReader(src)
	var out capped
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		if out.over {
			return nil, ErrTooLarge
		}
		return nil, err
	}
	if out.over {
		return nil, ErrTooLarge
	}
	return out.b.Bytes(), nil
}

type capped struct {
	b    bytes.Buffer
	over bool
}

func (c *capped) Write(p []byte) (int, error) {
	if c.over || c.b.Len()+len(p) > MaxOutput {
		c.over = true
		return 0, ErrTooLarge
	}
	return c.b.Write(p)
}

// Cells draws src as text cells at most cols wide. cut says the drawing is
// wider than cols.
func Cells(prog, src string, cols int) ([][]sketchtext.Cell, bool, error) {
	data, err := run(prog, src, "-f", "cells", "-cols", strconv.Itoa(cols), "-")
	if err != nil {
		return nil, false, err
	}
	var got struct {
		Rows [][][3]int `json:"rows"`
		Cut  bool       `json:"cut"`
	}
	if err := jsonx.Unmarshal(data, &got); err != nil {
		return nil, false, errfmt.Errorf("the sketch plugin gave no cells: %w", err)
	}
	rows := make([][]sketchtext.Cell, len(got.Rows))
	for y, row := range got.Rows {
		rows[y] = make([]sketchtext.Cell, len(row))
		for x, c := range row {
			r := rune(c[0])
			if r != 0 && (r < ' ' || r == 0x7f || (r >= 0x80 && r < 0xa0)) {
				r = ' '
			}
			rows[y][x] = sketchtext.Cell{Rune: r, Role: sketchtext.Role(c[1]), Bg: sketchtext.Role(c[2])} //nolint:gosec // a role is a small number
		}
	}
	return rows, got.Cut, nil
}

var reViewBox = regexp.MustCompile(`^\s*<svg\b[^>]*?\sviewBox="0 0 ([0-9.eE+-]+) ([0-9.eE+-]+)"`)

// SVG draws src as SVG in the plugin's own theme, and gives its size in SVG
// units. The colours come from --pele-* tokens on the root element, so a
// rasteriser given a theme draws it in that theme.
func SVG(prog, src string) (data string, width, height float64, err error) {
	out, err := run(prog, src, "-f", "svg", "-")
	if err != nil {
		return "", 0, 0, err
	}
	data = strings.TrimSpace(string(out))
	m := reViewBox.FindStringSubmatch(data)
	if m == nil {
		return "", 0, 0, errors.New("the sketch plugin gave no SVG")
	}
	width, e1 := strconv.ParseFloat(m[1], 64)
	height, e2 := strconv.ParseFloat(m[2], 64)
	if e1 != nil || e2 != nil || width <= 0 || height <= 0 {
		return "", 0, 0, errors.New("the sketch plugin gave an SVG with no size")
	}
	return data, width, height, nil
}
