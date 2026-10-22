package main

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"strconv"
	"strings"
)

// table is the data as tplot sees it: the labels of the rows, from the first
// text column, and a series per number column.
type table struct {
	labels []string
	series []series
}

type series struct {
	name   string
	named  bool      // the name came from the data, not from the column number
	values []float64 // NaN where a cell is empty
}

// guess picks a chart for the shape of the data.
func (t *table) guess() string {
	n := len(t.series[0].values)
	switch {
	case len(t.series) > 1:
		return "line"
	case len(t.labels) > 0 && n <= 40:
		return "bar"
	}
	return "line"
}

// pick keeps the series that list names, by name or by number from 1.
func (t *table) pick(list string) error {
	var out []series
	for want := range strings.SplitSeq(list, ",") {
		want = strings.TrimSpace(want)
		found := false
		for i, s := range t.series {
			if strings.EqualFold(s.name, want) || strconv.Itoa(i+1) == want {
				out = append(out, s)
				found = true
				break
			}
		}
		if !found {
			var names []string
			for _, s := range t.series {
				names = append(names, s.name)
			}
			return fmt.Errorf("no number column %q. The number columns are: %s", want, strings.Join(names, ", "))
		}
	}
	t.series = out
	return nil
}

// readTable reads CSV, TSV, whitespace-separated numbers or JSON. format
// names one, or is empty for a guess from the name and the data.
func readTable(data []byte, format, name string, noHeader bool) (*table, error) {
	data = bytes.TrimPrefix(data, []byte("\xef\xbb\xbf"))
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 {
		return nil, errors.New("the input is empty")
	}
	if format == "" {
		lower := strings.ToLower(name)
		switch {
		case trimmed[0] == '[' || trimmed[0] == '{' || strings.HasSuffix(lower, ".json"):
			format = "json"
		case strings.HasSuffix(lower, ".tsv"):
			format = "tsv"
		case bytes.Contains(firstLine(trimmed), []byte{'\t'}):
			format = "tsv"
		case bytes.Contains(firstLine(trimmed), []byte{','}) || strings.HasSuffix(lower, ".csv"):
			format = "csv"
		default:
			format = "numbers"
		}
	}
	var records [][]string
	header := false
	switch strings.ToLower(format) {
	case "json":
		var err error
		records, header, err = jsonRecords(trimmed)
		if err != nil {
			return nil, err
		}
	case "csv", "tsv":
		r := csv.NewReader(bytes.NewReader(data))
		if strings.EqualFold(format, "tsv") {
			r.Comma = '\t'
		}
		r.FieldsPerRecord = -1
		r.TrimLeadingSpace = true
		r.LazyQuotes = true
		r.ReuseRecord = false
		for {
			rec, err := r.Read()
			if err == io.EOF {
				break
			}
			if err != nil {
				return nil, fmt.Errorf("cannot read the CSV: %w", err)
			}
			if len(rec) == 1 && strings.TrimSpace(rec[0]) == "" {
				continue
			}
			records = append(records, rec)
		}
	case "numbers":
		for line := range strings.SplitSeq(string(data), "\n") {
			if f := strings.Fields(line); len(f) > 0 {
				records = append(records, f)
			}
		}
	default:
		return nil, fmt.Errorf("unknown input format %q: use csv, tsv, json or numbers", format)
	}
	if noHeader {
		header = false
	} else if !header {
		header = hasHeader(records)
	}
	return build(records, header)
}

func firstLine(b []byte) []byte {
	if before, _, ok := bytes.Cut(b, []byte{'\n'}); ok {
		return before
	}
	return b
}

// number parses a cell: a number, with spaces, a thousands "_" or a trailing
// % allowed. ok is false for text. An empty cell is a missing value.
func number(s string) (v float64, ok bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return math.NaN(), true
	}
	s = strings.TrimSuffix(s, "%")
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, false
	}
	return v, true
}

func isNumber(s string) bool {
	_, ok := number(s)
	return ok && strings.TrimSpace(s) != ""
}

// hasHeader says whether the first record names the columns: one of its
// cells is text where the rest of that column is numbers.
func hasHeader(records [][]string) bool {
	if len(records) < 2 {
		return false
	}
	for c, cell := range records[0] {
		if strings.TrimSpace(cell) == "" || isNumber(cell) {
			continue
		}
		numeric, seen := true, false
		for _, rec := range records[1:] {
			if c >= len(rec) || strings.TrimSpace(rec[c]) == "" {
				continue
			}
			seen = true
			if !isNumber(rec[c]) {
				numeric = false
				break
			}
		}
		if numeric && seen {
			return true
		}
	}
	return false
}

// build turns records into a table: the first text column gives the labels,
// and every column of numbers is a series.
func build(records [][]string, header bool) (*table, error) {
	var names []string
	if header && len(records) > 0 {
		names, records = records[0], records[1:]
	}
	if len(records) == 0 {
		return nil, errors.New("the input has no rows of data")
	}
	cols := 0
	for _, rec := range records {
		cols = max(cols, len(rec))
	}
	cell := func(rec []string, c int) string {
		if c < len(rec) {
			return rec[c]
		}
		return ""
	}
	t := &table{}
	labelCol := -1
	for c := range cols {
		numeric := true
		for _, rec := range records {
			if _, ok := number(cell(rec, c)); !ok {
				numeric = false
				break
			}
		}
		name := ""
		if c < len(names) {
			name = strings.TrimSpace(names[c])
		}
		if !numeric {
			if labelCol < 0 {
				labelCol = c
			}
			continue
		}
		s := series{name: name, named: name != "", values: make([]float64, len(records))}
		if name == "" {
			s.name = "column " + strconv.Itoa(c+1)
		}
		for i, rec := range records {
			s.values[i], _ = number(cell(rec, c))
		}
		t.series = append(t.series, s)
	}
	if labelCol >= 0 {
		for _, rec := range records {
			t.labels = append(t.labels, strings.TrimSpace(cell(rec, labelCol)))
		}
	}
	return t, nil
}

// jsonRecords turns JSON into records. header says the first record names
// the columns.
//
//	[1, 2, 3]                     one series
//	{"a": 1, "b": 2}              labels and a series
//	{"rx": [1, 2], "tx": [3, 4]}  a series per key
//	[[1, 2], [3, 4]]              rows, as CSV
//	[{"name": "a", "v": 1}, ...]  rows, the keys as names
func jsonRecords(data []byte) ([][]string, bool, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	v, err := decode(dec, 0)
	if err != nil {
		return nil, false, fmt.Errorf("cannot read the JSON: %w", err)
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil, false, errors.New("cannot read the JSON: more than one value")
	}
	switch v := v.(type) {
	case []any:
		if len(v) == 0 {
			return nil, false, errors.New("the JSON array is empty")
		}
		switch v[0].(type) {
		case []any:
			var out [][]string
			for _, row := range v {
				r, ok := row.([]any)
				if !ok {
					return nil, false, errors.New("the JSON array mixes rows and values")
				}
				out = append(out, cells(r))
			}
			return out, false, nil
		case object:
			var keys []string
			seen := map[string]bool{}
			for _, row := range v {
				o, ok := row.(object)
				if !ok {
					return nil, false, errors.New("the JSON array mixes objects and values")
				}
				for _, kv := range o {
					if !seen[kv.key] {
						seen[kv.key] = true
						keys = append(keys, kv.key)
					}
				}
			}
			out := [][]string{keys}
			for _, row := range v {
				o := row.(object)
				rec := make([]string, len(keys))
				for i, k := range keys {
					rec[i] = scalar(o.get(k))
				}
				out = append(out, rec)
			}
			return out, true, nil
		}
		out := [][]string{{"value"}}
		for _, x := range v {
			out = append(out, []string{scalar(x)})
		}
		return out, true, nil
	case object:
		if len(v) == 0 {
			return nil, false, errors.New("the JSON object is empty")
		}
		if _, ok := v[0].val.([]any); ok {
			n := 0
			names := make([]string, len(v))
			for i, kv := range v {
				names[i] = kv.key
				if a, ok := kv.val.([]any); ok {
					n = max(n, len(a))
				}
			}
			out := [][]string{names}
			for r := range n {
				rec := make([]string, len(v))
				for i, kv := range v {
					if a, ok := kv.val.([]any); ok && r < len(a) {
						rec[i] = scalar(a[r])
					}
				}
				out = append(out, rec)
			}
			return out, true, nil
		}
		out := [][]string{{"label", "value"}}
		for _, kv := range v {
			out = append(out, []string{kv.key, scalar(kv.val)})
		}
		return out, true, nil
	}
	return [][]string{{scalar(v)}}, false, nil
}

// object is a JSON object with its keys in order.
type object []struct {
	key string
	val any
}

func (o object) get(k string) any {
	for _, kv := range o {
		if kv.key == k {
			return kv.val
		}
	}
	return nil
}

// maxDepth bounds the nesting decode follows.
const maxDepth = 64

// decode reads one JSON value, keeping the order of object keys.
func decode(dec *json.Decoder, depth int) (any, error) {
	if depth > maxDepth {
		return nil, errors.New("the JSON is nested too deeply")
	}
	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}
	switch tok {
	case json.Delim('['):
		var out []any
		for dec.More() {
			v, err := decode(dec, depth+1)
			if err != nil {
				return nil, err
			}
			out = append(out, v)
		}
		_, err := dec.Token()
		return out, err
	case json.Delim('{'):
		var out object
		for dec.More() {
			k, err := dec.Token()
			if err != nil {
				return nil, err
			}
			v, err := decode(dec, depth+1)
			if err != nil {
				return nil, err
			}
			out = append(out, struct {
				key string
				val any
			}{fmt.Sprint(k), v})
		}
		_, err := dec.Token()
		return out, err
	}
	return tok, nil
}

func cells(r []any) []string {
	out := make([]string, len(r))
	for i, x := range r {
		out[i] = scalar(x)
	}
	return out
}

// scalar writes a JSON value as a cell. Null, true, false, arrays and
// objects are empty: they are not numbers and not labels.
func scalar(v any) string {
	switch v := v.(type) {
	case json.Number:
		return v.String()
	case string:
		return v
	}
	return ""
}
