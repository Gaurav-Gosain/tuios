package config

import (
	"fmt"
	"maps"
	"reflect"
	"slices"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

type tomlEdit struct {
	lines []string
	crlf  bool
}

type docHeader struct {
	line  int
	path  []string
	array bool
}

type docKey struct {
	start, end int
	path       []string
	header     int
	inArray    bool
	keyText    string
	comment    string
}

type docScan struct {
	headers []docHeader
	keys    []docKey
}

func newTOMLEdit(data []byte) *tomlEdit {
	s := string(data)
	e := &tomlEdit{crlf: strings.Contains(s, "\r\n")}
	if e.crlf {
		s = strings.ReplaceAll(s, "\r\n", "\n")
	}
	e.lines = splitLines(s)
	return e
}

func (e *tomlEdit) bytes() []byte {
	s := joinLines(e.lines)
	if e.crlf {
		s = strings.ReplaceAll(s, "\n", "\r\n")
	}
	return []byte(s)
}

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

type valueScan struct {
	depth int
	ml    string
}

func (v *valueScan) done() bool { return v.depth <= 0 && v.ml == "" }

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

func (e *tomlEdit) lastCode(from, to int) int {
	for i := to; i > from; i-- {
		s := strings.TrimSpace(e.lines[i-1])
		if s != "" && !strings.HasPrefix(s, "#") {
			return i
		}
	}
	return from
}

func (e *tomlEdit) cut(from, to int) {
	e.splice(from, to, nil)
	if from >= len(e.lines) || strings.TrimSpace(e.lines[from]) != "" {
		return
	}
	if from == 0 || strings.TrimSpace(e.lines[from-1]) == "" {
		e.splice(from, from+1, nil)
	}
}

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

func (e *tomlEdit) setTable(path []string, m map[string]any) {
	e.remove(path)
	e.appendBlock(renderTable(path, m))
}

func (e *tomlEdit) appendBlock(block []string) {
	end := e.lastContent(0, len(e.lines))
	e.lines = e.lines[:end]
	if end > 0 {
		e.lines = append(e.lines, "")
	}
	e.lines = append(e.lines, block...)
}

func (e *tomlEdit) remove(path []string) {
	for {
		d := e.scan()
		done := true
		for h, hd := range slices.Backward(d.headers) {
			if len(hd.path) >= len(path) && slices.Equal(hd.path[:len(path)], path) {
				e.cut(hd.line, e.lastCode(hd.line+1, d.blockEnd(e, h)))
				done = false
				break
			}
		}
		if !done {
			continue
		}
		for _, k := range slices.Backward(d.keys) {
			if !k.inArray && len(k.path) >= len(path) && slices.Equal(k.path[:len(path)], path) {
				e.cut(k.start, k.end)
				done = false
				break
			}
		}
		if done {
			return
		}
	}
}

func (e *tomlEdit) entryAt(d docScan, arr []string, seg string, tomb bool) int {
	name, key, _ := elemOf(seg)
	for h, hd := range d.headers {
		if !hd.array || !slices.Equal(hd.path, arr) {
			continue
		}
		body := strings.Join(e.lines[hd.line+1:d.blockEnd(e, h)], "\n")
		m, err := parseLayer([]byte(body))
		if err == nil && entryMatches(m, name, key) && isTombstone(m) == tomb {
			return h
		}
	}
	return -1
}

func tombstoneBlock(arr []string, seg string) []string {
	return append([]string{"[[" + dottedKey(arr) + "]]"}, renderEntryBody(tombstoneFor(seg))...)
}

func (e *tomlEdit) setEntry(arr []string, seg string, entry map[string]any, shadow bool) {
	d := e.scan()
	h := e.entryAt(d, arr, seg, false)
	if shadow {
		t := e.entryAt(d, arr, seg, true)
		if t < 0 || (h >= 0 && d.headers[t].line > d.headers[h].line) {
			block := tombstoneBlock(arr, seg)
			if h >= 0 {
				e.splice(d.headers[h].line, d.headers[h].line, append(block, ""))
			} else {
				e.appendBlock(block)
			}
			d = e.scan()
			h = e.entryAt(d, arr, seg, false)
		}
	}
	body := renderEntryBody(entry)
	if h >= 0 {
		hd := d.headers[h]
		end := e.lastCode(hd.line+1, d.blockEnd(e, h))
		e.splice(hd.line+1, end, body)
		return
	}
	e.appendBlock(append([]string{"[[" + dottedKey(arr) + "]]"}, body...))
}

func (e *tomlEdit) removeEntry(arr []string, seg string) {
	d := e.scan()
	if h := e.entryAt(d, arr, seg, false); h >= 0 {
		e.cut(d.headers[h].line, e.lastCode(d.headers[h].line+1, d.blockEnd(e, h)))
	}
}

func (e *tomlEdit) dropEmptyTables() {
	for {
		d := e.scan()
		removed := false
		for h, hd := range slices.Backward(d.headers) {
			if hd.array {
				continue
			}
			end := len(e.lines)
			if h+1 < len(d.headers) {
				end = d.headers[h+1].line
			}
			empty := e.lastCode(hd.line+1, end) == hd.line+1
			if empty && hd.line+1 < end && strings.HasPrefix(strings.TrimSpace(e.lines[hd.line+1]), "#") {
				empty = false
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
			e.cut(hd.line, e.lastCode(hd.line+1, end))
			removed = true
			break
		}
		if !removed {
			e.lines = trimTrailingBlank(e.lines)
			return
		}
	}
}

func (e *tomlEdit) apply(ch configChange) {
	last := ch.path[len(ch.path)-1]
	if _, _, ok := elemOf(last); ok {
		arr := ch.path[:len(ch.path)-1]
		if ch.deleted {
			e.removeEntry(arr, last)
			return
		}
		m, _ := ch.value.(map[string]any)
		e.setEntry(arr, last, m, ch.shadow)
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
		if ch.shadow {
			shadowEntry(want, ch.path)
		}
		setPath(want, ch.path, ch.value)
		e.apply(ch)
	}
	out = e.bytes()
	got, perr := parseLayer(out)
	return out, want, perr == nil && reflect.DeepEqual(got, want), nil
}

func shadowEntry(m map[string]any, path []string) {
	arrPath, seg := path[:len(path)-1], path[len(path)-1]
	v, _ := lookupPath(m, arrPath)
	arr, _ := v.([]any)
	at := entryIndexOf(arr, seg, false)
	if t := entryIndexOf(arr, seg, true); t >= 0 && (at < 0 || t < at) && isTombstone(arr[t].(map[string]any)) {
		return
	}
	tomb := tombstoneFor(seg)
	if at < 0 {
		setPath(m, arrPath, append(slices.Clone(arr), tomb))
		return
	}
	setPath(m, arrPath, slices.Insert(slices.Clone(arr), at, any(tomb)))
}

func tombstoneFor(seg string) map[string]any {
	name, key, _ := elemOf(seg)
	entry := map[string]any{tombstoneKey: true}
	if name != "" {
		entry["name"] = name
	}
	if key != "" {
		entry["key"] = key
	}
	return entry
}

func dottedKey(path []string) string {
	parts := make([]string, len(path))
	for i, p := range path {
		parts[i] = tomlKey(p)
	}
	return strings.Join(parts, ".")
}

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
