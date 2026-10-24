// Package tmlref writes the component list of the tml skill topic from the
// TML catalog, so the topic an agent reads cannot drift from what the
// validator accepts.
//
// skills/gentml.go splices Section into skills/tuios/tml.md between the
// Begin and End markers, and TestTMLTopicMatchesCatalog fails when the file
// and the catalog differ.
package tmlref

import (
	"bytes"
	"errors"
	"slices"
	"strings"

	"tuios.dev/tml"
)

// The markers around the generated region of tml.md.
const (
	Begin = "<!-- BEGIN generated from tml/catalog.go: go generate ./skills -->\n"
	End   = "<!-- END generated -->\n"
)

// groupTitles names each catalog group in the order the topic lists them.
// A group the catalog gains later still prints, under its own name, after
// these.
var groupTitles = []struct{ group, title string }{
	{"document", "Root"},
	{"layout", "Layout"},
	{"text", "Text"},
	{"inline", "Inside text"},
	{"data", "Data"},
	{"chart", "Charts"},
	{"media", "Media"},
	{"status", "Status"},
	{"input", "Input"},
	{"container", "Containers"},
}

// Section returns the generated region, without the markers.
func Section() string {
	var b strings.Builder
	b.WriteString("Each line gives a component, its attributes and what it holds.\n")
	b.WriteString("`*` marks a required attribute. `a|b` lists the accepted values.\n")
	b.WriteString("`=path` marks a data attribute. It takes a state path such as `state.rows`, with no braces.\n")
	b.WriteString("Every component also takes ")
	for i, g := range tml.GlobalAttrs() {
		if i > 0 {
			b.WriteString(" ")
		}
		b.WriteString(g.Name)
	}
	b.WriteString(".\n")

	comps := tml.Components()
	fields := shared(comps, func(c tml.Component) bool { return slices.Contains(tokens(c), "name*") })
	fields = slices.DeleteFunc(fields, func(t string) bool { return t == "name*" })
	if len(fields) > 0 {
		b.WriteString("A field is a component with `name*`. Every field also takes " + strings.Join(fields, " ") + ".\n")
	}

	var order []string
	titles := map[string]string{}
	for _, g := range groupTitles {
		order = append(order, g.group)
		titles[g.group] = g.title
	}
	for _, c := range comps {
		if _, ok := titles[c.Group]; !ok {
			titles[c.Group] = c.Group
			order = append(order, c.Group)
		}
	}
	for _, group := range order {
		inGroup := func(c tml.Component) bool { return c.Group == group }
		var common []string
		if n := len(slices.DeleteFunc(slices.Clone(comps), func(c tml.Component) bool { return !inGroup(c) })); n == 0 {
			continue
		} else if n >= 3 {
			common = shared(comps, inGroup)
		}
		b.WriteString("\n" + titles[group])
		if len(common) > 0 {
			b.WriteString(". Each also takes " + strings.Join(common, " "))
		}
		b.WriteString(":\n\n")
		// Components with the same attributes and content share a line.
		var names, rests []string
		for _, c := range comps {
			if !inGroup(c) {
				continue
			}
			isField := slices.Contains(tokens(c), "name*")
			var rest strings.Builder
			for _, t := range tokens(c) {
				if slices.Contains(common, t) || (isField && slices.Contains(fields, t)) {
					continue
				}
				rest.WriteString(" " + t)
			}
			if h := holds(c); h != "" {
				rest.WriteString(": holds " + h)
			}
			if i := slices.Index(rests, rest.String()); i >= 0 {
				names[i] += ", `" + c.Name + "`"
				continue
			}
			names = append(names, "`"+c.Name+"`")
			rests = append(rests, rest.String())
		}
		for i := range names {
			b.WriteString("- " + names[i] + rests[i] + "\n")
		}
	}
	return b.String()
}

// tokens renders each own attribute of c: its name, * when required, and
// the accepted values or =path.
func tokens(c tml.Component) []string {
	out := make([]string, 0, len(c.Attrs))
	for _, a := range c.Attrs {
		t := a.Name
		if a.Required {
			t += "*"
		}
		switch a.Type {
		case tml.TEnum:
			t += "=" + strings.Join(a.Enum, "|")
		case tml.TData, tml.TPath:
			t += "=path"
		}
		out = append(out, t)
	}
	return out
}

// shared returns the attribute tokens every component that keep selects has,
// in the order of the first such component.
func shared(comps []tml.Component, keep func(tml.Component) bool) []string {
	var out []string
	first := true
	for _, c := range comps {
		if !keep(c) {
			continue
		}
		ts := tokens(c)
		if first {
			out, first = ts, false
			continue
		}
		out = slices.DeleteFunc(out, func(t string) bool { return !slices.Contains(ts, t) })
	}
	return out
}

// holds says what a component may contain, or "" when it holds nothing.
func holds(c tml.Component) string {
	switch c.Content {
	case tml.ContentBlocks:
		if c.MinChildren == 2 && c.MaxChildren == 2 {
			return "two components"
		}
		return "components"
	case tml.ContentInline:
		return "text"
	case tml.ContentRaw:
		return "raw text"
	case tml.ContentOnly:
		return "`" + strings.Join(c.Children, "`, `") + "`"
	}
	return ""
}

// Splice returns doc with the region between the markers replaced by
// Section. It fails when a marker is missing or out of order.
func Splice(doc []byte) ([]byte, error) {
	start := bytes.Index(doc, []byte(Begin))
	end := bytes.Index(doc, []byte(End))
	if start < 0 || end < 0 || end < start {
		return nil, errors.New("tml.md has no generated region: add the BEGIN and END markers")
	}
	var out bytes.Buffer
	out.Write(doc[:start+len(Begin)])
	out.WriteString(Section())
	out.Write(doc[end:])
	return out.Bytes(), nil
}
