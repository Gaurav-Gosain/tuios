package main

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"strconv"

	"github.com/Gaurav-Gosain/tuios/internal/errfmt"
	"github.com/Gaurav-Gosain/tuios/internal/herdrplugin"
	"github.com/Gaurav-Gosain/tuios/internal/procsetup"
	sketch "tuios.dev/sketch/core"
)

const errNeedsSketchPlugin = "this diagram needs the sketch plugin. Run tuios plugins install sketch"

func notBuiltIn(sources []string) string {
	for _, src := range sources {
		if typ, ok := sketch.Detect(src); ok && !sketch.Supports(src) {
			return string(typ)
		}
	}
	return ""
}

func sketchPluginProgram() (string, error) {
	prog, e, err := procsetup.FindSketch()
	switch {
	case e == nil && err != nil:
		return "", err
	case e == nil:
		return "", &callError{status: 3, err: errors.New(errNeedsSketchPlugin)}
	case !e.Runnable():
		return "", &callError{status: 3, err: errfmt.Errorf("the sketch plugin is installed and does not run: %s. Run tuios plugins list to see why", entryState(*e))}
	case err != nil:
		return "", &callError{status: 3, err: errfmt.Errorf("the sketch plugin has no program at %s. Run tuios plugins update %s", prog, herdrplugin.SketchID)}
	}
	return prog, nil
}

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
