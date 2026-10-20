//go:build js && wasm

// Command sketch-wasm runs tuios.dev/sketch in a browser, for the Mermaid
// playground at tuios.dev/sketch. It imports sketch and nothing of tuios, so
// the wasm holds the renderer and the Go runtime only.
//
// The program sets one global object, `sketch`, and then blocks forever. The
// page runs it in a Web Worker, so a slow render never holds up typing.
// The object:
//
//	sketch.render(source, {maxWidth, theme}) -> {svg, width, height, type}
//	                                         or {error: {message, code, line, column}}
//	sketch.themes() -> [{name, title, dark, vars}]
//
// The program calls self.onSketchReady(sketch) once the object is set.
package main

import (
	"errors"
	"syscall/js"

	"tuios.dev/sketch"
)

// Limits for text a visitor types. They are lower than sketch's own, so one
// paste cannot keep the worker busy for long.
const (
	maxSource = 20_000
	maxEdges  = 2_000
)

func main() {
	api := js.Global().Get("Object").New()
	api.Set("render", js.FuncOf(render))
	api.Set("themes", js.FuncOf(themes))
	js.Global().Set("sketch", api)
	if ready := js.Global().Get("onSketchReady"); ready.Type() == js.TypeFunction {
		ready.Invoke(api)
	}
	select {}
}

// render(source, options) draws one diagram. options may hold maxWidth, the
// width in pixels the page has for it, and theme, a theme name. Without a
// theme the SVG reads its colours from the --pele-* properties of the page.
// It returns {svg, width, height, type} or {error: {message, code, line,
// column}}.
func render(_ js.Value, args []js.Value) any {
	if len(args) < 1 || args[0].Type() != js.TypeString {
		return failure(errors.New("render needs the diagram text"))
	}
	src := args[0].String()
	opt := sketch.RenderOptions{Limit: maxSource, MaxEdges: maxEdges}
	if len(args) > 1 && args[1].Type() == js.TypeObject {
		o := args[1]
		if w := o.Get("maxWidth"); w.Type() == js.TypeNumber && w.Float() > 0 {
			v := w.Float()
			opt.MaxWidth = &v
		}
		if t := o.Get("theme"); t.Type() == js.TypeString && t.String() != "" {
			th, ok := sketch.ThemeNamed(t.String())
			if !ok {
				return failure(errors.New("no theme is named " + t.String()))
			}
			opt.Theme = th
		}
	}
	svg, err := sketch.Render(src, opt)
	if err != nil {
		return failure(err)
	}
	return map[string]any{
		"svg":    svg.Data,
		"width":  svg.Width,
		"height": svg.Height,
		"type":   string(svg.Type),
	}
}

func failure(err error) map[string]any {
	e := map[string]any{"message": err.Error(), "code": "error", "line": 0, "column": 0}
	var se *sketch.Error
	if errors.As(err, &se) {
		e["code"] = string(se.Code)
		e["line"] = se.Line
		e["column"] = se.Column
	}
	return map[string]any{"error": e}
}

// themes() lists the named themes: [{name, title, dark, vars}], where vars
// maps each --pele-* property to its value.
func themes(js.Value, []js.Value) any {
	list := sketch.Themes()
	out := make([]any, 0, len(list))
	for _, t := range list {
		vars := map[string]any{}
		for _, kv := range t.Theme.Vars() {
			vars[kv[0]] = kv[1]
		}
		out = append(out, map[string]any{"name": t.Name, "title": t.Title, "dark": t.Dark, "vars": vars})
	}
	return out
}
