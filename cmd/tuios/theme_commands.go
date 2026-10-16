package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
)

// runImportTheme converts a terminal's colour scheme into a tuios theme.
//
// It runs in this process rather than over the socket because the file it reads
// is the caller's, at the caller's path, and a daemon on the other end of a
// socket has no business opening it. What it writes lands in the themes
// directory, which the daemon re-reads on demand, so the imported theme is
// selectable straight afterwards without a restart.
func runImportTheme(path, id string, jsonOutput bool) error {
	t, format, err := theme.Import(path, id)
	if err != nil {
		return err
	}
	written, err := theme.WriteTheme(t)
	if err != nil {
		return err
	}
	pal, _ := theme.Describe(t.ID)

	if jsonOutput {
		out, err := json.MarshalIndent(map[string]any{
			"id":      t.ID,
			"format":  string(format),
			"file":    written,
			"palette": pal,
		}, "", "  ")
		if err != nil {
			return err
		}
		fmt.Println(string(out))
		return nil
	}

	fmt.Printf("read %s as a %s colour scheme\nwrote %s\n\n", path, format, written)
	printThemePalette(os.Stdout, themePalette{
		ID: pal.ID, DisplayName: pal.DisplayName, Dark: pal.Dark, Bg: pal.Bg,
		Swatches: toCLISwatches(pal.Swatches), Illegible: pal.Illegible,
	})
	fmt.Printf("\nSelect it with: tuios set-config appearance.theme %s\n", t.ID)
	return nil
}

// toCLISwatches converts the theme package's rows into the shape the printer
// shares with the socket path, which decodes them from JSON.
func toCLISwatches(in []theme.Swatch) []themeSwatch {
	out := make([]themeSwatch, len(in))
	for i, s := range in {
		out[i] = themeSwatch{Name: s.Name, Hex: s.Hex, Ratio: s.Ratio, Floor: s.Floor, Passes: s.Passes}
	}
	return out
}

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
		Omarchy   *struct {
			Installed bool   `json:"installed"`
			Dir       string `json:"dir"`
			Theme     string `json:"theme"`
			Followed  bool   `json:"followed"`
		} `json:"omarchy"`
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
	active := orNone(res.Active)
	if res.Active == "" && res.Omarchy != nil && res.Omarchy.Followed {
		active = "omarchy"
	}
	fmt.Fprintf(w, "\nactive: %s", active)
	if res.Source != "" {
		fmt.Fprintf(w, " (%s)", res.Source)
	}
	fmt.Fprintf(w, "\nthemes dir: %s\n", res.ThemesDir)
	if o := res.Omarchy; o != nil {
		switch {
		case !o.Installed:
			fmt.Fprintln(w, "omarchy: not installed on this machine. tuios uses the colours of your terminal.")
		case o.Followed:
			fmt.Fprintf(w, "omarchy: %s, from %s. tuios follows it.\n", o.Theme, o.Dir)
		case res.Active == "none":
			fmt.Fprintf(w, "omarchy: %s, from %s. tuios does not follow it, because the theme is none.\n", o.Theme, o.Dir)
		default:
			fmt.Fprintf(w, "omarchy: %s, from %s. tuios does not follow it, because a theme is set.\n", o.Theme, o.Dir)
		}
	}
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
		fmt.Fprintf(w, "\n%d of %d matched, %d shown. Narrow it with --filter.\n", matched, total, len(themes))
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
		fmt.Fprintln(w, "tuios adjusts a border drawn in one of them so it stays visible. Text printed in one stays as the theme wrote it.")
	}
}

// runThemeReload reads the theme and glyph files again in every tuios client
// on this machine. Each client's config watcher follows the stamp file in the
// config directory, so writing it reaches clients of every kind, the ssh and
// web servers' included, with no protocol of its own. The stamp is left in
// place and written with the time: kqueue (macOS, the BSDs) sees a new file
// only by listing the directory, and one removed at once is gone before the
// listing. The files are read here too, so the command can say which ones fail.
func runThemeReload(jsonOutput bool) error {
	loaded, problems := theme.ReloadCustomThemes()
	path, err := config.GetConfigPath()
	if err != nil {
		return err
	}
	stamp := filepath.Join(filepath.Dir(path), config.ThemeReloadStamp)
	if err := os.WriteFile(stamp, []byte(time.Now().Format(time.RFC3339Nano)), 0o600); err != nil {
		return fmt.Errorf("ask the clients to reload: %w", err)
	}

	if jsonOutput {
		out, err := json.MarshalIndent(map[string]any{"themes": loaded, "problems": problems}, "", "  ")
		if err != nil {
			return err
		}
		fmt.Println(string(out))
		return nil
	}
	fmt.Printf("Read %d theme files. Every tuios client on this machine reloads them.\n", len(loaded))
	for _, p := range problems {
		fmt.Println("problem: " + p)
	}
	return nil
}

// runThemeExport prints a theme as a theme file or a base16 scheme.
func runThemeExport(id, format string, full bool) error {
	var out []byte
	var err error
	switch format {
	case "", "tuios":
		out, err = theme.ExportTheme(id, full)
	case "base16":
		if full {
			return fmt.Errorf("--full is for the tuios format. A base16 scheme always has all its colours")
		}
		out, err = theme.ExportBase16(id)
	default:
		return fmt.Errorf("unknown format %q. Use tuios or base16", format)
	}
	if err != nil {
		return err
	}
	_, err = os.Stdout.Write(out)
	return err
}
