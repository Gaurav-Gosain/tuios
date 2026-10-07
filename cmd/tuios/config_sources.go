package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"strings"

	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// printWriteNote tells the person when a save went to another file than the
// one that holds the key, because that file is read-only.
func printWriteNote(note config.WriteNote) {
	if msg := note.Message(); msg != "" {
		fmt.Fprintln(os.Stderr, msg)
	}
}

// configFileRow is one file of the config, as tuios config files prints it.
type configFileRow struct {
	Path     string `json:"path"`
	Kind     string `json:"kind"`
	From     string `json:"included_from,omitempty"`
	ReadOnly bool   `json:"read_only"`
	Missing  bool   `json:"missing,omitempty"`
}

// configFilesReport is tuios config files --json.
type configFilesReport struct {
	Main     string          `json:"main"`
	Files    []configFileRow `json:"files"`
	Warnings []string        `json:"warnings"`
}

// loadConfigLayers reads the user's config with every file it brings in.
func loadConfigLayers() (*config.LayeredConfig, error) {
	path, err := config.GetConfigPath()
	if err != nil {
		return nil, fmt.Errorf("could not determine config path: %w", err)
	}
	lc, err := config.LoadLayered(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("there is no config file at %s. tuios uses the defaults", path)
	}
	return lc, err
}

// runConfigFiles prints the files the config is read from, in merge order.
func runConfigFiles(w io.Writer, asJSON bool) error {
	lc, err := loadConfigLayers()
	if err != nil {
		return err
	}
	report := configFilesReport{Main: lc.Main, Warnings: lc.Warnings}
	for _, l := range lc.Layers {
		report.Files = append(report.Files, configFileRow{
			Path: l.Path, Kind: l.Kind.String(), From: l.From,
			ReadOnly: !l.Writable(),
		})
	}
	for _, m := range lc.Missing {
		report.Files = append(report.Files, configFileRow{Path: m, Kind: "include", Missing: true})
	}
	if report.Warnings == nil {
		report.Warnings = []string{}
	}
	if asJSON {
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		return enc.Encode(report)
	}
	fmt.Fprintln(w, "Config files, from lowest to highest precedence:")
	for i, r := range report.Files {
		if r.Missing {
			continue
		}
		var marks []string
		marks = append(marks, r.Kind)
		if r.ReadOnly {
			marks = append(marks, "read-only")
		}
		fmt.Fprintf(w, "  %d. %s (%s)\n", i+1, r.Path, strings.Join(marks, ", "))
	}
	for _, warn := range report.Warnings {
		fmt.Fprintln(w, "Warning: "+warn)
	}
	if !lc.Layered {
		fmt.Fprintln(w, "config.toml has no include list and there is no config.d directory.")
	}
	return nil
}

// pruneOptions are the flags of tuios config prune, and where it asks.
type pruneOptions struct {
	dryRun bool
	yes    bool
	tty    bool
	in     io.Reader
}

// runConfigPrune removes the keys of config.toml that have their default
// value. A key that another file also sets then takes that file's value,
// which changes the config, so the command lists those keys first and asks.
func runConfigPrune(w io.Writer, opts pruneOptions) error {
	path, err := config.GetConfigPath()
	if err != nil {
		return fmt.Errorf("could not determine config path: %w", err)
	}
	plan, err := config.PruneConfig(path, true)
	if errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("there is no config file at %s. tuios uses the defaults", path)
	}
	if err != nil {
		return err
	}
	if len(plan.Keys) == 0 {
		fmt.Fprintln(w, "config.toml has no key with its default value.")
		return nil
	}
	if len(plan.Uncovered) > 0 {
		fmt.Fprintln(w, "After the prune, these keys take the value of another file:")
		for _, o := range plan.Uncovered {
			fmt.Fprintf(w, "  %s  %s\n", o.Key, o.File)
		}
	}
	if !opts.dryRun && len(plan.Uncovered) > 0 && !opts.yes {
		if !opts.tty {
			return fmt.Errorf("the prune changes %d keys. Run tuios config prune --dry-run to see them, then run it again with --yes", len(plan.Uncovered))
		}
		fmt.Fprint(w, "Remove the keys? (yes/no): ")
		var answer string
		_, _ = fmt.Fscanln(opts.in, &answer)
		if a := strings.ToLower(strings.TrimSpace(answer)); a != "yes" && a != "y" {
			fmt.Fprintln(w, "Nothing was removed.")
			return nil
		}
	}
	res := plan
	if !opts.dryRun {
		if res, err = config.PruneConfig(path, false); err != nil {
			return err
		}
	}
	verb := "Removed"
	if opts.dryRun {
		verb = "Would remove"
	}
	fmt.Fprintf(w, "%s %d keys that have their default value from %s:\n", verb, len(res.Keys), path)
	for _, k := range res.Keys {
		fmt.Fprintln(w, "  "+k)
	}
	return nil
}

// runConfigOrigin prints the file each set key comes from. With a key, it
// prints that key and the keys under it only.
func runConfigOrigin(w io.Writer, key string, asJSON bool) error {
	lc, err := loadConfigLayers()
	if err != nil {
		return err
	}
	all := lc.Origins()
	var rows []config.KeyOrigin
	prefix := strings.Join(config.ParseKeyPath(key), ".")
	for _, o := range all {
		if key == "" || o.Key == prefix || strings.HasPrefix(o.Key, prefix+".") || strings.HasPrefix(o.Key, prefix+"[") {
			rows = append(rows, o)
		}
	}
	if asJSON {
		type row struct {
			Key    string   `json:"key"`
			File   string   `json:"file"`
			Hidden []string `json:"hidden,omitempty"`
		}
		out := make([]row, 0, len(rows))
		for _, o := range rows {
			out = append(out, row{Key: o.Key, File: o.File, Hidden: o.Hidden})
		}
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		return enc.Encode(out)
	}
	if len(rows) == 0 {
		if key != "" {
			fmt.Fprintf(w, "No config file sets %s. It has its default value.\n", key)
			return nil
		}
		fmt.Fprintln(w, "No config file sets a key. Every setting has its default value.")
		return nil
	}
	width := 0
	for _, o := range rows {
		width = max(width, len(o.Key))
	}
	for _, o := range rows {
		line := fmt.Sprintf("%-*s  %s", width, o.Key, lc.DisplayPath(o.File))
		if len(o.Hidden) > 0 {
			hidden := make([]string, 0, len(o.Hidden))
			for _, h := range o.Hidden {
				hidden = append(hidden, lc.DisplayPath(h))
			}
			line += "  (also set in " + strings.Join(hidden, ", ") + ")"
		}
		fmt.Fprintln(w, line)
	}
	return nil
}
