# tml

[![Go Reference](https://pkg.go.dev/badge/tuios.dev/tml.svg)](https://pkg.go.dev/tuios.dev/tml)

`tuios.dev/tml` is the TML language: a markup for terminal surfaces. A
program, a plugin or an agent sends a TML document to a host such as tuios.
The host draws it in a pane, a popup, a dock cell or a cockpit card. A key
press or a click goes back to the owner as an event, and the owner answers
with a state patch.

The package is the language only. It draws nothing. It has:

- The tree: a node type for each component of a fixed catalog.
- Two encodings: TML text, a strict XML subset, and TML JSON. A parser and a
  printer for each. Both parsers give the same canonical tree, and a
  canonical tree prints and reads back unchanged in either encoding.
- The validator. It checks the full catalog schema, and its errors name the
  fix.
- The JSON Schema of TML JSON, generated from the catalog. Agents and MCP
  tools use it.
- The state store, `{state.path}` bindings, and a small expression language
  with a step budget. Each expression records the state paths it reads.
- The action parser, which checks the action attributes.
- RFC 6902 JSON Patch, applied to the state, with the size limit checked
  after every operation.

The renderer, view state and dirty tracking are in tuios.dev/loom.

The package uses only the standard library. No code from a document runs:
expressions compute values, and cannot loop, assign or call out.

## Install

```sh
go get tuios.dev/tml
```

The go command reads the import path from tuios.dev. Go 1.25 or later can
fetch it directly. With the default module proxy, any Go version can fetch it.

## Example

```xml
<tml version="1" title="Build">
  <state>{"pct": 40, "step": "Compiling"}</state>
  <card title="Build">
    <text>Step: <b>{state.step}</b></text>
    <progress value="{state.pct / 100}"/>
  </card>
</tml>
```

The same document in TML JSON:

```json
{"type": "tml", "version": 1, "title": "Build", "state": {"pct": 40, "step": "Compiling"},
 "children": [{"type": "card", "title": "Build", "children": [
   {"type": "text", "children": ["Step: ", {"type": "b", "children": ["{state.step}"]}]},
   {"type": "progress", "value": "{state.pct / 100}"}]}]}
```

Check a document:

```go
_, problems, err := tml.Check(src, nil)
if err != nil {
	for _, p := range problems {
		fmt.Println(p)
	}
}
```

It prints:

```
3:3: <chart-bar> is not a TML component. (unknown_component at /children/0) Did you mean "bar"?
```

Apply a patch from the owner, then evaluate a binding:

```go
doc, _, err := tml.Check(src, nil)
if err != nil {
	panic(err)
}
store, err := tml.StoreFor(doc, nil)
if err != nil {
	panic(err)
}

// The owner sends a patch. Apply returns the state paths it changed.
ops, err := tml.DecodePatch([]byte(`[{"op":"replace","path":"/pct","value":80}]`), nil)
if err != nil {
	panic(err)
}
changed, err := store.Apply(ops)
if err != nil {
	panic(err)
}
fmt.Println(changed)

// A renderer evaluates the progress bar's binding with a budget.
bar := doc.At("/children/0/children/1")
v, _ := bar.Attr("value")
tmpl, err := tml.ParseTemplate(v.Text())
if err != nil {
	panic(err)
}
value, err := tmpl.Eval(&tml.Env{Vars: map[string]any{"state": store.State()}, Budget: tml.NewBudget()})
fmt.Println(value, err)
```

It prints `[/pct]`, then `0.8 <nil>`. The code is the body of `Example` in
`example_test.go`, and a test keeps the two the same.

## Notes for a host

- **Share one `Budget` per frame.** Set `Env.Budget` to one `NewBudget()` for
  all the bindings of a surface's frame. A nil `Budget` gets a fresh budget
  for each call, which bounds one binding but not a frame.
- **The store sanitizes state.** `Node.State`, `NewStore`, patch values and
  `Store.Set` pass every string and key through `Sanitize`. `Expr.Eval` and
  `Template.Eval` sanitize what they return. A renderer that reads the store
  directly, such as for table rows, gets clean strings.
- **Use `StoreFor` for a document's state.** The store keeps the tree, and
  `Apply` refuses a patch that grows `each`, `list`, the images or the rows
  past their limits. The validator checks the same counts when a document
  arrives.

## Syntax

A document is a tree of nodes. A node has a type, attributes and children.

- **TML text** is for files, people and the playground. It is a strict XML
  subset. It has no DTD, no entities other than `&lt;` `&gt;` `&amp;` `&quot;`
  `&apos;` and numeric references such as `&#10;`, no namespaces, no CDATA and
  no processing instructions. The parser refuses each of them. Comments are
  allowed, and the parser drops them.
- **TML JSON** is for the wire and for agents. `type` names the node. The
  attributes sit beside it. `children` is a list, and a string in it is a text
  run.

The rules that hold in both:

- An attribute value is a string, a number or a boolean. The catalog gives
  each attribute a type, and both parsers read the value to that type. So
  `version="1"` in TML text and `"version": 1` in JSON are the same tree.
- A value with `{...}` is a binding: `value="{state.pct / 100}"`. Write `{{`
  and `}}` for literal braces.
- `<state>` holds the state store as JSON. It must be the first child of the
  `tml` root. In JSON it is the `state` attribute of the root.
- `id` names a node for patches, focus and events. `key` keeps identity in
  repeated children.
- `each="state.items" as="item"` repeats a node once per array element.
- `if="expr"` drops a node when false. `show="expr"` hides it and keeps its
  space.
- A data attribute such as `rows`, `data`, `values`, `items` or `options`
  takes a state path without braces, such as `state.items`, or a literal
  array, such as `[2, 5, 3]`.
- The root is `tml`, or one block component for a small surface such as a
  dock cell.

White space in TML text follows these rules. `tml fmt` prints a document so
that it reads back to the same tree.

- In text content, such as `text`, `heading`, `button` and the inline
  components, each run of white space becomes one space. The ends of a block
  are trimmed. Write `&#32;` for a space the parser must keep, and `&#10;` for
  a line break.
- Between block components, white space does not count.
- Raw content keeps its text as written: `code`, `markdown`, `diff`,
  `mermaid`, `kbd` and `state`. Bindings do not apply in raw content.

`Sanitize` removes what must never reach a terminal:

- C0 and C1 control characters other than tab and newline, and DEL.
- The bidirectional marks, embeddings, overrides and isolates.
- The line and paragraph separators U+2028 and U+2029.
- Invisible characters: U+200B, U+2060 to U+2064, U+180E, U+FEFF and the
  tag characters.

It keeps the zero-width joiner and non-joiner, which emoji and several
scripts need. The parsers apply it to every text run and attribute. A one-line
text attribute, such as `title`, also has its tabs and line breaks turned
into spaces.

## Components

A tag or an attribute outside this catalog is an error with a suggestion. The
parser keeps an unknown component in the tree, and the validator reports it.

#### Document

| Component | What it is | Attributes (required in bold) | Holds |
| --- | --- | --- | --- |
| `tml` | The document root. | `accent` `on-key` `state` `title` **`version`** | block components |

#### Layout

| Component | What it is | Attributes (required in bold) | Holds |
| --- | --- | --- | --- |
| `col` | Lays children out top to bottom. | `align` `gap` `justify` `on-press` | block components |
| `grid` | Wraps children into equal columns. | **`cols`** `gap` | block components |
| `row` | Lays children out left to right. | `align` `gap` `justify` `on-press` | block components |
| `scroll` | Scrolls its children. | `follow` | block components |
| `spacer` | Takes the free space. | none | nothing |
| `split` | Two children with a divider that a person can drag. | `dir` `ratio` | two block components |
| `stack` | Layers children on top of each other. | none | block components |

#### Text

| Component | What it is | Attributes (required in bold) | Holds |
| --- | --- | --- | --- |
| `code` | Code, highlighted by lang. Also inline inside text. | `from` `lang` `lines` | text, kept as written |
| `diff` | A unified diff. | `lang` `layout` | text, kept as written |
| `heading` | A heading. | `level` `role` | text and inline components |
| `markdown` | Markdown text. | none | text, kept as written |
| `text` | A paragraph of text with inline styling. | `align` `on-press` `role` `wrap` | text and inline components |

#### Inline

| Component | What it is | Attributes (required in bold) | Holds |
| --- | --- | --- | --- |
| `a` | A link. It opens after the person confirms. | **`href`** | text and inline components |
| `b` | Bold. | none | text and inline components |
| `c` | Colours its text with a role. | **`role`** | text and inline components |
| `i` | Italic. | none | text and inline components |
| `kbd` | A key name. | none | text, kept as written |
| `s` | Strikethrough. | none | text and inline components |
| `u` | Underline. | none | text and inline components |

#### Data

| Component | What it is | Attributes (required in bold) | Holds |
| --- | --- | --- | --- |
| `column` | A column of a table. | `align` **`field`** `format` `sort` `title` | nothing |
| `item` | One pair of a kv. | **`k`** `role` `v` | nothing |
| `kv` | Key and value pairs in two aligned columns. | none | `item` only |
| `list` | Repeats its children once per element of a state array. | `as` **`items`** `on-key` `on-select` `select` | block components |
| `stat` | A labelled number, the KPI tile. | `delta` **`label`** `on-press` `role` **`value`** | nothing |
| `table` | Rows of a state array, sorted, filtered and scrolled locally. | `filter` `key` `on-key` `on-select` **`rows`** `select` `sort` | `column` only |
| `tree` | A tree of nested state objects. | `children` **`items`** `label` `on-select` `open` `select` | nothing |

#### Chart

| Component | What it is | Attributes (required in bold) | Holds |
| --- | --- | --- | --- |
| `area` | An area chart. | **`data`** `graphics` `role` `title` `x` `y` | nothing |
| `bar` | A bar chart. | **`data`** `dir` `graphics` `role` `title` `x` `y` | nothing |
| `gauge` | A value against a maximum. | `graphics` `label` `max` `role` `title` **`value`** | nothing |
| `heatmap` | A grid of values as shades. | **`data`** `graphics` `role` `title` `v` `x` `y` | nothing |
| `histogram` | Counts of values in bins. | `bins` `graphics` `role` `title` **`values`** | nothing |
| `line` | A line chart. | `axis` **`data`** `graphics` `role` `title` `x` `y` | nothing |
| `pie` | Shares of a whole. | **`data`** `graphics` `label` `role` `style` `title` `v` | nothing |
| `sparkline` | A one-row trend. | `graphics` `role` `title` **`values`** | nothing |

#### Media

| Component | What it is | Attributes (required in bold) | Holds |
| --- | --- | --- | --- |
| `image` | An image. Without graphics it falls back to block art, then to alt. | **`alt`** `graphics` `on-press` **`src`** | nothing |
| `mermaid` | A Mermaid diagram. Until the renderer lands, it shows the source. | none | text, kept as written |

#### Status

| Component | What it is | Attributes (required in bold) | Holds |
| --- | --- | --- | --- |
| `badge` | A short coloured tag. | `on-press` `role` | text and inline components |
| `progress` | A progress bar. | `label` `role` **`value`** | nothing |
| `spinner` | A busy mark. | `label` | nothing |
| `status` | The tuios agent state glyph and word. | `label` **`state`** | nothing |

#### Input

| Component | What it is | Attributes (required in bold) | Holds |
| --- | --- | --- | --- |
| `button` | A button. | `disabled` `key` `kind` `on-press` `submit` | text and inline components |
| `checkbox` | A checkbox. | `bind` `disabled` `label` **`name`** `on-change` `required` `value` | nothing |
| `form` | Collects its fields into one value for submit. | `on-key` `on-submit` | block components |
| `input` | A one-line text field. | `bind` `disabled` `label` `max` `min` **`name`** `numeric` `on-change` `pattern` `placeholder` `required` `value` | nothing |
| `select` | Picks one value from a list. | `bind` `disabled` `label` **`name`** `on-change` **`options`** `required` `value` | nothing |
| `slider` | A number on a track. | `bind` `disabled` `label` `max` `min` **`name`** `on-change` `required` `step` `value` | nothing |
| `textarea` | A multiline text field. | `bind` `disabled` `label` **`name`** `on-change` `placeholder` `required` `rows` `value` | nothing |
| `toggle` | An on and off switch. | `bind` `disabled` `label` **`name`** `on-change` `required` `value` | nothing |

#### Container

| Component | What it is | Attributes (required in bold) | Holds |
| --- | --- | --- | --- |
| `card` | A titled box, in the cockpit card style. | `fold` `meta` `on-press` `role` `title` | block components |
| `modal` | A box over the surface that holds focus while open. | **`open`** `title` | block components |
| `tab` | One tab of tabs. | **`id`** **`title`** | block components |
| `tabs` | Shows one tab at a time. | `on-change` `value` | `tab` only |
| `toast` | A short notice that goes away. | `role` `ttl` | text and inline components |

Every component also takes these attributes:

| Attribute | Meaning |
| --- | --- |
| `id` | Names the node for patches, focus and events. |
| `key` | Keeps identity in repeated children. |
| `each`, `as` | Repeats the node once per element of a state array. |
| `if` | Drops the node when false. |
| `show` | Hides the node when false and keeps its space. |
| `w`, `h` | Width and height: cells (`12`), a percentage (`50%`), `fill` or `fill:2`. |
| `min-w`, `max-w`, `min-h`, `max-h` | Size bounds. |
| `pad` | Padding: one, two or four cell counts. |
| `grow` | Share of free space. |

### Theme roles

A colour is a theme role, never a hex value. The host picks the colour from
its theme, at the colour depth of the terminal. A hex value is an error that
lists the roles.

`fg` `dim` `mute` `accent` `accent-bright` `success` `warning` `error` `info`
`attention` `busy` `edge` `canvas` `panel` `surface` `card` `accent-tint`
`success-tint` `warning-tint` `error-tint` `series1` to `series6`

The root's `accent` can also name an ANSI slot, such as `slot:magenta`.

## Expressions

A binding holds one expression. The language is small and total: every
expression gives a value, and a mismatch gives `null`.

- Literals: numbers, strings in single or double quotes, `true`, `false`,
  `null`, arrays and objects.
- Paths: `state.a.b`, `state.items[0]`, `item.x` inside `each`, `form.tag`
  inside a form, `event.value` inside an action, `value` and `row` in a column
  format, and `view.width`.
- Operators: `+ - * / %`, `== != < <= > >=`, `&& || !`, `a ? b : c`, and `??`
  for null.
- No loops, user functions, assignment or regular expressions.

| Function | Result |
| --- | --- |
| `len(x)` | The length of a string, an array or an object. |
| `sum(xs)`, `avg(xs)`, `min(xs)`, `max(xs)` | Over an array, or `sum(rows, 'field')`, or over several numbers. |
| `round(x, digits)`, `abs(x)` | Rounding and absolute value. |
| `pct(part, whole)` | `part / whole * 100`. `pct(x)` is `x * 100`. |
| `fmt(x, digits)`, also `format` | A number with thousands separators: `1,234.5`. |
| `dur(x, unit)` | A duration from `ns`, `us`, `ms` (the default), `s`, `m` or `h`: `840ms`, `2.21s`, `4m 5s`. |
| `bytes(x)` | A byte count: `512B`, `3.1G`. |
| `ago(t)` | How long ago a Unix time or RFC 3339 time was: `3m ago`. |
| `upper(s)`, `lower(s)`, `trim(s)` | Case and spaces. |
| `join(xs, sep)` | The elements as text, joined. |
| `contains(a, b)`, `starts(s, p)` | Substring, element and prefix tests. |
| `pick(rows, 'field')` | One field of each row. |
| `count(rows, 'field', value)` | The rows whose field equals value. |

Each surface has a budget of 10,000 steps per frame. A node costs one step,
and a function costs one step per element it reads. Past the budget, a
binding renders `!`.

## Actions

An action attribute, such as `on-press` or `on-submit`, holds one or more
calls separated by `;`.

| Action | Runs | Effect |
| --- | --- | --- |
| `set('path', expr)` | Locally | Writes document state. |
| `toggle('path')` | Locally | Flips a boolean. |
| `tab('id')` | Locally | Opens a tab. With `tabs value="state.tab"` it writes the state. |
| `sort('field')`, `sort('table', 'field')` | Locally | Sorts a table. The same field again flips the order. |
| `filter('id', expr)` | Locally | Filters a table or a list. |
| `fold('id')`, `focus('id')` | Locally | Folds a card, moves focus. |
| `emit('name', expr)` | Owner | Sends an event. |
| `close(expr)` | Owner | Closes the surface with a value. |
| `copy(expr)` | Host | Puts text on the clipboard. |
| `open('file:path')` | Host | Opens a file under the owner's files folder. |
| `pane('verb', {...})` | Host | Runs `focus`, `new-window` or `send-text` under the owner's grants. |

`ParseActions` parses and checks an action attribute. The renderer runs the
local actions and sends the rest to the owner or the host.

## Errors that name the fix

`Validate` and `Check` report up to 20 problems in one pass. Each problem has
a JSON Pointer into the JSON form, a line and column, a code, and the closest
legal value. Warnings alone do not stop a document.

```json
{"error": {"code": "invalid_tml", "message": "1 problem in the document", "problems": [
  {"at": "/children/0/children/2/sortBy", "line": 5, "col": 48, "code": "unknown_attribute",
   "message": "table has no attribute 'sortBy'.", "severity": "error", "did_you_mean": "sort",
   "accepted": ["filter", "key", "on-key", "on-select", "rows", "select", "sort", "..."]}]}}
```

The codes are `syntax`, `unsupported_xml`, `unknown_component`,
`unknown_attribute`, `missing_attribute`, `bad_value`, `hex_color`,
`bad_child`, `text_not_allowed`, `child_count`, `bad_expression`,
`unknown_function`, `unknown_path_root`, `binding_missing` (a warning),
`bad_action`, `duplicate_id`, `misplaced_state`, `password_refused`, `limit`
and `bad_root`.

## Limits

| Limit | Value |
| --- | --- |
| Document | 256 KiB, 1 MiB from the person's own command line |
| Nodes | 5,000 |
| Depth | 32 |
| State | 1 MiB |
| Nodes that `each` and `list` expand to | 2,000 |
| Rows and data points of tables, lists, trees and charts, after `each` expands | 50,000 |
| Patch | 64 KiB, 100 a second per surface |
| Images | 4 per document after `each` expands, 4 MiB each |
| Problems in one report | 20 |

Over a limit is a problem with the code `limit`. The report keeps its last
slot for an error, so warnings cannot hide one.

## Validate, format and describe

The standalone `tml` command will live in `tuios.dev/loom/cmd/tml`. Until
then, these functions do its work:

| Function | Does |
| --- | --- |
| `Check(src, opts)` | Parses and validates a document in either encoding. |
| `Format(src, to, opts)` | Prints a document in canonical TML text or TML JSON. |
| `Schema()` | Returns the JSON Schema of TML JSON. |

The JSON Schema is also in this folder as
[tml.schema.json](tml.schema.json). `go generate` writes it again after a
catalog change, and a test fails while the two differ.

## Tests

- Every example of the design is a fixture under `testdata/examples`. Each one
  validates, prints to its golden TML text and JSON, and each golden reads
  back to the other.
- Each broken document under `testdata/errors` has a golden list of its
  problems.
- `FuzzParseXML`, `FuzzParseJSON`, `FuzzExpr` and `FuzzPatch` run their seeds
  in the ordinary suite. The parser targets assert the limits, the character
  filter, and that every parsed tree prints and reads back to itself through
  either encoding.
- `review_test.go` holds the inputs of the wave 1 security review: patch
  growth, escapes in state, the problem cap, nested `each`, step charging,
  quadratic parsing, and the medium findings.

```sh
go test ./...
go test -run XXX -fuzz FuzzParseXML -fuzztime 5m
```

## Versions

The module is at v0. The API can change between minor versions. Its tags have
the folder name as a prefix, such as `tml/v0.1.0`. The source is in the
[tml folder](https://github.com/Gaurav-Gosain/tuios/tree/main/tml) of the
tuios repository, under the MIT licence.
