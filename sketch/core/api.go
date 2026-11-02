package core

import "tuios.dev/sketch/internal/engine"

// The API, from the engine every entry point shares. A type here is the
// engine's own, so a Diagram from tuios.dev/sketch and one from
// tuios.dev/sketch/core are the same type.

type (
	Type          = engine.Type
	MermaidType   = engine.MermaidType
	Config        = engine.Config
	Error         = engine.Error
	ErrorInfo     = engine.ErrorInfo
	ErrorCode     = engine.ErrorCode
	RenderOptions = engine.RenderOptions
	MonospacePart = engine.MonospacePart
	MathMarkup    = engine.MathMarkup
	MathResolver  = engine.MathResolver
	TextMeasurer  = engine.TextMeasurer
	Theme         = engine.Theme
	LinkInfo      = engine.LinkInfo
	NamedTheme    = engine.NamedTheme
	ParseOptions  = engine.ParseOptions
	Diagram       = engine.Diagram
	SVG           = engine.SVG
	Layout        = engine.Layout
)

const (
	CodeUnsupported     = engine.CodeUnsupported
	CodeSyntax          = engine.CodeSyntax
	CodeSemantic        = engine.CodeSemantic
	CodeLimit           = engine.CodeLimit
	MonospaceMembers    = engine.MonospaceMembers
	MonospaceAttributes = engine.MonospaceAttributes
	MonospaceFiles      = engine.MonospaceFiles
	MonospaceMessages   = engine.MonospaceMessages
	DefaultLimit        = engine.DefaultLimit
	DefaultOutputLimit  = engine.DefaultOutputLimit
	DefaultMaxEdges     = engine.DefaultMaxEdges
)

// Detect gives the type of diagram the text describes. It knows every type,
// also one this entry point does not draw: Supports says that.
func Detect(src string) (Type, bool) { return engine.Detect(src) }

// Supports reports whether the text is a diagram of a type sketch draws.
func Supports(src string) bool { return engine.Supports(src) }

// Parse reads diagram text.
func Parse(src string, opt ParseOptions) (*Diagram, error) { return engine.Parse(src, opt) }

// Render parses and draws diagram text.
func Render(src string, options RenderOptions) (SVG, error) { return engine.Render(src, options) }

// Themes lists the named themes.
func Themes() []NamedTheme { return engine.Themes() }

// ThemeNamed finds a theme by name.
func ThemeNamed(name string) (*Theme, bool) { return engine.ThemeNamed(name) }
