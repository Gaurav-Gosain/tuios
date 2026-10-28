// Ported from pele src/types.ts at 8aff183be57021b533f319a29341a529be934b08.
//
// Optional options are pointers where pele tells `undefined` from a value.

// Package types holds what every diagram type shares: options, results, errors
// and the Diagram interface.
package types

import (
	"slices"
	"time"

	"tuios.dev/sketch/internal/text"
)

// DiagramType names a kind of diagram, as Mermaid does.
type DiagramType string

// MermaidType is the diagram type as Mermaid reports it, such as "flowchart-v2" for `graph TD`.
type MermaidType string

// YamlValue is nil, bool, float64, string, []any or map[string]any.
type YamlValue = any

// Config is Mermaid's configuration, from front matter and init directives.
type Config = map[string]any

type IconResolver = text.IconResolver

// MathMarkup is typeset math. Dimensions are in pixels. HTML requires the typesetter's CSS.
type MathMarkup = text.MathMarkup

// MathResolver typesets TeX. TeX excludes the $$ delimiters; font size is in pixels.
type MathResolver = text.MathResolver

// MonospacePart names a part of a diagram that RenderOptions.Monospace can set in the
// monospace font.
type MonospacePart string

// The parts that RenderOptions.Monospace takes.
const (
	MonospaceMembers    MonospacePart = "members"
	MonospaceAttributes MonospacePart = "attributes"
	MonospaceFiles      MonospacePart = "files"
	MonospaceMessages   MonospacePart = "messages"
)

// HasMonospace reports whether Monospace lists part.
func (o RenderOptions) HasMonospace(part MonospacePart) bool {
	return slices.Contains(o.Monospace, part)
}

type RenderOptions struct {
	Measurer text.TextMeasurer
	// The fonts labels are measured in, and drawn in where the page sets no --pele-font or
	// --pele-font-mono.
	FontFamily     string
	FontFamilyMono string
	// Monospace lists the parts of a diagram that are measured and drawn in the monospace font:
	// "members" (class), "attributes" (ER), "files" (tree view) and "messages" (sequence).
	Monospace  []MonospacePart
	FontSize   *float64
	IDPrefix   *string
	Responsive *bool
	// The width the host has for the diagram, in pixels. A type that can draw itself narrower
	// does, so that its text is not shrunk with it.
	MaxWidth *float64
	// Whether a diagram that runs across may be drawn running down when it does not fit
	// `maxWidth`, and the width under which that is done. On by default, under 640 pixels.
	AutoDirection       *bool
	DirectionBreakpoint *float64
	Padding             *float64
	Limit               int
	OutputLimit         int
	// The most edges a flowchart may have. One short line can ask for millions of them.
	MaxEdges int
	// Schemes a link or an image may use besides a relative address. nil means the default.
	LinkSchemes  []string
	ImageSchemes []string
	Links        *bool
	Images       *bool
	// Link `rel` attribute. Defaults to `noopener`; an empty string omits it.
	LinkRel *string
	Icons   IconResolver
	// Math typesets $$...$$ in the labels of the types that take it. nil leaves the TeX
	// source as text. Its markup is written into the SVG unchanged: the host must trust it.
	// The TeX it gets is the diagram author's, with entities decoded: untrusted input.
	Math   MathResolver
	Config Config
	// The time gantt charts treat as now. The zero time means the current time.
	Now time.Time
	// The time zone gantt charts read dates in. It is not part of pele, which reads them in
	// the process's zone as JavaScript does: nil means time.Local.
	Location *time.Location
	// Theme sets the --pele-* tokens on the root element. It is not part of pele:
	// nil gives pele's output, byte for byte.
	Theme *Theme
	// Geometry, when set, receives the layout the renderer worked out. It is not
	// part of pele and does not change the SVG.
	Geometry *Geometry
}

type LinkInfo struct {
	ID       string
	Href     string
	Internal bool
}

type Rendered struct {
	SVG    string
	Width  float64
	Height float64
	Links  []LinkInfo
	// Geometry is the layout, when RenderOptions.Geometry asked for it. Not part of pele.
	Geometry *Geometry
}

// What a diagram type provides. `source` has been through preprocessing and entity encoding
// and ends with a newline, as Mermaid's parsers expect.
// Limits set by the host. A diagram's own configuration cannot change them.
type ParseLimits struct {
	MaxEdges int
}

type Diagram struct {
	Type DiagramType
	// The name of this type's section in Mermaid's config, when it is not the type's own name.
	Section string
	// A diagram that reports source positions is given the text with its `%%` comment lines kept.
	// `lineOffset` is the number of front matter lines that were removed before that text.
	KeepComments bool
	// Enables $$...$$ in labels.
	Math bool
	// Parse and Render report a problem with the text by panicking with a *PeleError,
	// as pele throws one. The API recovers it.
	Parse  func(source string, config Config, title *string, lineOffset int, limits ParseLimits) any
	Render func(model any, config Config, options RenderOptions) Rendered
}

// Ptr returns a pointer to v, for the optional fields.
func Ptr[T any](v T) *T { return &v }

// Or is `p ?? def`.
func Or[T any](p *T, def T) T {
	if p == nil {
		return def
	}
	return *p
}
