package config

import (
	"fmt"
	"maps"
	"reflect"
	"slices"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

// Editing a TOML file line by line.
//
// A save changes a few keys. Rewriting the whole file for that would drop
// every comment and every blank line the person put there, and in an included
// file that is somebody's hand-written work. So a change is made to the lines
// that hold the key: a value is replaced where it stands, a new key goes under
// the header of its table, a new table goes at the end, and a removed key takes
// its lines with it.
//
// The editor knows tables, arrays of tables, dotted keys and values that run
// over several lines. It does not know everything TOML allows, such as a table
// defined by a dotted key and then given a header. So every edit is checked:
// the edited text is parsed, and it has to say exactly what the change asked
// for. When it does not, the caller writes the file again from the parsed
// values, which is correct and loses the comments.

// tomlEdit is a file being edited.
type tomlEdit struct {
	lines []string
}

// docHeader is one table header in a file.
type docHeader struct {
	line  int
	path  []string
	array bool
}

// docKey is one key and the lines its value takes.
type docKey struct {
	start, end int
	// path is the full path: the table's path and then the dotted key.
	path []string
	// header is the index of the header the key is under, -1 for the root.
	header int
	// inArray is true for a key under an [[array]] header.
	inArray bool
	// keyText is the line up to the =, indentation and key spelling kept.
	keyText string
	// comment is the trailing comment of a one-line value, with its #.
	comment string
}

// docScan is what scan found.
type docScan struct {
	headers []docHeader
	keys    []docKey
}

func newTOMLEdit(data []byte) *tomlEdit {
	return &tomlEdit{lines: splitLines(string(data))}
}

func (e *tomlEdit) bytes() []byte { return []byte(joinLines(e.lines)) }

// scan finds every header and key.
func (e *tomlEdit) scan() docScan {
	var d docScan
	cur := -1
	var table []string
	inArray := false
	for i := 0; i < len(e.lines); i++ {
		line := e.lines[i]
		s := strings.TrimSpace(line)
		if s == "" || strings.HasPrefix(s, "#") {
			continue
		}
		if path, array, ok := parseHeaderLine(s); ok {
			d.headers = append(d.headers, docHeader{line: i, path: path, array: array})
			cur, table, inArray = len(d.headers)-1, path, array
			continue
		}
		k, rest, ok := cutOutsideQuotes(line, '=')
		if !ok {
			continue
		}
		var st valueScan
		commentAt := st.feed(rest)
		end := i + 1
		for !st.done() && end < len(e.lines) {
			st.feed(e.lines[end])
			end++
		}
		dk := docKey{
			start: i, end: end, header: cur, inArray: inArray,
			path:    append(slices.Clone(table), splitDotted(k)...),
			keyText: strings.TrimRight(k, " \t"),
		}
		if end == i+1 && commentAt >= 0 {
			dk.comment = strings.TrimSpace(rest[commentAt:])
		}
		d.keys = append(d.keys, dk)
		i = end - 1
	}
	return d
}

// parseHeaderLine reads a [table] or [[array]] header.
func parseHeaderLine(s string) ([]string, bool, bool) {
	if !strings.HasPrefix(s, "[") {
		return nil, false, false
	}
	array := strings.HasPrefix(s, "[[")
	inner := strings.TrimPrefix(s, "[")
	if array {
		inner = strings.TrimPrefix(inner, "[")
	}
	body, rest, ok := cutOutsideQuotes(inner, ']')
	if !ok {
		return nil, false, false
	}
	if array {
		if !strings.HasPrefix(rest, "]") {
			return nil, false, false
		}
		rest = rest[1:]
	}
	rest = strings.TrimSpace(rest)
	if rest != "" && !strings.HasPrefix(rest, "#") {
		return nil, false, false
	}
	return splitDotted(body), array, true
}

// valueScan follows a value over lines: open brackets and multi-line strings.
type valueScan struct {
	depth int
	ml    string
}

func (v *valueScan) done() bool { return v.depth <= 0 && v.ml == "" }

// feed reads one line of a value. It returns where a comment starts, or -1.
func (v *valueScan) feed(s string) int {
	for i := 0; i < len(s); i++ {
		if v.ml != "" {
			j := strings.Index(s[i:], v.ml)
			if j < 0 {
				return -1
			}
			i += j + len(v.ml) - 1
			v.ml = ""
			continue
		}
		switch c := s[i]; c {
		case '#':
			return i
		case '"', '\'':
			q := string([]byte{c, c, c})
			if strings.HasPrefix(s[i:], q) {
				v.ml = q
				i += 2
				continue
			}
			for i++; i < len(s); i++ {
				if c == '"' && s[i] == '\\' {
					i++
					continue
				}
				if s[i] == c {
					break
				}
			}
		case '[', '{':
			v.depth++
		case ']', '}':
			v.depth--
		}
	}
	return -1
}

// blockEnd is the line after the last line of the table that header h
// starts. For an [[array]] entry the block takes in the sub-tables of that
// entry.
func (d docScan) blockEnd(e *tomlEdit, h int) int {
	hd := d.headers[h]
	for _, next := range d.headers[h+1:] {
		if hd.array && !next.array && len(next.path) > len(hd.path) && slices.Equal(next.path[:len(hd.path)], hd.path) {
			continue
		}
		return next.line
	}
	return len(e.lines)
}

// lastContent is the line after the last line in [from, to) that is not
// blank, or from when there is none.
func (e *tomlEdit) lastContent(from, to int) int {
	for i := to; i > from; i-- {
		if strings.TrimSpace(e.lines[i-1]) != "" {
			return i
		}
	}
	return from
}

func (e *tomlEdit) splice(from, to int, repl []string) {
	e.lines = slices.Concat(e.lines[:from], repl, e.lines[to:])
}

// setLeaf sets a key whose value is not a table.
func (e *tomlEdit) setLeaf(path []string, v any) {
	d := e.scan()
	for _, k := range d.keys {
		if k.inArray || !slices.Equal(k.path, path) {
			continue
		}
		line := k.keyText + " = " + encodeValue(v)
		if k.comment != "" {
			line += " " + k.comment
		}
		e.splice(k.start, k.end, []string{line})
		return
	}
	parent, key := path[:len(path)-1], path[len(path)-1]
	line := tomlKey(key) + " = " + encodeValue(v)
	if len(parent) == 0 {
		at := 0
		for _, k := range d.keys {
			if k.header == -1 {
				at = k.end
			}
		}
		if at == 0 && len(d.headers) > 0 {
			e.splice(d.headers[0].line, d.headers[0].line, []string{line, ""})
			return
		}
		if at == 0 {
			at = e.lastContent(0, len(e.lines))
		}
		e.splice(at, at, []string{line})
		return
	}
	for h, hd := range d.headers {
		if hd.array || !slices.Equal(hd.path, parent) {
			continue
		}
		at := hd.line + 1
		for _, k := range d.keys {
			if k.header == h {
				at = k.end
			}
		}
		e.splice(at, at, []string{line})
		return
	}
	e.appendBlock(append([]string{"[" + dottedKey(parent) + "]"}, line))
}

// setTable puts a whole table at path, replacing what is there.
func (e *tomlEdit) setTable(path []string, m map[string]any) {
	e.remove(path)
	e.appendBlock(renderTable(path, m))
}

// appendBlock adds lines at the end, after one blank line.
func (e *tomlEdit) appendBlock(block []string) {
	end := e.lastContent(0, len(e.lines))
	e.lines = e.lines[:end]
	if end > 0 {
		e.lines = append(e.lines, "")
	}
	e.lines = append(e.lines, block...)
}

// remove deletes the key or table at path, and everything under it.
func (e *tomlEdit) remove(path []string) {
	for {
		d := e.scan()
		done := true
		for h := len(d.headers) - 1; h >= 0; h-- {
			hd := d.headers[h]
			if len(hd.path) >= len(path) && slices.Equal(hd.path[:len(path)], path) {
				e.splice(hd.line, d.blockEnd(e, h), nil)
				done = false
				break
			}
		}
		if !done {
			continue
		}
		for i := len(d.keys) - 1; i >= 0; i-- {
			k := d.keys[i]
			if !k.inArray && len(k.path) >= len(path) && slices.Equal(k.path[:len(path)], path) {
				e.splice(k.start, k.end, nil)
				done = false
				break
			}
		}
		if done {
			return
		}
	}
}

// entryAt finds the [[array]] header whose entry the segment names, or -1.
func (e *tomlEdit) entryAt(d docScan, arr []string, seg string) int {
	name, key, _ := elemOf(seg)
	for h, hd := range d.headers {
		if !hd.array || !slices.Equal(hd.path, arr) {
			continue
		}
		body := strings.Join(e.lines[hd.line+1:d.blockEnd(e, h)], "\n")
		m, err := parseLayer([]byte(body))
		if err == nil && entryMatches(m, name, key) {
			return h
		}
	}
	return -1
}

// setEntry replaces the entry of the array at arr that seg names, or adds it.
func (e *tomlEdit) setEntry(arr []string, seg string, entry map[string]any) {
	d := e.scan()
	body := renderEntryBody(entry)
	if h := e.entryAt(d, arr, seg); h >= 0 {
		hd := d.headers[h]
		end := e.lastContent(hd.line+1, d.blockEnd(e, h))
		e.splice(hd.line+1, end, body)
		return
	}
	e.appendBlock(append([]string{"[[" + dottedKey(arr) + "]]"}, body...))
}

// removeEntry deletes the entry of the array at arr that seg names.
func (e *tomlEdit) removeEntry(arr []string, seg string) {
	d := e.scan()
	if h := e.entryAt(d, arr, seg); h >= 0 {
		e.splice(d.headers[h].line, d.blockEnd(e, h), nil)
	}
}

// dropEmptyTables removes [table] headers that hold nothing: no key, no
// comment, and no table under them.
func (e *tomlEdit) dropEmptyTables() {
	for {
		d := e.scan()
		removed := false
		for h := len(d.headers) - 1; h >= 0; h-- {
			hd := d.headers[h]
			if hd.array {
				continue
			}
			end := len(e.lines)
			if h+1 < len(d.headers) {
				end = d.headers[h+1].line
			}
			empty := true
			for _, l := range e.lines[hd.line+1 : end] {
				if strings.TrimSpace(l) != "" {
					empty = false
					break
				}
			}
			if !empty {
				continue
			}
			sub := false
			for _, o := range d.headers {
				if len(o.path) > len(hd.path) && slices.Equal(o.path[:len(hd.path)], hd.path) {
					sub = true
					break
				}
			}
			if sub {
				continue
			}
			e.splice(hd.line, end, nil)
			removed = true
			break
		}
		if !removed {
			e.lines = trimTrailingBlank(e.lines)
			return
		}
	}
}

// apply makes one change.
func (e *tomlEdit) apply(ch configChange) {
	last := ch.path[len(ch.path)-1]
	if _, _, ok := elemOf(last); ok {
		arr := ch.path[:len(ch.path)-1]
		if ch.deleted {
			e.removeEntry(arr, last)
			return
		}
		m, _ := ch.value.(map[string]any)
		e.setEntry(arr, last, m)
		return
	}
	switch v := ch.value.(type) {
	case nil:
		e.remove(ch.path)
	case map[string]any:
		e.setTable(ch.path, v)
	case []any:
		if isTableArray(v) {
			e.remove(ch.path)
			var block []string
			for i, entry := range v {
				if i > 0 {
					block = append(block, "")
				}
				block = append(block, "[["+dottedKey(ch.path)+"]]")
				block = append(block, renderEntryBody(entry.(map[string]any))...)
			}
			e.appendBlock(block)
			return
		}
		e.setLeaf(ch.path, v)
	default:
		e.setLeaf(ch.path, v)
	}
}

// editFile makes the changes to a file's text and checks the result. ok is
// false when the edited text does not parse to what the changes ask for; the
// caller then writes want instead.
func editFile(data []byte, changes []configChange) (out []byte, want map[string]any, ok bool, err error) {
	want, err = parseLayer(data)
	if err != nil {
		return nil, nil, false, err
	}
	e := newTOMLEdit(data)
	for _, ch := range changes {
		if ch.deleted {
			deletePath(want, ch.path)
			e.apply(configChange{path: ch.path})
			continue
		}
		setPath(want, ch.path, ch.value)
		e.apply(ch)
	}
	out = e.bytes()
	got, perr := parseLayer(out)
	return out, want, perr == nil && reflect.DeepEqual(got, want), nil
}

// dottedKey writes a path as a TOML dotted key.
func dottedKey(path []string) string {
	parts := make([]string, len(path))
	for i, p := range path {
		parts[i] = tomlKey(p)
	}
	return strings.Join(parts, ".")
}

// encodeValue writes a value the way it goes after the = of a key. A table is
// an inline table.
func encodeValue(v any) string {
	switch v := v.(type) {
	case map[string]any:
		if len(v) == 0 {
			return "{}"
		}
		parts := make([]string, 0, len(v))
		for _, k := range slices.Sorted(maps.Keys(v)) {
			parts = append(parts, tomlKey(k)+" = "+encodeValue(v[k]))
		}
		return "{ " + strings.Join(parts, ", ") + " }"
	case []any:
		parts := make([]string, len(v))
		for i, e := range v {
			parts[i] = encodeValue(e)
		}
		return "[" + strings.Join(parts, ", ") + "]"
	case string:
		return tomlString(v)
	}
	out, err := toml.Marshal(map[string]any{"v": v})
	if err != nil {
		return fmt.Sprint(v)
	}
	return strings.TrimSpace(strings.TrimPrefix(string(out), "v = "))
}

// renderTable is a table as lines: its header, its plain keys, and then its
// sub-tables and arrays of tables.
func renderTable(path []string, m map[string]any) []string {
	out := []string{"[" + dottedKey(path) + "]"}
	var subs, arrays []string
	for _, k := range slices.Sorted(maps.Keys(m)) {
		switch v := m[k].(type) {
		case map[string]any:
			subs = append(subs, k)
		case []any:
			if isTableArray(v) {
				arrays = append(arrays, k)
				continue
			}
			out = append(out, tomlKey(k)+" = "+encodeValue(v))
		default:
			out = append(out, tomlKey(k)+" = "+encodeValue(v))
		}
	}
	for _, k := range subs {
		out = append(out, "")
		out = append(out, renderTable(append(slices.Clone(path), k), m[k].(map[string]any))...)
	}
	for _, k := range arrays {
		for _, entry := range m[k].([]any) {
			out = append(out, "", "[["+dottedKey(append(slices.Clone(path), k))+"]]")
			out = append(out, renderEntryBody(entry.(map[string]any))...)
		}
	}
	return out
}

// renderEntryBody is the keys of an array entry, name and key first. A table
// inside an entry is written inline.
func renderEntryBody(m map[string]any) []string {
	keys := slices.Sorted(maps.Keys(m))
	order := func(k string) int {
		switch k {
		case "name":
			return 0
		case "key":
			return 1
		}
		return 2
	}
	slices.SortStableFunc(keys, func(a, b string) int { return order(a) - order(b) })
	out := make([]string, 0, len(keys))
	for _, k := range keys {
		out = append(out, tomlKey(k)+" = "+encodeValue(m[k]))
	}
	return out
}
