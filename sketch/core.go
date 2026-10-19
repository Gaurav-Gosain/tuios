// Ported from pele src/core.ts and src/index.ts at 458322a5b8abb1ae57ddf127c32017ab9fb14a92.
//
// mount() and enableZoom() are browser only and are not ported. pele throws a
// PeleError; the port panics with one inside and returns it as an error here.

// Package sketch draws Mermaid diagrams as SVG. It is a Go port of pele
// (github.com/obsidianmd/pele), which draws them without a browser.
package sketch

import (
	"fmt"
	"strconv"
	"strings"

	"tuios.dev/sketch/internal/js"
	"tuios.dev/sketch/internal/text"
	"tuios.dev/sketch/internal/types"
)

// Error is what Parse and Render return for text they cannot draw.
type Error = types.PeleError

// ErrorInfo is where in the source an Error is, and the diagram type.
type ErrorInfo = types.ErrorInfo

// ErrorCode says what kind of problem an Error is.
type ErrorCode = types.PeleErrorCode

const (
	CodeUnsupported = types.CodeUnsupported
	CodeSyntax      = types.CodeSyntax
	CodeSemantic    = types.CodeSemantic
	CodeLimit       = types.CodeLimit
)

// RenderOptions are pele's render options. A nil pointer is an option left unset.
type RenderOptions = types.RenderOptions

// TextMeasurer gives the width of text. The default is a table of Arial
// advances, which gives the same output as pele.
type TextMeasurer = text.TextMeasurer

// Theme sets the colours, fonts and radius of a diagram through pele's --pele-* tokens.
type Theme = types.Theme

// LinkInfo is a link the diagram draws.
type LinkInfo = types.LinkInfo

var diagrams = map[types.DiagramType]*types.Diagram{}

// register makes diagram types available to Render and Parse.
func register(list ...*types.Diagram) {
	for _, d := range list {
		diagrams[d.Type] = d
	}
}

func registered(typ Type) bool {
	_, ok := diagrams[typ]
	return ok
}

// DefaultLimit is the longest source in characters: Mermaid's default maxTextSize.
const DefaultLimit = 50000

// DefaultOutputLimit is the longest SVG in characters. Mermaid has no limit on its output. A source within the limit can still describe a drawing
// of many megabytes, which would stall the page it is put into.
const DefaultOutputLimit = 4_000_000

// DefaultMaxEdges is the most edges a flowchart can have. Mermaid stops at 500. Pele draws 5,000 edges in tens of milliseconds, and `A & B --> C & D`
// multiplies: without a limit, a few kilobytes of source ask for millions of edges.
const DefaultMaxEdges = 5000

// guarded turns the PeleError a parser or renderer panics with into an error.
// Any other panic is a bug in the port; it is reported as an error too, so that
// a host is never brought down by a diagram.
func guarded(err *error) {
	if e := recover(); e != nil {
		switch t := e.(type) {
		case *types.PeleError:
			*err = t
		default:
			*err = types.NewPeleError(fmt.Sprintf("sketch: internal error: %v", e), types.CodeSemantic, types.ErrorInfo{})
		}
	}
}

type parsed struct {
	typ     Type
	diagram *types.Diagram
	model   any
	config  Config
}

func parseSource(src string, limit, maxEdges int, extra Config) parsed {
	// JavaScript strings cannot hold invalid UTF-8, and a browser refuses an
	// SVG that does. Each invalid byte becomes U+FFFD, as TextDecoder does.
	src = strings.ToValidUTF8(src, "\uFFFD")
	if limit <= 0 {
		limit = DefaultLimit
	}
	if maxEdges <= 0 {
		maxEdges = DefaultMaxEdges
	}
	if js.Len(src) > limit {
		types.Throw("Diagram source is longer than the limit of "+strconv.Itoa(limit)+" characters.", types.CodeLimit, types.ErrorInfo{})
	}
	pre := preprocess(src)
	typ, ok := detect(pre.text)
	if !ok {
		types.Throw("No diagram type detected for the given text.", types.CodeUnsupported, types.ErrorInfo{})
	}
	diagram := diagrams[typ]
	if diagram == nil {
		types.Throw(`Diagram type "`+string(typ)+`" is not supported yet.`, types.CodeUnsupported, types.ErrorInfo{Type: string(typ)})
	}
	config := pre.config
	if extra != nil {
		config = Config{}
		for k, v := range extra {
			config[k] = v
		}
		for k, v := range pre.config {
			config[k] = v
		}
	}
	source := pre.text
	if diagram.KeepComments {
		source = pre.withComments
	}
	model := diagram.Parse(encodeEntities(source)+"\n", config, pre.title, pre.lineOffset, types.ParseLimits{MaxEdges: maxEdges})
	return parsed{typ, diagram, model, config}
}

// Detect gives the type of diagram the text describes.
func Detect(src string) (typ Type, ok bool) {
	defer func() {
		if recover() != nil {
			typ, ok = "", false
		}
	}()
	return detect(preprocess(src).text)
}

// Supports reports whether the text is a diagram of a type sketch draws.
func Supports(src string) bool {
	typ, ok := Detect(src)
	return ok && registered(typ)
}

// ParseOptions are the options read when the text is parsed.
type ParseOptions struct {
	Limit    int
	MaxEdges int
	Config   Config
}

// Diagram is diagram text that parsed. RenderSVG draws it.
type Diagram struct {
	Type Type
	p    parsed
}

// Parse reads diagram text.
func Parse(src string, opt ParseOptions) (d *Diagram, err error) {
	defer guarded(&err)
	p := parseSource(src, opt.Limit, opt.MaxEdges, opt.Config)
	return &Diagram{Type: p.typ, p: p}, nil
}

// SVG is a drawn diagram.
type SVG struct {
	Type   Type
	Data   string
	Width  float64
	Height float64
	Links  []LinkInfo
}

// Render parses and draws diagram text.
func Render(src string, options RenderOptions) (out SVG, err error) {
	defer guarded(&err)
	p := parseSource(src, options.Limit, options.MaxEdges, options.Config)
	return render(p, options), nil
}

// RenderSVG draws a parsed diagram.
func (d *Diagram) RenderSVG(options RenderOptions) (out SVG, err error) {
	defer guarded(&err)
	return render(d.p, options), nil
}

func render(p parsed, options RenderOptions) SVG {
	section := p.diagram.Section
	if section == "" {
		section = string(p.typ)
	}
	fixed := false
	if s, ok := p.config[section].(map[string]any); ok {
		if v, ok := s["useMaxWidth"].(bool); ok && !v {
			fixed = true
		}
	}
	if options.Responsive == nil {
		options.Responsive = types.Ptr(!fixed)
	}
	// The host's icon resolver and the ids in `links` see names as the author meant them, with
	// entity codes turned into their characters, as `data-icon` and `data-id` in the SVG do.
	var icons text.IconResolver
	if resolve := options.Icons; resolve != nil {
		icons = func(name string) string { return resolve(text.DecodeEntities(name)) }
	}
	options.Icons = icons
	m := options.Measurer
	if m == nil {
		m = text.DefaultMeasurer(options.FontFamily, options.FontFamilyMono)
	}
	options.Measurer = text.WithIcons(m, icons)
	rendered := p.diagram.Render(p.model, p.config, options)
	outputLimit := options.OutputLimit
	if outputLimit <= 0 {
		outputLimit = DefaultOutputLimit
	}
	// A UTF-16 length is never more than the UTF-8 one, so only a long SVG is counted.
	if len(rendered.SVG) > outputLimit && js.Len(rendered.SVG) > outputLimit {
		types.Throw("Diagram output is longer than the limit of "+strconv.Itoa(outputLimit)+" characters.", types.CodeLimit, types.ErrorInfo{Type: string(p.typ)})
	}
	if options.Geometry != nil && rendered.Geometry != nil {
		*options.Geometry = *rendered.Geometry
	}
	for i := range rendered.Links {
		rendered.Links[i].ID = text.DecodeEntities(rendered.Links[i].ID)
	}
	return SVG{Type: p.typ, Data: rendered.SVG, Width: rendered.Width, Height: rendered.Height, Links: rendered.Links}
}

// Layout is the geometry a renderer worked out for a diagram, in SVG units:
// boxes, routes and label lines. It is not part of pele. sketch/text draws it with
// box-drawing characters.
type Layout = types.Geometry

// Layout draws the diagram and returns its geometry. Types without geometry yet
// give a Layout with nothing in it.
func (d *Diagram) Layout(options RenderOptions) (l *Layout, err error) {
	defer guarded(&err)
	g := &types.Geometry{}
	options.Geometry = g
	render(d.p, options)
	return g, nil
}
