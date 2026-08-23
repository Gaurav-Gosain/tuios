package main

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
)

// glyphSetDetail is the described set as list-glyphs reports it.
type glyphSetDetail struct {
	ID             string            `json:"id"`
	DisplayName    string            `json:"display_name"`
	Inherits       string            `json:"inherits"`
	ASCII          bool              `json:"ascii"`
	BorderStyle    string            `json:"border_style"`
	BorderInEffect bool              `json:"border_in_effect"`
	Names          map[string]string `json:"names"`
	Drawn          map[string]string `json:"drawn"`
}

// runListGlyphs lists the glyph sets and, with a name, describes one.
//
// The human form prints what the set names beside what would actually be drawn,
// because those are two different things and the gap between them is the whole
// difficulty of authoring a set: a set states only what it changes, and a role
// whose glyph was the wrong width for its slot is dropped back to the default
// without anything on screen saying so.
func runListGlyphs(sessionName, setName string, jsonOutput bool) error {
	client, err := dialVerb()
	if err != nil {
		return err
	}
	defer func() { _ = client.Close() }()

	raw, err := client.Call("list-glyphs", map[string]any{
		"session": sessionName,
		"glyphs":  setName,
	})
	if err != nil {
		return reportVerbError(explainVerbError("list-glyphs", err), jsonOutput)
	}
	if jsonOutput {
		return printVerbResult(raw, jsonOutput)
	}

	var res struct {
		Sets      []string       `json:"sets"`
		Roles     []string       `json:"roles"`
		Total     int            `json:"total"`
		Active    string         `json:"active"`
		Source    string         `json:"active_source"`
		GlyphsDir string         `json:"glyphs_dir"`
		Problems  []string       `json:"problems"`
		Set       glyphSetDetail `json:"set"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return fmt.Errorf("failed to parse response: %w", err)
	}

	w := os.Stdout
	if res.Set.ID != "" {
		printGlyphSet(w, res.Set)
		fmt.Fprintln(w)
	} else {
		printGlyphSetList(w, res.Sets, res.Total)
		fmt.Fprintf(w, "\nroles: %s\n", strings.Join(res.Roles, ", "))
	}
	fmt.Fprintf(w, "\nactive: %s", orNone(res.Active))
	if res.Source != "" {
		fmt.Fprintf(w, " (%s)", res.Source)
	}
	fmt.Fprintf(w, "\nglyphs dir: %s\n", res.GlyphsDir)
	for _, p := range res.Problems {
		fmt.Fprintf(w, "problem: %s\n", p)
	}
	return nil
}

// printGlyphSetList prints the ids in one wrapped run, like list-themes does.
func printGlyphSetList(w *os.File, sets []string, total int) {
	if len(sets) == 0 {
		fmt.Fprintln(w, "no glyph sets")
		return
	}
	fmt.Fprintln(w)
	for _, id := range sets {
		fmt.Fprintf(w, "  %-20s", id)
	}
	fmt.Fprintf(w, "\n\n%d glyph set(s).\n", total)
}

// printGlyphSet prints one set, role by role: what the set says, and what draws.
func printGlyphSet(w *os.File, set glyphSetDetail) {
	fmt.Fprintf(w, "%s  (%s)", set.ID, set.DisplayName)
	if set.Inherits != "" {
		fmt.Fprintf(w, ", inherits %s", set.Inherits)
	}
	if set.ASCII {
		fmt.Fprint(w, ", ascii-safe")
	}
	fmt.Fprintln(w)
	fmt.Fprintln(w)

	roles := make([]string, 0, len(set.Drawn)+len(set.Names))
	seen := map[string]bool{}
	for r := range set.Drawn {
		roles, seen[r] = append(roles, r), true
	}
	for r := range set.Names {
		if !seen[r] {
			roles = append(roles, r)
		}
	}
	sort.Strings(roles)

	for _, role := range roles {
		named, isNamed := set.Names[role]
		drawn := set.Drawn[role]
		// A role the set does not mention reads as "-" rather than blank, so
		// the two columns cannot be misread as "it named this and it did not
		// take". A role it did name and did not get is marked, which is what
		// ASCII mode does to a glyph the terminal cannot draw; a role dropped
		// for its width never reaches here, because it is dropped on load and
		// reported as a problem instead.
		mark := " "
		switch {
		case !isNamed:
			named = "-"
		case drawn != "" && named != drawn:
			mark = "!"
		}
		fmt.Fprintf(w, " %s %-21s %-6s %s\n", mark, role, quoteGlyph(named), quoteGlyph(drawn))
	}
	fmt.Fprintln(w)
	fmt.Fprintln(w, "columns: role, what the set says, what draws. ! marks a role the set")
	fmt.Fprintln(w, "named and did not get. A role whose glyph was the wrong width for its")
	fmt.Fprintln(w, "slot was dropped on load and is listed under problems below.")
	if !set.BorderInEffect {
		fmt.Fprintf(w, "the border rows are what this set would draw; border_style is %q, so\n"+
			"nothing draws them. set appearance.border_style to %q to use them.\n",
			set.BorderStyle, "glyphs")
	}
}

// quoteGlyph makes an empty or blank glyph visible in a column.
func quoteGlyph(g string) string {
	if g == "" {
		return `""`
	}
	if strings.TrimSpace(g) == "" {
		return fmt.Sprintf("%q", g)
	}
	return g
}
