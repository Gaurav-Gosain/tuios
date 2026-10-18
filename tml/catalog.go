package tml

import (
	"slices"
	"sort"
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

func (t AttrType) String() string { return attrTypeNames[t] }

// AttrSpec describes one attribute of a component.
type AttrSpec struct {
	Type     AttrType
	Enum     []string // TEnum: the accepted values
	Required bool
	// Bind reports whether the value may be a {binding}.
	Bind bool
	// Min and Max bound a TInt or TNumber when HasRange is set.
	Min, Max float64
	HasRange bool
	Doc      string
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

// Component is one entry of the catalog.
type Component struct {
	Name     string
	Group    string
	Doc      string
	Attrs    map[string]AttrSpec
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

// Roles are the theme roles a role attribute accepts. A host resolves each to
// a colour at the client's colour depth.
var Roles = []string{
	"fg", "dim", "mute", "accent", "accent-bright",
	"success", "warning", "error", "info", "attention", "busy", "edge",
	"canvas", "panel", "surface", "card",
	"accent-tint", "success-tint", "warning-tint", "error-tint",
	"series1", "series2", "series3", "series4", "series5", "series6",
}

// Slots are the ANSI colours the root's accent may name as slot:NAME.
var Slots = []string{
	"black", "red", "green", "yellow", "blue", "magenta", "cyan", "white",
	"bright-black", "bright-red", "bright-green", "bright-yellow",
	"bright-blue", "bright-magenta", "bright-cyan", "bright-white",
}

// roleAliases maps colour words an author reaches for to the role that means
// the same thing.
var roleAliases = map[string]string{
	"red": "error", "danger": "error", "green": "success", "ok": "success",
	"yellow": "warning", "orange": "warning", "warn": "warning", "blue": "info",
	"gray": "dim", "grey": "dim", "muted": "mute", "primary": "accent",
	"secondary": "dim", "white": "fg", "text": "fg", "border": "edge",
	"background": "canvas", "bg": "canvas",
}

// componentAliases maps tag names from HTML and other toolkits to the TML
// component an author most likely meant.
var componentAliases = map[string]string{
	"div": "col", "span": "text", "p": "text", "h1": "heading", "h2": "heading",
	"h3": "heading", "img": "image", "ul": "list", "ol": "list", "li": "text",
	"pre": "code", "hr": "spacer", "strong": "b", "em": "i", "vbox": "col",
	"hbox": "row", "progressbar": "progress", "meter": "gauge", "chart": "bar",
	"barchart": "bar", "linechart": "line", "piechart": "pie", "label": "text",
	"tag": "badge", "pill": "badge", "dialog": "modal", "panel": "card",
	"tr": "column", "th": "column", "td": "column", "option": "select",
	"switch": "toggle", "range": "slider", "dropdown": "select", "link": "a",
	"notification": "toast", "kpi": "stat", "metric": "stat",
}

// Global attributes apply to every component.
var globalAttrs = map[string]AttrSpec{
	"id":    {Type: TIdent, Doc: "Names the node for patches, focus and events."},
	"key":   {Type: TString, Bind: true, Doc: "Keeps identity in repeated children."},
	"each":  {Type: TData, Doc: "Repeats the node once per array element."},
	"as":    {Type: TIdent, Doc: "The name of the element inside each. Default item."},
	"if":    {Type: TExpr, Doc: "Drops the node when false."},
	"show":  {Type: TExpr, Doc: "Hides the node when false and keeps its space."},
	"w":     {Type: TSize, Bind: true, Doc: "Width: cells, 50%, fill or fill:2."},
	"h":     {Type: TSize, Bind: true, Doc: "Height: cells, 50%, fill or fill:2."},
	"min-w": {Type: TSize, Doc: "Smallest width."},
	"max-w": {Type: TSize, Doc: "Largest width."},
	"min-h": {Type: TSize, Doc: "Smallest height."},
	"max-h": {Type: TSize, Doc: "Largest height."},
	"pad":   {Type: TPad, Doc: "Padding in cells: all, vertical horizontal, or top right bottom left."},
	"grow":  {Type: TInt, HasRange: true, Min: 0, Max: 100, Doc: "Share of free space."},
}

var catalog = map[string]*Component{}

func init() {
	for _, c := range components() {
		catalog[c.Name] = c
	}
}

// Lookup returns the catalog entry for a component type.
func Lookup(name string) (*Component, bool) {
	c, ok := catalog[name]
	return c, ok
}

// Components returns the catalog sorted by group, then by name.
func Components() []*Component {
	out := make([]*Component, 0, len(catalog))
	for _, c := range catalog {
		out = append(out, c)
	}
	order := map[string]int{}
	for i, g := range groupOrder {
		order[g] = i
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Group != out[j].Group {
			return order[out[i].Group] < order[out[j].Group]
		}
		return out[i].Name < out[j].Name
	})
	return out
}

var groupOrder = []string{"document", "layout", "text", "inline", "data", "chart", "media", "status", "input", "container"}

// AttrNames returns the attributes a component accepts, its own first, then
// the global ones, each sorted.
func (c *Component) AttrNames() []string {
	own := make([]string, 0, len(c.Attrs))
	for n := range c.Attrs {
		own = append(own, n)
	}
	sort.Strings(own)
	global := make([]string, 0, len(globalAttrs))
	for n := range globalAttrs {
		if _, dup := c.Attrs[n]; !dup {
			global = append(global, n)
		}
	}
	sort.Strings(global)
	return append(own, global...)
}

// Spec returns the spec of the named attribute, own or global.
func (c *Component) Spec(name string) (AttrSpec, bool) {
	if s, ok := c.Attrs[name]; ok {
		return s, true
	}
	s, ok := globalAttrs[name]
	return s, ok
}

// Accepts reports whether the component may hold a child of type child.
func (c *Component) Accepts(child string) bool {
	cc, known := catalog[child]
	switch c.Content {
	case ContentBlocks:
		return known && cc.Block
	case ContentInline:
		return known && cc.Inline
	case ContentOnly:
		return slices.Contains(c.Children, child)
	}
	return false
}

// accepted lists the child types c accepts, sorted.
func (c *Component) accepted() []string {
	var out []string
	switch c.Content {
	case ContentOnly:
		out = append(out, c.Children...)
	case ContentBlocks, ContentInline:
		for _, cc := range catalog {
			if c.Accepts(cc.Name) {
				out = append(out, cc.Name)
			}
		}
	}
	sort.Strings(out)
	return out
}

// attrMap is the attribute table of one component.
type attrMap = map[string]AttrSpec

func str(doc string) AttrSpec { return AttrSpec{Type: TString, Bind: true, Doc: doc} }
func reqStr(doc string) AttrSpec {
	return AttrSpec{Type: TString, Bind: true, Required: true, Doc: doc}
}
func num(doc string) AttrSpec { return AttrSpec{Type: TNumber, Bind: true, Doc: doc} }
func intRange(lo, hi float64, doc string) AttrSpec {
	return AttrSpec{Type: TInt, Bind: true, HasRange: true, Min: lo, Max: hi, Doc: doc}
}
func boolean(doc string) AttrSpec { return AttrSpec{Type: TBool, Bind: true, Doc: doc} }
func enum(doc string, values ...string) AttrSpec {
	return AttrSpec{Type: TEnum, Enum: values, Doc: doc}
}
func role() AttrSpec {
	return AttrSpec{Type: TRole, Bind: true, Doc: "A theme role colour."}
}
func data(doc string) AttrSpec     { return AttrSpec{Type: TData, Required: true, Doc: doc} }
func field(doc string) AttrSpec    { return AttrSpec{Type: TField, Doc: doc} }
func action(doc string) AttrSpec   { return AttrSpec{Type: TAction, Doc: doc} }
func ident(doc string) AttrSpec    { return AttrSpec{Type: TIdent, Doc: doc} }
func reqIdent(doc string) AttrSpec { return AttrSpec{Type: TIdent, Required: true, Doc: doc} }

var graphicsAttr = enum("How to draw: auto picks kitty graphics, then cells, then ASCII.", "auto", "kitty", "cells", "ascii")

func chart(name, doc string, extra attrMap) *Component {
	a := attrMap{"title": str("A caption above the chart."), "graphics": graphicsAttr, "role": role()}
	for k, v := range extra {
		a[k] = v
	}
	return &Component{Name: name, Group: "chart", Doc: doc, Attrs: a, Block: true}
}

// The input field attributes every field shares.
func fieldAttrs(extra attrMap) attrMap {
	a := attrMap{
		"name":      reqIdent("The key of the value in the submitted form."),
		"label":     str("The label shown before the field."),
		"bind":      {Type: TPath, Doc: "A state path the field reads and writes."},
		"required":  boolean("The form does not submit while the field is empty."),
		"on-change": action("Runs when the value changes."),
		"disabled":  boolean("The field does not take input."),
	}
	for k, v := range extra {
		a[k] = v
	}
	return a
}

func components() []*Component {
	blocksOf := func(name, group, doc string, a attrMap) *Component {
		if a == nil {
			a = attrMap{}
		}
		return &Component{Name: name, Group: group, Doc: doc, Attrs: a, Content: ContentBlocks, Block: true}
	}
	inlineHolder := func(name, group, doc string, a attrMap) *Component {
		if a == nil {
			a = attrMap{}
		}
		return &Component{Name: name, Group: group, Doc: doc, Attrs: a, Content: ContentInline, Block: true}
	}
	inline := func(name, doc string, a attrMap) *Component {
		if a == nil {
			a = attrMap{}
		}
		return &Component{Name: name, Group: "inline", Doc: doc, Attrs: a, Content: ContentInline, Inline: true}
	}
	void := func(name, group, doc string, a attrMap) *Component {
		return &Component{Name: name, Group: group, Doc: doc, Attrs: a, Block: true}
	}
	raw := func(name, group, doc string, a attrMap) *Component {
		if a == nil {
			a = attrMap{}
		}
		return &Component{Name: name, Group: group, Doc: doc, Attrs: a, Content: ContentRaw, Block: true}
	}
	align := enum("Cross-axis alignment.", "start", "center", "end", "stretch")
	justify := enum("Main-axis distribution.", "start", "center", "end", "between", "around")
	press := action("Runs on enter or a click.")
	gap := intRange(0, 100, "Cells between children.")

	tml := blocksOf("tml", "document", "The document root.", attrMap{
		"version": {Type: TInt, Required: true, HasRange: true, Min: 1, Max: 1, Doc: "The TML version. Always 1."},
		"title":   {Type: TString, Doc: "The title on the frame."},
		"accent":  {Type: TAccent, Doc: "The accent: a role, or an ANSI slot such as slot:magenta."},
		"state":   {Type: TObject, Doc: "The JSON state store. In TML text, a <state> element."},
		"on-key":  action("Runs on a key press no node took. event.key names the key."),
	})
	tml.Block = false // the root only

	code := raw("code", "text", "Code, highlighted by lang. Also inline inside text.", attrMap{
		"lang":  {Type: TString, Doc: "The language for highlighting."},
		"lines": {Type: TBool, Doc: "Shows line numbers."},
		"from":  {Type: TInt, HasRange: true, Min: 1, Max: 1e9, Doc: "The first line number."},
	})
	code.Inline = true

	return []*Component{
		tml,
		// Layout
		blocksOf("row", "layout", "Lays children out left to right.", attrMap{"gap": gap, "align": align, "justify": justify, "on-press": press}),
		blocksOf("col", "layout", "Lays children out top to bottom.", attrMap{"gap": gap, "align": align, "justify": justify, "on-press": press}),
		blocksOf("grid", "layout", "Wraps children into equal columns.", attrMap{
			"cols": {Type: TInt, Required: true, HasRange: true, Min: 1, Max: 24, Doc: "The number of columns."}, "gap": gap}),
		blocksOf("stack", "layout", "Layers children on top of each other.", nil),
		void("spacer", "layout", "Takes the free space.", attrMap{}),
		blocksOf("scroll", "layout", "Scrolls its children.", attrMap{"follow": enum("Keeps the view at this end as content grows.", "start", "end")}),
		func() *Component {
			c := blocksOf("split", "layout", "Two children with a divider that a person can drag.", attrMap{
				"dir":   enum("h puts the children side by side, v stacks them.", "h", "v"),
				"ratio": {Type: TNumber, HasRange: true, Min: 0, Max: 1, Doc: "The share of the first child."},
			})
			c.MinChildren, c.MaxChildren = 2, 2
			return c
		}(),
		// Text
		inlineHolder("heading", "text", "A heading.", attrMap{"level": intRange(1, 3, "1 is the largest."), "role": role()}),
		inlineHolder("text", "text", "A paragraph of text with inline styling.", attrMap{
			"wrap":     enum("How long lines break.", "word", "char", "none"),
			"align":    enum("Horizontal alignment.", "left", "center", "right"),
			"role":     role(),
			"on-press": press,
		}),
		code,
		raw("markdown", "text", "Markdown text.", nil),
		raw("diff", "text", "A unified diff.", attrMap{
			"lang": {Type: TString, Doc: "The language for highlighting."}, "layout": enum("unified or split.", "unified", "split")}),
		// Inline
		inline("b", "Bold.", nil),
		inline("i", "Italic.", nil),
		inline("u", "Underline.", nil),
		inline("s", "Strikethrough.", nil),
		inline("c", "Colours its text with a role.", attrMap{"role": {Type: TRole, Bind: true, Required: true, Doc: "A theme role colour."}}),
		{Name: "kbd", Group: "inline", Doc: "A key name.", Attrs: attrMap{}, Content: ContentRaw, Inline: true},
		inline("a", "A link. It opens after the person confirms.", attrMap{"href": {Type: THref, Required: true, Doc: "A file: or https: link."}}),
		// Data
		{Name: "table", Group: "data", Doc: "Rows of a state array, sorted, filtered and scrolled locally.", Block: true,
			Content: ContentOnly, Children: []string{"column"}, MinChildren: 1, Attrs: attrMap{
				"rows":      data("The state array of rows."),
				"key":       field("The field that keeps a row's identity."),
				"select":    enum("How many rows a person can select.", "none", "single", "multi"),
				"sort":      {Type: TSort, Doc: "The first sort: field, field:asc or field:desc."},
				"filter":    {Type: TExpr, Doc: "A text that rows must contain."},
				"on-select": action("Runs when a row is selected. event.row is the row."),
				"on-key":    action("Runs on a key press. event.key names the key."),
			}},
		void("column", "data", "A column of a table.", attrMap{
			"field":  {Type: TField, Required: true, Doc: "The row field the column shows."},
			"title":  str("The header."),
			"align":  enum("Horizontal alignment.", "left", "center", "right"),
			"format": {Type: TExpr, Doc: "An expression of value and row that formats the cell."},
			"sort":   {Type: TBool, Doc: "A person can sort by this column."},
		}),
		{Name: "list", Group: "data", Doc: "Repeats its children once per element of a state array.", Block: true,
			Content: ContentBlocks, Attrs: attrMap{
				"items":     data("The state array."),
				"as":        ident("The name of the element inside the list. Default item."),
				"select":    enum("How many items a person can select.", "none", "single", "multi"),
				"on-select": action("Runs when an item is selected. event.item is the item."),
				"on-key":    action("Runs on a key press. event.key names the key."),
			}},
		void("tree", "data", "A tree of nested state objects.", attrMap{
			"items":     data("The state array of top nodes."),
			"label":     field("The field to show. Default name."),
			"children":  field("The field that holds the child array. Default children."),
			"open":      intRange(0, 32, "How many levels start open."),
			"select":    enum("How many nodes a person can select.", "none", "single", "multi"),
			"on-select": action("Runs when a node is selected. event.item is the node."),
		}),
		{Name: "kv", Group: "data", Doc: "Key and value pairs in two aligned columns.", Block: true,
			Content: ContentOnly, Children: []string{"item"}, MinChildren: 1, Attrs: attrMap{}},
		void("item", "data", "One pair of a kv.", attrMap{"k": reqStr("The key."), "v": str("The value."), "role": role()}),
		void("stat", "data", "A labelled number, the KPI tile.", attrMap{
			"label": reqStr("The label under the number."), "value": reqStr("The number."),
			"role": role(), "delta": str("A change to show beside the number, such as +12."), "on-press": press}),
		// Charts
		chart("bar", "A bar chart.", attrMap{"data": data("A state array of numbers or rows."), "x": field("The label field."), "y": field("The value field."),
			"dir": enum("v draws columns, h draws bars.", "v", "h")}),
		chart("line", "A line chart.", attrMap{"data": data("A state array of rows."), "x": field("The x field."), "y": field("The y field, or a comma list for several series."),
			"axis": boolean("Draws the axes.")}),
		chart("area", "An area chart.", attrMap{"data": data("A state array of rows or numbers."), "x": field("The x field."), "y": field("The y field.")}),
		chart("sparkline", "A one-row trend.", attrMap{"values": data("A state array of numbers.")}),
		chart("gauge", "A value against a maximum.", attrMap{"value": {Type: TNumber, Bind: true, Required: true, Doc: "The value."},
			"max": num("The maximum. Default 100."), "label": str("The label.")}),
		chart("heatmap", "A grid of values as shades.", attrMap{"data": data("A state array of rows."), "x": field("The column field."), "y": field("The row field."), "v": field("The value field.")}),
		chart("histogram", "Counts of values in bins.", attrMap{"values": data("A state array of numbers."), "bins": intRange(1, 200, "The number of bins.")}),
		chart("pie", "Shares of a whole.", attrMap{"data": data("A state array of rows."), "label": field("The label field."), "v": field("The value field."),
			"style": enum("pie or donut.", "pie", "donut")}),
		// Media
		void("image", "media", "An image. Without graphics it falls back to block art, then to alt.", attrMap{
			"src": {Type: TSrc, Required: true, Doc: "A file: path or a data:image URI."}, "alt": reqStr("The text shown when the image cannot be."),
			"graphics": graphicsAttr, "on-press": press}),
		raw("mermaid", "media", "A Mermaid diagram. Until the renderer lands, it shows the source.", nil),
		// Status
		void("progress", "status", "A progress bar.", attrMap{"value": {Type: TNumber, Bind: true, Required: true, Doc: "From 0 to 1."},
			"label": str("The label."), "role": role()}),
		void("spinner", "status", "A busy mark.", attrMap{"label": str("The label.")}),
		inlineHolder("badge", "status", "A short coloured tag.", attrMap{"role": role(), "on-press": press}),
		void("status", "status", "The tuios agent state glyph and word.", attrMap{
			"state": {Type: TEnum, Enum: []string{"working", "needs_input", "idle", "done", "errored", "unknown"}, Required: true, Bind: true, Doc: "The agent state."},
			"label": str("The label.")}),
		// Input
		blocksOf("form", "input", "Collects its fields into one value for submit.", attrMap{"on-submit": action("Runs on submit. form is the value."), "on-key": action("Runs on a key press. event.key names the key.")}),
		void("input", "input", "A one-line text field.", fieldAttrs(attrMap{
			"value": str("The first value."), "placeholder": str("Text shown while the field is empty."),
			"pattern": {Type: TPattern, Doc: "An RE2 regular expression the value must match."},
			"numeric": boolean("Takes a number only."),
			"min":     num("The smallest number, for type number."), "max": num("The largest number, for type number."),
		})),
		void("textarea", "input", "A multiline text field.", fieldAttrs(attrMap{
			"value": str("The first value."), "placeholder": str("Text shown while the field is empty."), "rows": intRange(1, 100, "The visible lines.")})),
		void("checkbox", "input", "A checkbox.", fieldAttrs(attrMap{"value": boolean("The first value.")})),
		void("select", "input", "Picks one value from a list.", fieldAttrs(attrMap{"options": data("A state array, or a literal array, of the choices."), "value": str("The first value.")})),
		void("toggle", "input", "An on and off switch.", fieldAttrs(attrMap{"value": boolean("The first value.")})),
		void("slider", "input", "A number on a track.", fieldAttrs(attrMap{"min": num("The smallest value."), "max": num("The largest value."),
			"step": num("The step."), "value": num("The first value.")})),
		inlineHolder("button", "input", "A button.", attrMap{
			"submit": {Type: TBool, Doc: "Submits the form it is in."}, "key": {Type: TKey, Doc: "The key that presses the button, such as enter or r."},
			"kind": enum("How much the button stands out.", "default", "primary", "danger"), "on-press": press, "disabled": boolean("The button does not take input.")}),
		// Containers
		{Name: "tabs", Group: "container", Doc: "Shows one tab at a time.", Block: true, Content: ContentOnly, Children: []string{"tab"}, MinChildren: 1, Attrs: attrMap{
			"value": {Type: TPath, Doc: "A state path that holds the open tab's id."}, "on-change": action("Runs when the open tab changes. event.value is its id.")}},
		blocksOf("tab", "container", "One tab of tabs.", attrMap{"id": reqIdent("The tab's id."), "title": reqStr("The tab's title.")}),
		blocksOf("card", "container", "A titled box, in the cockpit card style.", attrMap{
			"title": str("The title."), "meta": str("Text at the right of the title."), "fold": boolean("Starts folded."),
			"role": role(), "on-press": press}),
		blocksOf("modal", "container", "A box over the surface that holds focus while open.", attrMap{
			"title": str("The title."), "open": {Type: TBool, Bind: true, Required: true, Doc: "Shows the modal when true."}}),
		inlineHolder("toast", "container", "A short notice that goes away.", attrMap{"role": role(), "ttl": {Type: TDuration, Doc: "How long it shows, such as 5s."}}),
	}
}
