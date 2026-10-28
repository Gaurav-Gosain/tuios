// Ported from pele src/core.ts and src/index.ts at 8aff183be57021b533f319a29341a529be934b08.
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

// MonospacePart names a part of a diagram that RenderOptions.Monospace sets in the monospace
// font: MonospaceMembers, MonospaceAttributes, MonospaceFiles or MonospaceMessages.
type MonospacePart = types.MonospacePart

// The parts that RenderOptions.Monospace takes.
const (
	MonospaceMembers    = types.MonospaceMembers
	MonospaceAttributes = types.MonospaceAttributes
	MonospaceFiles      = types.MonospaceFiles
	MonospaceMessages   = types.MonospaceMessages
)

// MathMarkup is a typeset $$...$$ expression that a MathResolver gives: SVG
// (or HTML) markup and its size in pixels.
type MathMarkup = types.MathMarkup

// MathResolver typesets the TeX of a $$...$$ label (without the delimiters) at
// a font size in pixels, and reports false when it has nothing for it. Then,
// and when RenderOptions.Math is nil, the label shows the $$...$$ source as
// text. sketch has no typesetter of its own.
//
// The markup is written into the SVG unchanged, as pele does: a resolver must
// give markup the host trusts. The resolver gets the TeX as the diagram's
// author wrote it, with entities decoded. Treat it as untrusted input: escape
// it in any markup you build from it, and do not enable commands that make
// links or run scripts, such as KaTeX's trust option.
type MathResolver = types.MathResolver

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

// tagErrors names the diagram type on a PeleError that run panics with and that
// does not name one yet.
//
// pele also turns a RangeError, a JavaScript stack overflow, into a limit error
// here. Go grows the stack instead, so the port has nothing to catch.
func tagErrors(found detected) {
	if e := recover(); e != nil {
		if pe, ok := e.(*types.PeleError); ok {
			if pe.Type == "" {
				pe.Type = string(found.typ)
			}
			if pe.MermaidType == "" {
				pe.MermaidType = string(found.mt)
			}
			pe.Message = text.WrittenCodes(pe.Message)
			pe.Snippet = text.WrittenCodes(pe.Snippet)
		}
		panic(e)
	}
}

type parsed struct {
	typ     Type
	mt      MermaidType
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
	pre := preprocess(src, nil)
	found, ok := detectDiagram(pre.text)
	if !ok {
		types.Throw("No diagram type detected for the given text.", types.CodeUnsupported, types.ErrorInfo{})
	}
	diagram := diagrams[found.typ]
	if diagram == nil {
		types.Throw(`Diagram type "`+string(found.typ)+`" is not supported yet.`, types.CodeUnsupported,
			types.ErrorInfo{Type: string(found.typ), MermaidType: string(found.mt)})
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
	keepComments := diagram.KeepComments
	source := pre.text
	if keepComments {
		source = pre.withComments
	}
	model := parseModel(found, diagram, encodeEntities(source, nil)+"\n", config, pre, maxEdges, &inSource{text: src, keepComments: keepComments})
	return parsed{found.typ, found.mt, diagram, model, config}
}

func parseModel(found detected, diagram *types.Diagram, source string, config Config, pre preprocessed, maxEdges int, origin types.Origin) any {
	defer tagErrors(found)
	types.Locating(source, origin)
	defer types.DoneLocating(source)
	return diagram.Parse(source, config, pre.title, pre.lineOffset, types.ParseLimits{MaxEdges: maxEdges})
}

// inSource maps an offset of the parsed text to the source as written. It records
// the edits of preprocessing only when an error needs a position.
type inSource struct {
	text         string
	keepComments bool
	steps        [][]types.Edit
	source       string
	done         bool
}

func (s *inSource) Find(at int) (string, int) {
	if !s.done {
		s.done = true
		pre := preprocess(s.text, &s.steps)
		kept := pre.withComments
		if !s.keepComments {
			kept = cleanupComments(pre.withComments, &s.steps)
		}
		encodeEntities(kept, &s.steps)
		// Match the line break appended for parsing.
		s.source = normalizeLineBreaks(s.text) + "\n"
	}
	return s.source, types.InputOffset(s.steps, at)
}

// Detect gives the type of diagram the text describes.
func Detect(src string) (typ Type, ok bool) {
	defer func() {
		if recover() != nil {
			typ, ok = "", false
		}
	}()
	return detect(preprocess(src, nil).text)
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
	// MermaidType is the type as Mermaid names it.
	MermaidType MermaidType
	p           parsed
}

// Parse reads diagram text.
func Parse(src string, opt ParseOptions) (d *Diagram, err error) {
	defer guarded(&err)
	p := parseSource(src, opt.Limit, opt.MaxEdges, opt.Config)
	return &Diagram{Type: p.typ, MermaidType: p.mt, p: p}, nil
}

// SVG is a drawn diagram.
type SVG struct {
	Type Type
	// MermaidType is the type as Mermaid names it, such as "flowchart-v2" for `graph TD`.
	MermaidType MermaidType
	Data        string
	Width       float64
	Height      float64
	Links       []LinkInfo
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
	if p.diagram.Math {
		options.Measurer = text.WithMath(options.Measurer, options.Math)
	}
	rendered := renderModel(p, options)
	outputLimit := options.OutputLimit
	if outputLimit <= 0 {
		outputLimit = DefaultOutputLimit
	}
	// A UTF-16 length is never more than the UTF-8 one, so only a long SVG is counted.
	if len(rendered.SVG) > outputLimit && js.Len(rendered.SVG) > outputLimit {
		types.Throw("Diagram output is longer than the limit of "+strconv.Itoa(outputLimit)+" characters.", types.CodeLimit,
			types.ErrorInfo{Type: string(p.typ), MermaidType: string(p.mt)})
	}
	if options.Geometry != nil && rendered.Geometry != nil {
		*options.Geometry = *rendered.Geometry
	}
	for i := range rendered.Links {
		rendered.Links[i].ID = text.DecodeEntities(rendered.Links[i].ID)
	}
	return SVG{Type: p.typ, MermaidType: p.mt, Data: rendered.SVG, Width: rendered.Width, Height: rendered.Height, Links: rendered.Links}
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

func renderModel(p parsed, options RenderOptions) types.Rendered {
	defer tagErrors(detected{p.typ, p.mt})
	return p.diagram.Render(p.model, p.config, options)
}
