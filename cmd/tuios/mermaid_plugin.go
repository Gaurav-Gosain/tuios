package main

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"

	"github.com/Gaurav-Gosain/tuios/internal/errfmt"
	"github.com/Gaurav-Gosain/tuios/internal/herdrplugin"
	"github.com/Gaurav-Gosain/tuios/internal/release"
	sketch "tuios.dev/sketch/core"
)

// tuios draws eight diagram types itself (tuios.dev/sketch/core). The rest
// come from the sketch plugin, whose program is tuios.dev/sketch with every
// type. tuios mermaid runs it for a diagram of another type, with the flags
// both take. Without the plugin it says how to get it.

// errNeedsSketchPlugin is what tuios says about a type it does not draw.
const errNeedsSketchPlugin = "this diagram needs the sketch plugin. Run tuios plugins install sketch"

// notBuiltIn names the first diagram type in sources that tuios knows and
// does not draw, "" when it draws all of them.
func notBuiltIn(sources []string) string {
	for _, src := range sources {
		if typ, ok := sketch.Detect(src); ok && !sketch.Supports(src) {
			return string(typ)
		}
	}
	return ""
}

// sketchPluginProgram is the program of the sketch plugin, or why there is
// none.
func sketchPluginProgram() (string, error) {
	entries, _, err := discoverPlugins()
	if err != nil {
		return "", err
	}
	e := herdrplugin.Find(entries, herdrplugin.SketchID)
	switch {
	case e == nil || e.Plugin == nil:
		return "", &callError{status: 3, err: errors.New(errNeedsSketchPlugin)}
	case !e.Runnable():
		return "", &callError{status: 3, err: errfmt.Errorf("the sketch plugin is installed and does not run: %s. Run tuios plugins list to see why", entryState(*e))}
	}
	prog := filepath.Join(e.Plugin.PluginRoot, "bin", release.ExecutableName("sketch"))
	if _, err := os.Stat(prog); err != nil {
		return "", &callError{status: 3, err: errfmt.Errorf("the sketch plugin has no program at %s. Run tuios plugins update %s", prog, herdrplugin.SketchID)}
	}
	return prog, nil
}

// delegateSketch draws the diagrams of name, whose text is data, with the
// sketch plugin.
func delegateSketch(name string, data []byte, typ string, o mermaidOptions, stdout *os.File) error {
	prog, err := sketchPluginProgram()
	if err != nil {
		return err
	}
	switch {
	case o.scale != 2, o.rows != 0, o.fit, o.background != "":
		return errfmt.Errorf("the sketch plugin draws the %s diagram, and it does not take --scale, --rows, --fit or --background. Run the command again without them", typ)
	}
	var args []string
	for _, f := range [][2]string{{"-f", o.format}, {"-o", o.out}, {"-theme", o.theme}} {
		if f[1] != "" {
			args = append(args, f[0], f[1])
		}
	}
	if o.cols > 0 {
		args = append(args, "-cols", strconv.Itoa(o.cols))
	}
	if o.diagram > 0 {
		args = append(args, "-diagram", strconv.Itoa(o.diagram))
	}
	args = append(args, "-")
	cmd := exec.Command(prog, args...) //nolint:gosec // the program of the plugin the person installed
	cmd.Stdin, cmd.Stdout, cmd.Stderr = bytes.NewReader(data), stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		return errfmt.Errorf("the sketch plugin could not draw %s: %w", name, err)
	}
	return nil
}
