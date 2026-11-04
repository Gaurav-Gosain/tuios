package preview

import (
	"bufio"
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"io"
	"os"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/Gaurav-Gosain/tuios/internal/session"
)

// Tables, JSON and archives, parsed off the UI thread into what their views
// show. Each is bounded: a table's rows, a tree's nodes, an archive's
// entries. What a bound cuts is said, not hidden.

// The bounds.
const (
	tableBytesMax = 64 << 20
	tableRowsMax  = 500_000
	cellMax       = 4096 // runes kept of a cell
	jsonBytesMax  = 64 << 20
	jsonNodesMax  = 2_000_000
	archiveMax    = 50_000
)

// TableData is a CSV or TSV file in rows.
type TableData struct {
	Header []string
	Rows   [][]string
	// Numeric marks the columns whose cells read as numbers, which line
	// up on the right.
	Numeric []bool
	// Width is each column's widest cell in runes, of the first rows.
	Width []int
	// Capped says the file has more rows than were read.
	Capped bool
	Comma  rune
}

// ReadTable reads a CSV file, or a TSV one by its name or its first line.
func ReadTable(ctx context.Context, path string) (*TableData, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	br := bufio.NewReaderSize(io.LimitReader(f, tableBytesMax), 256<<10)
	comma := ','
	if IsTSV(path) {
		comma = '\t'
	} else if first, _ := br.Peek(4096); strings.Count(string(first), "\t") > strings.Count(string(first), ",") {
		comma = '\t'
	}
	r := csv.NewReader(br)
	r.Comma = comma
	r.LazyQuotes = true
	r.FieldsPerRecord = -1
	r.ReuseRecord = false
	t := &TableData{Comma: comma}
	for {
		if len(t.Rows)%4096 == 0 && ctx.Err() != nil {
			return nil, ctx.Err()
		}
		rec, err := r.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			var pe *csv.ParseError
			if errors.As(err, &pe) {
				continue
			}
			return nil, err
		}
		for i, c := range rec {
			if len(c) > cellMax {
				rec[i] = truncRunes(c, cellMax)
			}
		}
		if t.Header == nil {
			t.Header = rec
			continue
		}
		if len(t.Rows) >= tableRowsMax {
			t.Capped = true
			break
		}
		t.Rows = append(t.Rows, rec)
	}
	if t.Header == nil {
		return nil, errors.New("The table is empty.")
	}
	if _, err := br.Peek(1); err == nil && !t.Capped {
		// The byte bound cut the file.
		if fi, err := f.Stat(); err == nil && fi.Size() > tableBytesMax {
			t.Capped = true
		}
	}
	cols := len(t.Header)
	for _, row := range t.Rows[:min(len(t.Rows), 2000)] {
		cols = max(cols, len(row))
	}
	t.Numeric = make([]bool, cols)
	t.Width = make([]int, cols)
	for c := range cols {
		numeric, seen := true, 0
		w := 0
		if c < len(t.Header) {
			w = utf8.RuneCountInString(t.Header[c])
		}
		for _, row := range t.Rows[:min(len(t.Rows), 2000)] {
			if c >= len(row) {
				continue
			}
			cell := strings.TrimSpace(row[c])
			w = max(w, utf8.RuneCountInString(row[c]))
			if cell == "" {
				continue
			}
			seen++
			if _, err := strconv.ParseFloat(strings.ReplaceAll(cell, ",", ""), 64); err != nil {
				numeric = false
			}
		}
		t.Numeric[c] = numeric && seen > 0
		t.Width[c] = w
	}
	return t, nil
}

func truncRunes(s string, n int) string {
	i := 0
	for k := range s {
		if i == n {
			return s[:k] + "…"
		}
		i++
	}
	return s
}

// JSON values as a tree: nodes in document order, each a value with the key
// or index it has in its parent. A container's children follow it, and End
// is the node after its last descendant, so the children of node i are
// i+1, then each child's End, up to i's End.

// JSONKind is what a JSON value is.
type JSONKind byte

// The kinds of JSON values.
const (
	JSONObject JSONKind = 'o'
	JSONArray  JSONKind = 'a'
	JSONString JSONKind = 's'
	JSONNumber JSONKind = 'n'
	JSONBool   JSONKind = 'b'
	JSONNull   JSONKind = 'z'
)

// JSONNode is one value of a JSON tree.
type JSONNode struct {
	Key   string // the key in an object, or "[i]" in an array; "" at the root
	Value string // a scalar's text; for a container, nothing
	Kind  JSONKind
	Count int32 // a container's children
	End   int32 // the node after the last of its descendants
	Depth int16
}

// JSONTree is a JSON file read into nodes.
type JSONTree struct {
	Nodes  []JSONNode
	Capped bool // the file holds more than was read
}

// Children are the children of node i.
func (t *JSONTree) Children(i int) []int32 {
	n := &t.Nodes[i]
	if n.Kind != JSONObject && n.Kind != JSONArray || n.Count == 0 {
		return nil
	}
	out := make([]int32, 0, n.Count)
	for k := int32(i + 1); k < n.End; k = t.Nodes[k].End {
		out = append(out, k)
	}
	return out
}

// ReadJSON reads a JSON file into a tree.
func ReadJSON(ctx context.Context, path string) (*JSONTree, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	fi, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if fi.Size() > jsonBytesMax {
		return nil, errors.New("The JSON file is too large to read as a tree.")
	}
	dec := json.NewDecoder(bufio.NewReaderSize(f, 256<<10))
	dec.UseNumber()
	t := &JSONTree{}
	type frame struct {
		node int
		obj  bool
		key  string // an object's next key, once read
		has  bool
		idx  int
	}
	var stack []frame
	add := func(n JSONNode) int {
		if len(stack) > 0 {
			top := &stack[len(stack)-1]
			if top.obj {
				n.Key, top.has = top.key, false
			} else {
				n.Key = "[" + strconv.Itoa(top.idx) + "]"
				top.idx++
			}
			t.Nodes[top.node].Count++
			n.Depth = int16(len(stack))
		}
		t.Nodes = append(t.Nodes, n)
		return len(t.Nodes) - 1
	}
	for {
		if len(t.Nodes)%8192 == 0 && ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if len(t.Nodes) >= jsonNodesMax {
			t.Capped = true
			break
		}
		tok, err := dec.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			if len(t.Nodes) == 0 {
				return nil, errors.New("The file is not valid JSON: " + err.Error())
			}
			t.Capped = true
			break
		}
		// An object's key comes as a string token before its value.
		if len(stack) > 0 {
			if top := &stack[len(stack)-1]; top.obj && !top.has {
				if s, ok := tok.(string); ok {
					top.key, top.has = s, true
					continue
				}
			}
		}
		switch v := tok.(type) {
		case json.Delim:
			switch v {
			case '{', '[':
				i := add(JSONNode{Kind: map[json.Delim]JSONKind{'{': JSONObject, '[': JSONArray}[v]})
				stack = append(stack, frame{node: i, obj: v == '{'})
			case '}', ']':
				top := stack[len(stack)-1]
				stack = stack[:len(stack)-1]
				t.Nodes[top.node].End = int32(len(t.Nodes))
			}
			continue
		case string:
			add(JSONNode{Kind: JSONString, Value: truncRunes(v, cellMax), End: int32(len(t.Nodes) + 1)})
		case json.Number:
			add(JSONNode{Kind: JSONNumber, Value: v.String(), End: int32(len(t.Nodes) + 1)})
		case bool:
			add(JSONNode{Kind: JSONBool, Value: strconv.FormatBool(v), End: int32(len(t.Nodes) + 1)})
		case nil:
			add(JSONNode{Kind: JSONNull, Value: "null", End: int32(len(t.Nodes) + 1)})
		}
		if len(stack) == 0 {
			break // one value at the top
		}
	}
	// A tree cut short closes its open containers where it stopped.
	for _, fr := range stack {
		t.Nodes[fr.node].End = int32(len(t.Nodes))
	}
	if len(t.Nodes) == 0 {
		return nil, errors.New("The file is empty.")
	}
	return t, nil
}

// ArchiveEntry is one entry of an archive's listing.
type ArchiveEntry struct {
	Name string
	Size int64
	Dir  bool
}

// ListArchive lists an archive: zip and tar (gzipped or not) as tuios's
// quick look reads them, in Go, and the rest (7z, rar, xz, zstd) through
// bsdtar where this machine has it.
func ListArchive(ctx context.Context, path string) ([]ArchiveEntry, bool, error) {
	out, err := session.PreviewLocalWant(ctx, path, session.PreviewWant{})
	if err == nil && out["kind"] == "archive" {
		raw, _ := out["entries"].([]session.PreviewArchiveEntry)
		entries := make([]ArchiveEntry, len(raw))
		for i, e := range raw {
			entries[i] = ArchiveEntry{Name: e.Name, Size: e.Size, Dir: e.Dir}
		}
		capped, _ := out["capped"].(bool)
		return entries, capped, nil
	}
	b, err := run(ctx, 64<<20, "bsdtar", "-tvf", path)
	if err != nil {
		var nt ErrNoTool
		if errors.As(err, &nt) {
			return nil, false, errors.New("This machine has no bsdtar to list this archive.")
		}
		if len(b) == 0 {
			return nil, false, errors.New("The archive could not be read.")
		}
	}
	var entries []ArchiveEntry
	capped := false
	for line := range strings.SplitSeq(string(b), "\n") {
		e, ok := parseTarLine(line)
		if !ok {
			continue
		}
		if len(entries) >= archiveMax {
			capped = true
			break
		}
		entries = append(entries, e)
	}
	if len(entries) == 0 {
		return nil, false, errors.New("The archive could not be read.")
	}
	return entries, capped, nil
}

// parseTarLine reads a line of `bsdtar -tv`: mode, links, owner, group,
// size, month, day, time or year, then the name.
func parseTarLine(line string) (ArchiveEntry, bool) {
	f := strings.Fields(line)
	if len(f) < 9 || len(f[0]) < 10 {
		return ArchiveEntry{}, false
	}
	size, err := strconv.ParseInt(f[4], 10, 64)
	if err != nil {
		return ArchiveEntry{}, false
	}
	// The name is what follows the eighth field, spaces and all.
	rest := line
	for k := 0; k < 8; k++ {
		rest = strings.TrimLeft(rest, " \t")
		i := strings.IndexAny(rest, " \t")
		if i < 0 {
			return ArchiveEntry{}, false
		}
		rest = rest[i:]
	}
	name := strings.TrimLeft(rest, " \t")
	if i := strings.Index(name, " -> "); i >= 0 && f[0][0] == 'l' {
		name = name[:i]
	}
	return ArchiveEntry{Name: name, Size: size, Dir: f[0][0] == 'd'}, true
}
