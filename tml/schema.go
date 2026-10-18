package tml

import (
	"bytes"
	"encoding/json"
	"strings"
)

//go:generate go test -run TestSchemaGolden -update .

// SchemaID is the $id of the generated JSON Schema.
const SchemaID = "https://tuios.dev/tml/tml.schema.json"

// Schema returns the JSON Schema (draft 2020-12) of the JSON encoding,
// generated from the catalog, so the schema and the validator cannot drift.
// An agent can use it for constrained decoding. The validator checks more
// than the schema can say: bindings, state paths, actions and limits.
func Schema() []byte {
	defs := map[string]any{
		"binding": map[string]any{
			"type": "string", "pattern": `\{.*\}`,
			"description": "A binding: an expression in braces, such as {state.count}.",
		},
		"role": map[string]any{"enum": roles, "description": "A theme role. Never a hex colour."},
	}
	var blocks, inlines []any
	inlines = append(inlines, map[string]any{"type": "string", "description": "A text run. It may hold {bindings}."})
	for i := range table {
		c := &table[i]
		defs[c.Name] = componentSchema(c)
		ref := map[string]any{"$ref": "#/$defs/" + c.Name}
		if c.Block {
			blocks = append(blocks, ref)
		}
		if c.Inline {
			inlines = append(inlines, ref)
		}
	}
	defs["block"] = map[string]any{"anyOf": blocks}
	defs["inline"] = map[string]any{"anyOf": inlines}
	root := map[string]any{
		"$schema":     "https://json-schema.org/draft/2020-12/schema",
		"$id":         SchemaID,
		"title":       "TML document",
		"description": "A TML document in its JSON encoding: a tml root, or one block component such as a dock cell's row. Each node has a type, its attributes beside it, and a children list in which a string is a text run.",
		"anyOf":       []any{map[string]any{"$ref": "#/$defs/tml"}, map[string]any{"$ref": "#/$defs/block"}},
		"$defs":       defs,
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	_ = enc.Encode(root)
	return buf.Bytes()
}

func componentSchema(c *Component) map[string]any {
	props := map[string]any{"type": map[string]any{"const": c.Name}}
	required := []string{"type"}
	for _, name := range c.AttrNames() {
		spec, _ := c.Spec(name)
		props[name] = attrSchema(spec)
		if spec.Required {
			required = append(required, name)
		}
	}
	switch c.Content {
	case ContentBlocks:
		props["children"] = childList(map[string]any{"$ref": "#/$defs/block"}, c)
	case ContentInline:
		props["children"] = childList(map[string]any{"$ref": "#/$defs/inline"}, c)
	case ContentRaw:
		props["children"] = map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "maxItems": 1,
			"description": "One text run, kept as written."}
	case ContentOnly:
		var refs []any
		for _, ch := range c.Children {
			refs = append(refs, map[string]any{"$ref": "#/$defs/" + ch})
		}
		props["children"] = childList(map[string]any{"anyOf": refs}, c)
	}
	return map[string]any{
		"type": "object", "description": c.Doc, "properties": props,
		"required": required, "additionalProperties": false,
	}
}

func childList(items map[string]any, c *Component) map[string]any {
	s := map[string]any{"type": "array", "items": items}
	if c.MinChildren > 0 {
		s["minItems"] = c.MinChildren
	}
	if c.MaxChildren > 0 {
		s["maxItems"] = c.MaxChildren
	}
	return s
}

func withBinding(s map[string]any, spec AttrSpec) map[string]any {
	if !spec.Bind {
		s["description"] = spec.Doc
		return s
	}
	return map[string]any{"anyOf": []any{s, map[string]any{"$ref": "#/$defs/binding"}}, "description": spec.Doc}
}

func attrSchema(spec AttrSpec) map[string]any {
	str := func(pattern string) map[string]any {
		s := map[string]any{"type": "string"}
		if pattern != "" {
			s["pattern"] = pattern
		}
		return s
	}
	switch spec.Type {
	case TInt, TNumber:
		t := "number"
		if spec.Type == TInt {
			t = "integer"
		}
		s := map[string]any{"type": t}
		if spec.HasRange {
			s["minimum"], s["maximum"] = spec.Min, spec.Max
		}
		return withBinding(s, spec)
	case TBool:
		return withBinding(map[string]any{"type": "boolean"}, spec)
	case TEnum:
		return withBinding(map[string]any{"enum": spec.Enum}, spec)
	case TRole:
		return withBinding(map[string]any{"$ref": "#/$defs/role"}, spec)
	case TAccent:
		return withBinding(map[string]any{"anyOf": []any{
			map[string]any{"$ref": "#/$defs/role"},
			str("^slot:(" + strings.Join(slots[:], "|") + ")$"),
		}}, spec)
	case TSize:
		return withBinding(map[string]any{"anyOf": []any{
			map[string]any{"type": "integer", "minimum": 0},
			str(`^([0-9]+|[0-9]+%|fill|fill:[0-9]+)$`),
		}}, spec)
	case TPad:
		return withBinding(map[string]any{"anyOf": []any{
			map[string]any{"type": "integer", "minimum": 0},
			str(`^[0-9]+( [0-9]+)?( [0-9]+ [0-9]+)?$`),
		}}, spec)
	case TData:
		return withBinding(map[string]any{"anyOf": []any{
			str(`^[A-Za-z_][A-Za-z0-9_]*(\.[A-Za-z_][A-Za-z0-9_]*|\[[0-9]+\])*$`),
			map[string]any{"type": "array"},
		}}, spec)
	case TPath:
		return withBinding(str(`^state(\.[A-Za-z_][A-Za-z0-9_]*|\[[0-9]+\])+$`), spec)
	case TDuration:
		return withBinding(str(`^[0-9]+(ms|s|m|h)$`), spec)
	case THref:
		return withBinding(str(`^(file:|https://)`), spec)
	case TSrc:
		return withBinding(str(`^(file:|data:image/(png|jpeg|gif|webp);base64,)`), spec)
	case TIdent:
		return withBinding(str(`^[A-Za-z_][A-Za-z0-9_-]*$`), spec)
	case TSort:
		return withBinding(str(`^[^:]+(:(asc|desc))?$`), spec)
	case TObject:
		return withBinding(map[string]any{"type": "object"}, spec)
	default:
		return withBinding(str(""), spec)
	}
}
