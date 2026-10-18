package tml

import (
	"slices"
	"strconv"
)

// AttrType is the type an attribute value must have.
type AttrType uint8

// The attribute types of the catalog.
const (
	TString   AttrType = iota // free text
	TInt                      // a whole number
	TNumber                   // any number
	TBool                     // true or false
	TEnum                     // one of a fixed list
	TRole                     // a theme role, never a hex colour
	TAccent                   // a theme role or an ANSI slot, slot:magenta
	TSize                     // cells, 50%, fill or fill:2
	TPad                      // one, two or four cell counts
	TData                     // a state path such as state.items, or a literal array
	TPath                     // a state path only, such as state.tab
	TExpr                     // an expression, with or without the braces
	TAction                   // one or more action calls separated by ;
	TDuration                 // 300ms, 5s, 2m
	THref                     // a file: or https: link
	TSrc                      // a file: path or a data:image URI
	TIdent                    // a name: letters, digits, - and _
	TField                    // a field name of a row, or a comma list of them
	TSort                     // a field, optionally :asc or :desc
	TKey                      // a key name such as enter, esc, r or ctrl+b
	TPattern                  // an RE2 regular expression
	TObject                   // a JSON object (the root's state)
)

var attrTypeNames = [...]string{
	TString: "string", TInt: "integer", TNumber: "number", TBool: "boolean",
	TEnum: "enum", TRole: "role", TAccent: "accent", TSize: "size", TPad: "pad",
	TData: "data", TPath: "path", TExpr: "expression", TAction: "action",
	TDuration: "duration", THref: "href", TSrc: "src", TIdent: "name",
	TField: "field", TSort: "sort", TKey: "key", TPattern: "pattern",
	TObject: "object",
}

func (t AttrType) String() string {
	if int(t) < len(attrTypeNames) {
		return attrTypeNames[t]
	}
	return "AttrType(" + strconv.Itoa(int(t)) + ")"
}

// AttrSpec describes one attribute of a component.
type AttrSpec struct {
	Name     string
	Type     AttrType
	Enum     []string // TEnum: the accepted values
	Required bool
	// Bind reports whether the value may be a {binding}.
	Bind bool
	// Min and Max bound a TInt or TNumber when HasRange is set.
	Min, Max float64
	HasRange bool
	// Multiline marks a text attribute that may hold line breaks. The
	// parsers turn a tab or newline in any other text attribute into a space.
	Multiline bool
	Doc       string
}

// Content is what a component may hold.
type Content uint8

// The content models.
const (
	ContentNone   Content = iota // no children
	ContentBlocks                // block components
	ContentInline                // text runs and inline components
	ContentRaw                   // one text run, kept verbatim
	ContentOnly                  // only the components Children names
)

// Component is one entry of the catalog. Lookup and Components return
// copies, so a caller cannot change the catalog.
type Component struct {
	Name     string
	Group    string
	Doc      string
	Attrs    []AttrSpec // sorted by name; the global attributes are separate
	Content  Content
	Children []string // ContentOnly: the accepted child types
	// MinChildren and MaxChildren bound the number of child components.
	// MaxChildren 0 means no bound.
	MinChildren, MaxChildren int
	// Inline marks a component that sits inside text: b, i, u, s, code, c,
	// kbd and a.
	Inline bool
	// Block marks a component that may sit in block content.
	Block bool
}

var roles = [...]string{
	"fg", "dim", "mute", "accent", "accent-bright",
	"success", "warning", "error", "info", "attention", "busy", "edge",
	"canvas", "panel", "surface", "card",
	"accent-tint", "success-tint", "warning-tint", "error-tint",
	"series1", "series2", "series3", "series4", "series5", "series6",
}

var slots = [...]string{
	"black", "red", "green", "yellow", "blue", "magenta", "cyan", "white",
	"bright-black", "bright-red", "bright-green", "bright-yellow",
	"bright-blue", "bright-magenta", "bright-cyan", "bright-white",
}

// Roles returns the theme roles a role attribute accepts. A host resolves
// each to a colour at the client's colour depth.
func Roles() []string { return slices.Clone(roles[:]) }

// Slots returns the ANSI colours the root's accent may name as slot:NAME.
func Slots() []string { return slices.Clone(slots[:]) }

// roleAlias maps a colour word an author reaches for to the role that means
// the same thing.
func roleAlias(s string) string {
	switch s {
	case "red", "danger":
		return "error"
	case "green", "ok":
		return "success"
	case "yellow", "orange", "warn":
		return "warning"
	case "blue":
		return "info"
	case "gray", "grey", "secondary":
		return "dim"
	case "muted":
		return "mute"
	case "primary":
		return "accent"
	case "white", "text":
		return "fg"
	case "border":
		return "edge"
	case "background", "bg":
		return "canvas"
	}
	return ""
}

// componentAlias maps a tag name from HTML or another toolkit to the TML
// component an author most likely meant.
func componentAlias(s string) string {
	switch s {
	case "div", "vbox":
		return "col"
	case "hbox":
		return "row"
	case "span", "p", "li", "label":
		return "text"
	case "h1", "h2", "h3":
		return "heading"
	case "img":
		return "image"
	case "ul", "ol":
		return "list"
	case "pre":
		return "code"
	case "hr":
		return "spacer"
	case "strong":
		return "b"
	case "em":
		return "i"
	case "progressbar":
		return "progress"
	case "meter":
		return "gauge"
	case "chart", "barchart":
		return "bar"
	case "linechart":
		return "line"
	case "piechart":
		return "pie"
	case "tag", "pill":
		return "badge"
	case "dialog":
		return "modal"
	case "panel":
		return "card"
	case "tr", "th", "td":
		return "column"
	case "option", "dropdown":
		return "select"
	case "switch":
		return "toggle"
	case "range":
		return "slider"
	case "link":
		return "a"
	case "notification":
		return "toast"
	case "kpi", "metric":
		return "stat"
	}
	return ""
}

// lookup returns the catalog entry itself. Code in this package must not
// change it.
func lookup(name string) *Component {
	for i := range table {
		if table[i].Name == name {
			return &table[i]
		}
	}
	return nil
}

// Lookup returns a copy of the catalog entry for a component type.
func Lookup(name string) (Component, bool) {
	c := lookup(name)
	if c == nil {
		return Component{}, false
	}
	return c.clone(), true
}

// Components returns a copy of the catalog, sorted by group, then by name.
func Components() []Component {
	out := make([]Component, len(table))
	for i := range table {
		out[i] = table[i].clone()
	}
	return out
}

func (c *Component) clone() Component {
	out := *c
	out.Attrs = make([]AttrSpec, len(c.Attrs))
	for i, a := range c.Attrs {
		a.Enum = slices.Clone(a.Enum)
		out.Attrs[i] = a
	}
	out.Children = slices.Clone(c.Children)
	return out
}

// GlobalAttrs returns the attributes every component takes.
func GlobalAttrs() []AttrSpec {
	out := make([]AttrSpec, len(globals))
	for i, a := range globals {
		a.Enum = slices.Clone(a.Enum)
		out[i] = a
	}
	return out
}

// AttrNames returns the attributes a component accepts, its own first, then
// the global ones, each sorted.
func (c Component) AttrNames() []string {
	out := make([]string, 0, len(c.Attrs)+len(globals))
	for _, a := range c.Attrs {
		out = append(out, a.Name)
	}
	for _, g := range globals {
		if !c.hasOwn(g.Name) {
			out = append(out, g.Name)
		}
	}
	return out
}

func (c Component) hasOwn(name string) bool {
	for _, a := range c.Attrs {
		if a.Name == name {
			return true
		}
	}
	return false
}

// Spec returns the spec of the named attribute, own or global.
func (c Component) Spec(name string) (AttrSpec, bool) {
	for _, a := range c.Attrs {
		if a.Name == name {
			return a, true
		}
	}
	for _, g := range globals {
		if g.Name == name {
			return g, true
		}
	}
	return AttrSpec{}, false
}

// AttrTypeOf returns the type of an attribute of a component type, without
// copying the catalog entry.
func AttrTypeOf(component, attr string) (AttrType, bool) {
	c := lookup(component)
	if c == nil {
		return 0, false
	}
	s, ok := c.Spec(attr)
	return s.Type, ok
}

// ContentOf returns the content model of a component type. An unknown type
// holds blocks.
func ContentOf(component string) Content {
	if c := lookup(component); c != nil {
		return c.Content
	}
	return ContentBlocks
}

// Accepts reports whether the component may hold a child of type child.
func (c Component) Accepts(child string) bool {
	cc := lookup(child)
	switch c.Content {
	case ContentBlocks:
		return cc != nil && cc.Block
	case ContentInline:
		return cc != nil && cc.Inline
	case ContentOnly:
		return slices.Contains(c.Children, child)
	}
	return false
}

// accepted lists the child types c accepts, sorted.
func (c Component) accepted() []string {
	var out []string
	switch c.Content {
	case ContentOnly:
		out = append(out, c.Children...)
	case ContentBlocks, ContentInline:
		for i := range table {
			if c.Accepts(table[i].Name) {
				out = append(out, table[i].Name)
			}
		}
	}
	slices.Sort(out)
	return out
}

// globals are the attributes every component takes.
var globals = [...]AttrSpec{
	{Name: "as", Type: TIdent, Doc: "The name of the element inside each. Default item."},
	{Name: "each", Type: TData, Doc: "Repeats the node once per array element."},
	{Name: "grow", Type: TInt, HasRange: true, Min: 0, Max: 100, Doc: "Share of free space."},
	{Name: "h", Type: TSize, Bind: true, Doc: "Height: cells, 50%, fill or fill:2."},
	{Name: "id", Type: TIdent, Doc: "Names the node for patches, focus and events."},
	{Name: "if", Type: TExpr, Doc: "Drops the node when false."},
	{Name: "key", Type: TString, Bind: true, Doc: "Keeps identity in repeated children."},
	{Name: "max-h", Type: TSize, Doc: "Largest height."},
	{Name: "max-w", Type: TSize, Doc: "Largest width."},
	{Name: "min-h", Type: TSize, Doc: "Smallest height."},
	{Name: "min-w", Type: TSize, Doc: "Smallest width."},
	{Name: "pad", Type: TPad, Doc: "Padding in cells: all, vertical horizontal, or top right bottom left."},
	{Name: "show", Type: TExpr, Doc: "Hides the node when false and keeps its space."},
	{Name: "w", Type: TSize, Bind: true, Doc: "Width: cells, 50%, fill or fill:2."},
}

// table is the catalog, sorted by group, then by name. It is static data:
// the linker lays it out, so it costs no start-up code.
var table = [...]Component{
	{Name: "tml", Group: "document", Doc: "The document root.", Content: ContentBlocks, Attrs: []AttrSpec{
		{Name: "accent", Type: TAccent, Doc: "The accent: a role, or an ANSI slot such as slot:magenta."},
		{Name: "on-key", Type: TAction, Doc: "Runs on a key press no node took. event.key names the key."},
		{Name: "state", Type: TObject, Doc: "The JSON state store. In TML text, a <state> element."},
		{Name: "title", Type: TString, Doc: "The title on the frame."},
		{Name: "version", Type: TInt, Required: true, HasRange: true, Min: 1, Max: 1, Doc: "The TML version. Always 1."},
	}},
	{Name: "col", Group: "layout", Doc: "Lays children out top to bottom.", Content: ContentBlocks, Block: true, Attrs: []AttrSpec{
		{Name: "align", Type: TEnum, Enum: []string{"start", "center", "end", "stretch"}, Doc: "Cross-axis alignment."},
		{Name: "gap", Type: TInt, Bind: true, HasRange: true, Min: 0, Max: 100, Doc: "Cells between children."},
		{Name: "justify", Type: TEnum, Enum: []string{"start", "center", "end", "between", "around"}, Doc: "Main-axis distribution."},
		{Name: "on-press", Type: TAction, Doc: "Runs on enter or a click."},
	}},
	{Name: "grid", Group: "layout", Doc: "Wraps children into equal columns.", Content: ContentBlocks, Block: true, Attrs: []AttrSpec{
		{Name: "cols", Type: TInt, Required: true, HasRange: true, Min: 1, Max: 24, Doc: "The number of columns."},
		{Name: "gap", Type: TInt, Bind: true, HasRange: true, Min: 0, Max: 100, Doc: "Cells between children."},
	}},
	{Name: "row", Group: "layout", Doc: "Lays children out left to right.", Content: ContentBlocks, Block: true, Attrs: []AttrSpec{
		{Name: "align", Type: TEnum, Enum: []string{"start", "center", "end", "stretch"}, Doc: "Cross-axis alignment."},
		{Name: "gap", Type: TInt, Bind: true, HasRange: true, Min: 0, Max: 100, Doc: "Cells between children."},
		{Name: "justify", Type: TEnum, Enum: []string{"start", "center", "end", "between", "around"}, Doc: "Main-axis distribution."},
		{Name: "on-press", Type: TAction, Doc: "Runs on enter or a click."},
	}},
	{Name: "scroll", Group: "layout", Doc: "Scrolls its children.", Content: ContentBlocks, Block: true, Attrs: []AttrSpec{
		{Name: "follow", Type: TEnum, Enum: []string{"start", "end"}, Doc: "Keeps the view at this end as content grows."},
	}},
	{Name: "spacer", Group: "layout", Doc: "Takes the free space.", Content: ContentNone, Block: true},
	{Name: "split", Group: "layout", Doc: "Two children with a divider that a person can drag.", Content: ContentBlocks, MinChildren: 2, MaxChildren: 2, Block: true, Attrs: []AttrSpec{
		{Name: "dir", Type: TEnum, Enum: []string{"h", "v"}, Doc: "h puts the children side by side, v stacks them."},
		{Name: "ratio", Type: TNumber, HasRange: true, Min: 0, Max: 1, Doc: "The share of the first child."},
	}},
	{Name: "stack", Group: "layout", Doc: "Layers children on top of each other.", Content: ContentBlocks, Block: true},
	{Name: "code", Group: "text", Doc: "Code, highlighted by lang. Also inline inside text.", Content: ContentRaw, Inline: true, Block: true, Attrs: []AttrSpec{
		{Name: "from", Type: TInt, HasRange: true, Min: 1, Max: 1e+09, Doc: "The first line number."},
		{Name: "lang", Type: TString, Doc: "The language for highlighting."},
		{Name: "lines", Type: TBool, Doc: "Shows line numbers."},
	}},
	{Name: "diff", Group: "text", Doc: "A unified diff.", Content: ContentRaw, Block: true, Attrs: []AttrSpec{
		{Name: "lang", Type: TString, Doc: "The language for highlighting."},
		{Name: "layout", Type: TEnum, Enum: []string{"unified", "split"}, Doc: "unified or split."},
	}},
	{Name: "heading", Group: "text", Doc: "A heading.", Content: ContentInline, Block: true, Attrs: []AttrSpec{
		{Name: "level", Type: TInt, Bind: true, HasRange: true, Min: 1, Max: 3, Doc: "1 is the largest."},
		{Name: "role", Type: TRole, Bind: true, Doc: "A theme role colour."},
	}},
	{Name: "markdown", Group: "text", Doc: "Markdown text.", Content: ContentRaw, Block: true},
	{Name: "text", Group: "text", Doc: "A paragraph of text with inline styling.", Content: ContentInline, Block: true, Attrs: []AttrSpec{
		{Name: "align", Type: TEnum, Enum: []string{"left", "center", "right"}, Doc: "Horizontal alignment."},
		{Name: "on-press", Type: TAction, Doc: "Runs on enter or a click."},
		{Name: "role", Type: TRole, Bind: true, Doc: "A theme role colour."},
		{Name: "wrap", Type: TEnum, Enum: []string{"word", "char", "none"}, Doc: "How long lines break."},
	}},
	{Name: "a", Group: "inline", Doc: "A link. It opens after the person confirms.", Content: ContentInline, Inline: true, Attrs: []AttrSpec{
		{Name: "href", Type: THref, Required: true, Doc: "A file: or https: link."},
	}},
	{Name: "b", Group: "inline", Doc: "Bold.", Content: ContentInline, Inline: true},
	{Name: "c", Group: "inline", Doc: "Colours its text with a role.", Content: ContentInline, Inline: true, Attrs: []AttrSpec{
		{Name: "role", Type: TRole, Required: true, Bind: true, Doc: "A theme role colour."},
	}},
	{Name: "i", Group: "inline", Doc: "Italic.", Content: ContentInline, Inline: true},
	{Name: "kbd", Group: "inline", Doc: "A key name.", Content: ContentRaw, Inline: true},
	{Name: "s", Group: "inline", Doc: "Strikethrough.", Content: ContentInline, Inline: true},
	{Name: "u", Group: "inline", Doc: "Underline.", Content: ContentInline, Inline: true},
	{Name: "column", Group: "data", Doc: "A column of a table.", Content: ContentNone, Attrs: []AttrSpec{
		{Name: "align", Type: TEnum, Enum: []string{"left", "center", "right"}, Doc: "Horizontal alignment."},
		{Name: "field", Type: TField, Required: true, Doc: "The row field the column shows."},
		{Name: "format", Type: TExpr, Doc: "An expression of value and row that formats the cell."},
		{Name: "sort", Type: TBool, Doc: "A person can sort by this column."},
		{Name: "title", Type: TString, Bind: true, Doc: "The header."},
	}},
	{Name: "item", Group: "data", Doc: "One pair of a kv.", Content: ContentNone, Attrs: []AttrSpec{
		{Name: "k", Type: TString, Required: true, Bind: true, Doc: "The key."},
		{Name: "role", Type: TRole, Bind: true, Doc: "A theme role colour."},
		{Name: "v", Type: TString, Bind: true, Doc: "The value."},
	}},
	{Name: "kv", Group: "data", Doc: "Key and value pairs in two aligned columns.", Content: ContentOnly, Children: []string{"item"}, MinChildren: 1, Block: true},
	{Name: "list", Group: "data", Doc: "Repeats its children once per element of a state array.", Content: ContentBlocks, Block: true, Attrs: []AttrSpec{
		{Name: "as", Type: TIdent, Doc: "The name of the element inside the list. Default item."},
		{Name: "items", Type: TData, Required: true, Doc: "The state array."},
		{Name: "on-key", Type: TAction, Doc: "Runs on a key press. event.key names the key."},
		{Name: "on-select", Type: TAction, Doc: "Runs when an item is selected. event.item is the item."},
		{Name: "select", Type: TEnum, Enum: []string{"none", "single", "multi"}, Doc: "How many items a person can select."},
	}},
	{Name: "stat", Group: "data", Doc: "A labelled number, the KPI tile.", Content: ContentNone, Block: true, Attrs: []AttrSpec{
		{Name: "delta", Type: TString, Bind: true, Doc: "A change to show beside the number, such as +12."},
		{Name: "label", Type: TString, Required: true, Bind: true, Doc: "The label under the number."},
		{Name: "on-press", Type: TAction, Doc: "Runs on enter or a click."},
		{Name: "role", Type: TRole, Bind: true, Doc: "A theme role colour."},
		{Name: "value", Type: TString, Required: true, Bind: true, Doc: "The number."},
	}},
	{Name: "table", Group: "data", Doc: "Rows of a state array, sorted, filtered and scrolled locally.", Content: ContentOnly, Children: []string{"column"}, MinChildren: 1, Block: true, Attrs: []AttrSpec{
		{Name: "filter", Type: TExpr, Doc: "A text that rows must contain."},
		{Name: "key", Type: TField, Doc: "The field that keeps a row's identity."},
		{Name: "on-key", Type: TAction, Doc: "Runs on a key press. event.key names the key."},
		{Name: "on-select", Type: TAction, Doc: "Runs when a row is selected. event.row is the row."},
		{Name: "rows", Type: TData, Required: true, Doc: "The state array of rows."},
		{Name: "select", Type: TEnum, Enum: []string{"none", "single", "multi"}, Doc: "How many rows a person can select."},
		{Name: "sort", Type: TSort, Doc: "The first sort: field, field:asc or field:desc."},
	}},
	{Name: "tree", Group: "data", Doc: "A tree of nested state objects.", Content: ContentNone, Block: true, Attrs: []AttrSpec{
		{Name: "children", Type: TField, Doc: "The field that holds the child array. Default children."},
		{Name: "items", Type: TData, Required: true, Doc: "The state array of top nodes."},
		{Name: "label", Type: TField, Doc: "The field to show. Default name."},
		{Name: "on-select", Type: TAction, Doc: "Runs when a node is selected. event.item is the node."},
		{Name: "open", Type: TInt, Bind: true, HasRange: true, Min: 0, Max: 32, Doc: "How many levels start open."},
		{Name: "select", Type: TEnum, Enum: []string{"none", "single", "multi"}, Doc: "How many nodes a person can select."},
	}},
	{Name: "area", Group: "chart", Doc: "An area chart.", Content: ContentNone, Block: true, Attrs: []AttrSpec{
		{Name: "data", Type: TData, Required: true, Doc: "A state array of rows or numbers."},
		{Name: "graphics", Type: TEnum, Enum: []string{"auto", "kitty", "cells", "ascii"}, Doc: "How to draw: auto picks kitty graphics, then cells, then ASCII."},
		{Name: "role", Type: TRole, Bind: true, Doc: "A theme role colour."},
		{Name: "title", Type: TString, Bind: true, Doc: "A caption above the chart."},
		{Name: "x", Type: TField, Doc: "The x field."},
		{Name: "y", Type: TField, Doc: "The y field."},
	}},
	{Name: "bar", Group: "chart", Doc: "A bar chart.", Content: ContentNone, Block: true, Attrs: []AttrSpec{
		{Name: "data", Type: TData, Required: true, Doc: "A state array of numbers or rows."},
		{Name: "dir", Type: TEnum, Enum: []string{"v", "h"}, Doc: "v draws columns, h draws bars."},
		{Name: "graphics", Type: TEnum, Enum: []string{"auto", "kitty", "cells", "ascii"}, Doc: "How to draw: auto picks kitty graphics, then cells, then ASCII."},
		{Name: "role", Type: TRole, Bind: true, Doc: "A theme role colour."},
		{Name: "title", Type: TString, Bind: true, Doc: "A caption above the chart."},
		{Name: "x", Type: TField, Doc: "The label field."},
		{Name: "y", Type: TField, Doc: "The value field."},
	}},
	{Name: "gauge", Group: "chart", Doc: "A value against a maximum.", Content: ContentNone, Block: true, Attrs: []AttrSpec{
		{Name: "graphics", Type: TEnum, Enum: []string{"auto", "kitty", "cells", "ascii"}, Doc: "How to draw: auto picks kitty graphics, then cells, then ASCII."},
		{Name: "label", Type: TString, Bind: true, Doc: "The label."},
		{Name: "max", Type: TNumber, Bind: true, Doc: "The maximum. Default 100."},
		{Name: "role", Type: TRole, Bind: true, Doc: "A theme role colour."},
		{Name: "title", Type: TString, Bind: true, Doc: "A caption above the chart."},
		{Name: "value", Type: TNumber, Required: true, Bind: true, Doc: "The value."},
	}},
	{Name: "heatmap", Group: "chart", Doc: "A grid of values as shades.", Content: ContentNone, Block: true, Attrs: []AttrSpec{
		{Name: "data", Type: TData, Required: true, Doc: "A state array of rows."},
		{Name: "graphics", Type: TEnum, Enum: []string{"auto", "kitty", "cells", "ascii"}, Doc: "How to draw: auto picks kitty graphics, then cells, then ASCII."},
		{Name: "role", Type: TRole, Bind: true, Doc: "A theme role colour."},
		{Name: "title", Type: TString, Bind: true, Doc: "A caption above the chart."},
		{Name: "v", Type: TField, Doc: "The value field."},
		{Name: "x", Type: TField, Doc: "The column field."},
		{Name: "y", Type: TField, Doc: "The row field."},
	}},
	{Name: "histogram", Group: "chart", Doc: "Counts of values in bins.", Content: ContentNone, Block: true, Attrs: []AttrSpec{
		{Name: "bins", Type: TInt, Bind: true, HasRange: true, Min: 1, Max: 200, Doc: "The number of bins."},
		{Name: "graphics", Type: TEnum, Enum: []string{"auto", "kitty", "cells", "ascii"}, Doc: "How to draw: auto picks kitty graphics, then cells, then ASCII."},
		{Name: "role", Type: TRole, Bind: true, Doc: "A theme role colour."},
		{Name: "title", Type: TString, Bind: true, Doc: "A caption above the chart."},
		{Name: "values", Type: TData, Required: true, Doc: "A state array of numbers."},
	}},
	{Name: "line", Group: "chart", Doc: "A line chart.", Content: ContentNone, Block: true, Attrs: []AttrSpec{
		{Name: "axis", Type: TBool, Bind: true, Doc: "Draws the axes."},
		{Name: "data", Type: TData, Required: true, Doc: "A state array of rows."},
		{Name: "graphics", Type: TEnum, Enum: []string{"auto", "kitty", "cells", "ascii"}, Doc: "How to draw: auto picks kitty graphics, then cells, then ASCII."},
		{Name: "role", Type: TRole, Bind: true, Doc: "A theme role colour."},
		{Name: "title", Type: TString, Bind: true, Doc: "A caption above the chart."},
		{Name: "x", Type: TField, Doc: "The x field."},
		{Name: "y", Type: TField, Doc: "The y field, or a comma list for several series."},
	}},
	{Name: "pie", Group: "chart", Doc: "Shares of a whole.", Content: ContentNone, Block: true, Attrs: []AttrSpec{
		{Name: "data", Type: TData, Required: true, Doc: "A state array of rows."},
		{Name: "graphics", Type: TEnum, Enum: []string{"auto", "kitty", "cells", "ascii"}, Doc: "How to draw: auto picks kitty graphics, then cells, then ASCII."},
		{Name: "label", Type: TField, Doc: "The label field."},
		{Name: "role", Type: TRole, Bind: true, Doc: "A theme role colour."},
		{Name: "style", Type: TEnum, Enum: []string{"pie", "donut"}, Doc: "pie or donut."},
		{Name: "title", Type: TString, Bind: true, Doc: "A caption above the chart."},
		{Name: "v", Type: TField, Doc: "The value field."},
	}},
	{Name: "sparkline", Group: "chart", Doc: "A one-row trend.", Content: ContentNone, Block: true, Attrs: []AttrSpec{
		{Name: "graphics", Type: TEnum, Enum: []string{"auto", "kitty", "cells", "ascii"}, Doc: "How to draw: auto picks kitty graphics, then cells, then ASCII."},
		{Name: "role", Type: TRole, Bind: true, Doc: "A theme role colour."},
		{Name: "title", Type: TString, Bind: true, Doc: "A caption above the chart."},
		{Name: "values", Type: TData, Required: true, Doc: "A state array of numbers."},
	}},
	{Name: "image", Group: "media", Doc: "An image. Without graphics it falls back to block art, then to alt.", Content: ContentNone, Block: true, Attrs: []AttrSpec{
		{Name: "alt", Type: TString, Required: true, Bind: true, Doc: "The text shown when the image cannot be."},
		{Name: "graphics", Type: TEnum, Enum: []string{"auto", "kitty", "cells", "ascii"}, Doc: "How to draw: auto picks kitty graphics, then cells, then ASCII."},
		{Name: "on-press", Type: TAction, Doc: "Runs on enter or a click."},
		{Name: "src", Type: TSrc, Required: true, Doc: "A file: path or a data:image URI."},
	}},
	{Name: "mermaid", Group: "media", Doc: "A Mermaid diagram. Until the renderer lands, it shows the source.", Content: ContentRaw, Block: true},
	{Name: "badge", Group: "status", Doc: "A short coloured tag.", Content: ContentInline, Block: true, Attrs: []AttrSpec{
		{Name: "on-press", Type: TAction, Doc: "Runs on enter or a click."},
		{Name: "role", Type: TRole, Bind: true, Doc: "A theme role colour."},
	}},
	{Name: "progress", Group: "status", Doc: "A progress bar.", Content: ContentNone, Block: true, Attrs: []AttrSpec{
		{Name: "label", Type: TString, Bind: true, Doc: "The label."},
		{Name: "role", Type: TRole, Bind: true, Doc: "A theme role colour."},
		{Name: "value", Type: TNumber, Required: true, Bind: true, Doc: "From 0 to 1."},
	}},
	{Name: "spinner", Group: "status", Doc: "A busy mark.", Content: ContentNone, Block: true, Attrs: []AttrSpec{
		{Name: "label", Type: TString, Bind: true, Doc: "The label."},
	}},
	{Name: "status", Group: "status", Doc: "The tuios agent state glyph and word.", Content: ContentNone, Block: true, Attrs: []AttrSpec{
		{Name: "label", Type: TString, Bind: true, Doc: "The label."},
		{Name: "state", Type: TEnum, Enum: []string{"working", "needs_input", "idle", "done", "errored", "unknown"}, Required: true, Bind: true, Doc: "The agent state."},
	}},
	{Name: "button", Group: "input", Doc: "A button.", Content: ContentInline, Block: true, Attrs: []AttrSpec{
		{Name: "disabled", Type: TBool, Bind: true, Doc: "The button does not take input."},
		{Name: "key", Type: TKey, Doc: "The key that presses the button, such as enter or r."},
		{Name: "kind", Type: TEnum, Enum: []string{"default", "primary", "danger"}, Doc: "How much the button stands out."},
		{Name: "on-press", Type: TAction, Doc: "Runs on enter or a click."},
		{Name: "submit", Type: TBool, Doc: "Submits the form it is in."},
	}},
	{Name: "checkbox", Group: "input", Doc: "A checkbox.", Content: ContentNone, Block: true, Attrs: []AttrSpec{
		{Name: "bind", Type: TPath, Doc: "A state path the field reads and writes."},
		{Name: "disabled", Type: TBool, Bind: true, Doc: "The field does not take input."},
		{Name: "label", Type: TString, Bind: true, Doc: "The label shown before the field."},
		{Name: "name", Type: TIdent, Required: true, Doc: "The key of the value in the submitted form."},
		{Name: "on-change", Type: TAction, Doc: "Runs when the value changes."},
		{Name: "required", Type: TBool, Bind: true, Doc: "The form does not submit while the field is empty."},
		{Name: "value", Type: TBool, Bind: true, Doc: "The first value."},
	}},
	{Name: "form", Group: "input", Doc: "Collects its fields into one value for submit.", Content: ContentBlocks, Block: true, Attrs: []AttrSpec{
		{Name: "on-key", Type: TAction, Doc: "Runs on a key press. event.key names the key."},
		{Name: "on-submit", Type: TAction, Doc: "Runs on submit. form is the value."},
	}},
	{Name: "input", Group: "input", Doc: "A one-line text field.", Content: ContentNone, Block: true, Attrs: []AttrSpec{
		{Name: "bind", Type: TPath, Doc: "A state path the field reads and writes."},
		{Name: "disabled", Type: TBool, Bind: true, Doc: "The field does not take input."},
		{Name: "label", Type: TString, Bind: true, Doc: "The label shown before the field."},
		{Name: "max", Type: TNumber, Bind: true, Doc: "The largest number, for type number."},
		{Name: "min", Type: TNumber, Bind: true, Doc: "The smallest number, for type number."},
		{Name: "name", Type: TIdent, Required: true, Doc: "The key of the value in the submitted form."},
		{Name: "numeric", Type: TBool, Bind: true, Doc: "Takes a number only."},
		{Name: "on-change", Type: TAction, Doc: "Runs when the value changes."},
		{Name: "pattern", Type: TPattern, Doc: "An RE2 regular expression the value must match."},
		{Name: "placeholder", Type: TString, Bind: true, Doc: "Text shown while the field is empty."},
		{Name: "required", Type: TBool, Bind: true, Doc: "The form does not submit while the field is empty."},
		{Name: "value", Type: TString, Bind: true, Doc: "The first value."},
	}},
	{Name: "select", Group: "input", Doc: "Picks one value from a list.", Content: ContentNone, Block: true, Attrs: []AttrSpec{
		{Name: "bind", Type: TPath, Doc: "A state path the field reads and writes."},
		{Name: "disabled", Type: TBool, Bind: true, Doc: "The field does not take input."},
		{Name: "label", Type: TString, Bind: true, Doc: "The label shown before the field."},
		{Name: "name", Type: TIdent, Required: true, Doc: "The key of the value in the submitted form."},
		{Name: "on-change", Type: TAction, Doc: "Runs when the value changes."},
		{Name: "options", Type: TData, Required: true, Doc: "A state array, or a literal array, of the choices."},
		{Name: "required", Type: TBool, Bind: true, Doc: "The form does not submit while the field is empty."},
		{Name: "value", Type: TString, Bind: true, Doc: "The first value."},
	}},
	{Name: "slider", Group: "input", Doc: "A number on a track.", Content: ContentNone, Block: true, Attrs: []AttrSpec{
		{Name: "bind", Type: TPath, Doc: "A state path the field reads and writes."},
		{Name: "disabled", Type: TBool, Bind: true, Doc: "The field does not take input."},
		{Name: "label", Type: TString, Bind: true, Doc: "The label shown before the field."},
		{Name: "max", Type: TNumber, Bind: true, Doc: "The largest value."},
		{Name: "min", Type: TNumber, Bind: true, Doc: "The smallest value."},
		{Name: "name", Type: TIdent, Required: true, Doc: "The key of the value in the submitted form."},
		{Name: "on-change", Type: TAction, Doc: "Runs when the value changes."},
		{Name: "required", Type: TBool, Bind: true, Doc: "The form does not submit while the field is empty."},
		{Name: "step", Type: TNumber, Bind: true, Doc: "The step."},
		{Name: "value", Type: TNumber, Bind: true, Doc: "The first value."},
	}},
	{Name: "textarea", Group: "input", Doc: "A multiline text field.", Content: ContentNone, Block: true, Attrs: []AttrSpec{
		{Name: "bind", Type: TPath, Doc: "A state path the field reads and writes."},
		{Name: "disabled", Type: TBool, Bind: true, Doc: "The field does not take input."},
		{Name: "label", Type: TString, Bind: true, Doc: "The label shown before the field."},
		{Name: "name", Type: TIdent, Required: true, Doc: "The key of the value in the submitted form."},
		{Name: "on-change", Type: TAction, Doc: "Runs when the value changes."},
		{Name: "placeholder", Type: TString, Bind: true, Multiline: true, Doc: "Text shown while the field is empty."},
		{Name: "required", Type: TBool, Bind: true, Doc: "The form does not submit while the field is empty."},
		{Name: "rows", Type: TInt, Bind: true, HasRange: true, Min: 1, Max: 100, Doc: "The visible lines."},
		{Name: "value", Type: TString, Bind: true, Multiline: true, Doc: "The first value."},
	}},
	{Name: "toggle", Group: "input", Doc: "An on and off switch.", Content: ContentNone, Block: true, Attrs: []AttrSpec{
		{Name: "bind", Type: TPath, Doc: "A state path the field reads and writes."},
		{Name: "disabled", Type: TBool, Bind: true, Doc: "The field does not take input."},
		{Name: "label", Type: TString, Bind: true, Doc: "The label shown before the field."},
		{Name: "name", Type: TIdent, Required: true, Doc: "The key of the value in the submitted form."},
		{Name: "on-change", Type: TAction, Doc: "Runs when the value changes."},
		{Name: "required", Type: TBool, Bind: true, Doc: "The form does not submit while the field is empty."},
		{Name: "value", Type: TBool, Bind: true, Doc: "The first value."},
	}},
	{Name: "card", Group: "container", Doc: "A titled box, in the cockpit card style.", Content: ContentBlocks, Block: true, Attrs: []AttrSpec{
		{Name: "fold", Type: TBool, Bind: true, Doc: "Starts folded."},
		{Name: "meta", Type: TString, Bind: true, Doc: "Text at the right of the title."},
		{Name: "on-press", Type: TAction, Doc: "Runs on enter or a click."},
		{Name: "role", Type: TRole, Bind: true, Doc: "A theme role colour."},
		{Name: "title", Type: TString, Bind: true, Doc: "The title."},
	}},
	{Name: "modal", Group: "container", Doc: "A box over the surface that holds focus while open.", Content: ContentBlocks, Block: true, Attrs: []AttrSpec{
		{Name: "open", Type: TBool, Required: true, Bind: true, Doc: "Shows the modal when true."},
		{Name: "title", Type: TString, Bind: true, Doc: "The title."},
	}},
	{Name: "tab", Group: "container", Doc: "One tab of tabs.", Content: ContentBlocks, Attrs: []AttrSpec{
		{Name: "id", Type: TIdent, Required: true, Doc: "The tab's id."},
		{Name: "title", Type: TString, Required: true, Bind: true, Doc: "The tab's title."},
	}},
	{Name: "tabs", Group: "container", Doc: "Shows one tab at a time.", Content: ContentOnly, Children: []string{"tab"}, MinChildren: 1, Block: true, Attrs: []AttrSpec{
		{Name: "on-change", Type: TAction, Doc: "Runs when the open tab changes. event.value is its id."},
		{Name: "value", Type: TPath, Doc: "A state path that holds the open tab's id."},
	}},
	{Name: "toast", Group: "container", Doc: "A short notice that goes away.", Content: ContentInline, Block: true, Attrs: []AttrSpec{
		{Name: "role", Type: TRole, Bind: true, Doc: "A theme role colour."},
		{Name: "ttl", Type: TDuration, Doc: "How long it shows, such as 5s."},
	}},
}
