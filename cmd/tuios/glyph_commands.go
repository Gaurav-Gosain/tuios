package main

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/errfmt"
	"github.com/Gaurav-Gosain/tuios/internal/jsonx"
	"github.com/Gaurav-Gosain/tuios/internal/plural"
)

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

func runListGlyphs(sessionName, setName string, jsonOutput bool) error {
	raw, err := localVerb("list-glyphs", map[string]any{
		"session": sessionName,
		"glyphs":  setName,
	}, 0, jsonOutput)
	if err != nil {
		return err
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
	if err := jsonx.Unmarshal(raw, &res); err != nil {
		return errfmt.Errorf("failed to parse response: %w", err)
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
	if env, why := config.DetectGlyphEnv(os.Getenv); why != "" {
		fmt.Fprintf(w, "this terminal: %s glyphs, because %s (a glyph set or --ascii-only overrides it)\n", env, why)
	}
	for _, p := range res.Problems {
		fmt.Fprintf(w, "problem: %s\n", p)
	}
	return nil
}

func printGlyphSetList(w *os.File, sets []string, total int) {
	if len(sets) == 0 {
		fmt.Fprintln(w, "no glyph sets")
		return
	}
	fmt.Fprintln(w)
	for _, id := range sets {
		fmt.Fprintf(w, "  %-20s", id)
	}
	fmt.Fprintf(w, "\n\n%s.\n", plural.Count(total, "glyph set"))
}

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

func quoteGlyph(g string) string {
	if g == "" {
		return `""`
	}
	if strings.TrimSpace(g) == "" {
		return fmt.Sprintf("%q", g)
	}
	return g
}
