package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
)

// themeSwatch is one measured colour of a theme as list-themes reports it.
type themeSwatch struct {
	Name   string  `json:"name"`
	Hex    string  `json:"hex"`
	Ratio  float64 `json:"ratio"`
	Floor  float64 `json:"floor"`
	Passes bool    `json:"passes"`
}

// themePalette is the described theme.
type themePalette struct {
	ID          string        `json:"id"`
	DisplayName string        `json:"display_name"`
	Dark        bool          `json:"dark"`
	Bg          string        `json:"bg"`
	Swatches    []themeSwatch `json:"swatches"`
	Illegible   []string      `json:"illegible"`
}

// runListThemes lists the registered themes and, with a name, describes one.
//
// The human form prints the contrast beside each colour rather than the hex
// alone, because the number is the only part a reader cannot get by looking at
// the theme in a picker, and it is the part that says whether the palette is
// going to be readable.
func runListThemes(sessionName, themeName, filter string, jsonOutput bool) error {
	client, err := dialVerb()
	if err != nil {
		return err
	}
	defer func() { _ = client.Close() }()

	raw, err := client.Call("list-themes", map[string]any{
		"session": sessionName,
		"theme":   themeName,
		"filter":  filter,
	})
	if err != nil {
		return reportVerbError(explainVerbError("list-themes", err), jsonOutput)
	}
	if jsonOutput {
		return printVerbResult(raw, jsonOutput)
	}

	var res struct {
		Themes    []string     `json:"themes"`
		Total     int          `json:"total"`
		Matched   int          `json:"matched"`
		Truncated bool         `json:"truncated"`
		Active    string       `json:"active"`
		Source    string       `json:"active_source"`
		ThemesDir string       `json:"themes_dir"`
		Problems  []string     `json:"problems"`
		Palette   themePalette `json:"palette"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return fmt.Errorf("failed to parse response: %w", err)
	}

	w := os.Stdout
	if res.Palette.ID != "" {
		printThemePalette(w, res.Palette)
		fmt.Fprintln(w)
	}
	// The verb sends the roster only when the roster was the question, so its
	// absence is the answer here rather than an empty result to report.
	if themeName == "" || filter != "" {
		printThemeList(w, res.Themes, res.Matched, res.Total, res.Truncated)
	}
	fmt.Fprintf(w, "\nactive: %s", orNone(res.Active))
	if res.Source != "" {
		fmt.Fprintf(w, " (%s)", res.Source)
	}
	fmt.Fprintf(w, "\nthemes dir: %s\n", res.ThemesDir)
	for _, p := range res.Problems {
		fmt.Fprintf(w, "problem: %s\n", p)
	}
	return nil
}

// printThemeList writes the matching ids in columns.
func printThemeList(w io.Writer, themes []string, matched, total int, truncated bool) {
	if len(themes) == 0 {
		fmt.Fprintf(w, "No theme matches that filter. %d are registered.\n", total)
		return
	}
	const cols = 4
	width := 0
	for _, id := range themes {
		if len(id) > width {
			width = len(id)
		}
	}
	for i, id := range themes {
		fmt.Fprintf(w, "  %-*s", width, id)
		if i%cols == cols-1 {
			fmt.Fprintln(w)
		}
	}
	if len(themes)%cols != 0 {
		fmt.Fprintln(w)
	}
	if truncated {
		fmt.Fprintf(w, "\n%d of %d matched; %d shown. Narrow it with --filter.\n", matched, total, len(themes))
	} else {
		fmt.Fprintf(w, "\n%d of %d registered themes.\n", matched, total)
	}
}

// printThemePalette writes one theme's colours with what each measures against
// its own background.
func printThemePalette(w io.Writer, p themePalette) {
	kind := "light"
	if p.Dark {
		kind = "dark"
	}
	fmt.Fprintf(w, "%s", p.ID)
	if p.DisplayName != "" && p.DisplayName != p.ID {
		fmt.Fprintf(w, "  (%s)", p.DisplayName)
	}
	fmt.Fprintf(w, "  %s, background %s\n\n", kind, p.Bg)

	width := 0
	for _, s := range p.Swatches {
		if len(s.Name) > width {
			width = len(s.Name)
		}
	}
	for _, s := range p.Swatches {
		mark := " "
		if !s.Passes {
			mark = "!"
		}
		fmt.Fprintf(w, " %s %-*s  %s  %5.2f:1  needs %.1f\n", mark, width, s.Name, s.Hex, s.Ratio, s.Floor)
	}
	if len(p.Illegible) > 0 {
		fmt.Fprintf(w, "\n%d of these do not clear their floor on this background: %s\n",
			len(p.Illegible), strings.Join(p.Illegible, ", "))
		fmt.Fprintln(w, "tuios lifts a border drawn from one of them; text printed in one stays as the theme wrote it.")
	}
}
