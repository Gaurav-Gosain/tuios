package main

import (
	"encoding/json"
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/Gaurav-Gosain/tuios/pkg/fuzzy"
)

// The settings page searches its rows by name, key, value and description.
// list-options --search is the same search for a shell: the query is ranked
// against each option's path first, then its value in this session and its
// default, then a word from its description, with the fuzzy matcher the page
// and the palette use. A description has to match close together, or a short
// query would find every sentence one letter at a time.

// optionHit is one option a search found.
type optionHit struct {
	index, field, score int
}

// rankOptions orders the options that match query, best first.
func rankOptions(options []optionRow, query string) []optionHit {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil
	}
	keyQuery := strings.ReplaceAll(query, " ", "_")
	var m fuzzy.Matcher
	var hits []optionHit
	for i, o := range options {
		fields := []struct {
			pattern, text string
			compact       bool
		}{
			{keyQuery, o.Path, false},
			{query, o.SessionVal, false},
			{query, o.Default, false},
			{query, o.Description, true},
		}
		for f, field := range fields {
			if field.text == "" {
				continue
			}
			r, ok := m.Find(field.pattern, field.text)
			if !ok {
				continue
			}
			n := len(field.pattern)
			if field.compact && r.End-r.Start > n+n/2+1 {
				continue
			}
			hits = append(hits, optionHit{index: i, field: f, score: r.Score})
			break
		}
	}
	slices.SortStableFunc(hits, func(a, b optionHit) int {
		if a.field != b.field {
			return a.field - b.field
		}
		return b.score - a.score
	})
	return hits
}

// printOptionSearch writes the options a search found, best first. The JSON
// form keeps each option exactly as the daemon sent it.
func printOptionSearch(w io.Writer, raw json.RawMessage, query string, jsonOutput bool) error {
	var res struct {
		Options []json.RawMessage `json:"options"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return fmt.Errorf("failed to parse response: %w", err)
	}
	rows := make([]optionRow, len(res.Options))
	for i, o := range res.Options {
		if err := json.Unmarshal(o, &rows[i]); err != nil {
			return fmt.Errorf("failed to parse response: %w", err)
		}
	}
	hits := rankOptions(rows, query)

	if jsonOutput {
		out := struct {
			Options []json.RawMessage `json:"options"`
			Total   int               `json:"total"`
			Query   string            `json:"query"`
		}{Options: []json.RawMessage{}, Total: len(hits), Query: query}
		for _, h := range hits {
			out.Options = append(out.Options, res.Options[h.index])
		}
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		return enc.Encode(out)
	}

	if len(hits) == 0 {
		fmt.Fprintf(w, "No options match %q.\n", query)
		return nil
	}
	width := 0
	for _, h := range hits {
		width = max(width, len(rows[h.index].Path))
	}
	for _, h := range hits {
		o := rows[h.index]
		fmt.Fprintf(w, "%-*s  %-6s  default %s\n", width, o.Path, o.Type, orNone(o.Default))
		fmt.Fprintf(w, "%-*s  %s\n", width, "", o.Description)
		if o.SessionVal != "" {
			fmt.Fprintf(w, "%-*s  this session: %s\n", width, "", o.SessionVal)
		}
	}
	fmt.Fprintf(w, "\n%d option(s) match %q, best first. Set one with 'tuios set-config <path> <value>'.\n", len(hits), query)
	return nil
}
