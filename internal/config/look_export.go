package config

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/pelletier/go-toml/v2"

	"github.com/Gaurav-Gosain/tuios/internal/theme"
)

// Look export.
//
// An export is the reverse of an apply: it reads the value in force of every
// look key and writes the ones that differ from the defaults as a look file.
// A theme or glyph set from the person's own directory travels inside the
// file, since the machine that applies it does not have that file. A built-in
// one is named, since every tuios has it. The result goes through ParseLook
// before it is returned, so `tuios look apply` takes it unchanged.

// MaxLookName is the longest look name, in characters. The tuios.dev gallery
// shows a name as the title of a card and keeps no longer one.
const MaxLookName = 60

// LookExportOptions says what an export writes besides the settings.
type LookExportOptions struct {
	Name   string
	Author string
	// All writes every look key, also the ones at their default.
	All bool
}

// LookExport is an exported look.
type LookExport struct {
	// Text is the look file.
	Text []byte
	// Keys is how many settings the file sets.
	Keys int
	// Theme and Glyphs are the ids of the theme and glyph set the file
	// carries, or empty.
	Theme, Glyphs string
	// Notes name what the export left out, and why.
	Notes []string
}

// LoadLookConfig reads the config in force at mainPath: config.toml, the
// files it includes and config.d. A config.toml that is not there reads as
// the defaults. Nothing is written.
func LoadLookConfig(mainPath string) (*UserConfig, error) {
	lc, err := loadLayered(mainPath, true)
	if err != nil {
		return nil, err
	}
	return parseLayered(lc)
}

// ExportLook writes the look of cfg as a look file.
func ExportLook(cfg *UserConfig, opts LookExportOptions) (*LookExport, error) {
	if cfg == nil {
		return nil, fmt.Errorf("no configuration is loaded")
	}
	for key, v := range map[string]string{"name": opts.Name, "author": opts.Author} {
		if hasControlChar(v) || len(v) > maxLookString {
			return nil, fmt.Errorf("the %s must be one line of at most %d bytes", key, maxLookString)
		}
	}
	if utf8.RuneCountInString(opts.Name) > MaxLookName {
		return nil, fmt.Errorf("the name must be at most %d characters", MaxLookName)
	}
	base, err := ParseUserConfig(nil)
	if err != nil {
		return nil, err
	}

	ex := &LookExport{}
	values := map[string]any{}
	for _, path := range LookKeys() {
		now, was := lookKeyText(cfg, path), lookKeyText(base, path)
		if now == was && !opts.All {
			continue
		}
		v, err := lookKeyValue(cfg, path)
		if err != nil {
			ex.Notes = append(ex.Notes, fmt.Sprintf("%s is left out: %v", path, err))
			continue
		}
		// An item a look cannot carry, such as a custom dock cell, is left
		// out here, so the file holds nothing that apply would drop.
		kept, dropped, err := lookItems(path, v)
		if err != nil {
			ex.Notes = append(ex.Notes, fmt.Sprintf("%s is left out: %v", path, err))
			continue
		}
		for _, item := range dropped {
			ex.Notes = append(ex.Notes, fmt.Sprintf("%s: %s is left out. A look cannot carry it", path, item))
		}
		if list, ok := kept.([]string); kept == nil || ok && len(list) == 0 && len(dropped) > 0 {
			// Nothing a look can carry is left. An empty list would draw
			// nothing where the person has something.
			continue
		}
		values[path] = kept
	}

	assets := map[string]any{}
	if err := ex.embedTheme(values, assets); err != nil {
		return nil, err
	}
	if err := ex.embedGlyphs(values, assets); err != nil {
		return nil, err
	}

	text, err := renderLookExport(opts, values, assets)
	if err != nil {
		return nil, err
	}
	if _, perr := ParseLook(text); perr != nil {
		// A value that passes in config.toml can still fail the look's own
		// check, which is stricter. Each key is tried alone, and the ones
		// that fail are left out and named.
		for _, path := range lookSortedKeys(values) {
			one, err := renderLookExport(LookExportOptions{}, map[string]any{path: values[path]}, assets)
			if err != nil {
				return nil, err
			}
			if _, err := ParseLook(one); err != nil {
				delete(values, path)
				ex.Notes = append(ex.Notes, fmt.Sprintf("%s is left out: %s", path, lookOneLine(err.Error())))
			}
		}
		if text, err = renderLookExport(opts, values, assets); err != nil {
			return nil, err
		}
		if _, err := ParseLook(text); err != nil {
			return nil, fmt.Errorf("tuios cannot make a look of this config: %w", err)
		}
	}
	ex.Text = text
	ex.Keys = len(values)
	return ex, nil
}

// embedTheme puts a theme of the person's own into the export as [theme].
// A theme_light of their own cannot travel too, since a look carries one
// theme, so it is left out.
func (ex *LookExport) embedTheme(values, assets map[string]any) error {
	id, _ := values["appearance.theme"].(string)
	if light, ok := values["appearance.theme_light"].(string); ok && light != "" && theme.IsThemeFile(light) {
		delete(values, "appearance.theme_light")
		ex.Notes = append(ex.Notes, fmt.Sprintf("appearance.theme_light is left out: %s is a theme file of yours, and a look carries one theme", light))
	}
	if id == "" || !theme.IsThemeFile(id) {
		return nil
	}
	data, err := theme.ExportTheme(id, false)
	if err != nil {
		delete(values, "appearance.theme")
		ex.Notes = append(ex.Notes, fmt.Sprintf("appearance.theme is left out: %v", err))
		return nil
	}
	table, err := lookAssetTable(data)
	if err != nil {
		return fmt.Errorf("theme %s: %w", id, err)
	}
	own := strings.TrimPrefix(id, LookAssetPrefix)
	table["id"] = own
	dropOwnName(table, own)
	assets["theme"] = table
	values["appearance.theme"] = own
	ex.Theme = own
	return nil
}

// embedGlyphs puts a glyph set of the person's own into the export as
// [glyphs].
func (ex *LookExport) embedGlyphs(values, assets map[string]any) error {
	id, _ := values["appearance.glyphs"].(string)
	if id == "" {
		return nil
	}
	data, ok, err := theme.ExportGlyphSet(id)
	if err != nil {
		delete(values, "appearance.glyphs")
		ex.Notes = append(ex.Notes, fmt.Sprintf("appearance.glyphs is left out: %v", err))
		return nil
	}
	if !ok {
		return nil
	}
	table, err := lookAssetTable(data)
	if err != nil {
		return fmt.Errorf("glyph set %s: %w", id, err)
	}
	// Computed on load, never read from a file.
	delete(table, "ascii")
	own := strings.TrimPrefix(id, LookAssetPrefix)
	table["id"] = own
	dropOwnName(table, own)
	assets["glyphs"] = table
	values["appearance.glyphs"] = own
	ex.Glyphs = own
	return nil
}

// dropOwnName removes a display name that only repeats the id. An installed
// asset is named look-<id> and keeps <id> as its display name, so without
// this an export of an applied look would gain a line the original lacked.
func dropOwnName(table map[string]any, own string) {
	if name, _ := table["display_name"].(string); name == own || name == LookAssetPrefix+own {
		delete(table, "display_name")
	}
}

// lookAssetTable reads a theme or glyph set file into the table a look holds.
func lookAssetTable(data []byte) (map[string]any, error) {
	table := map[string]any{}
	dec := json.NewDecoder(bytes.NewReader(data))
	if err := dec.Decode(&table); err != nil {
		return nil, err
	}
	return table, nil
}

// renderLookExport writes the look file: the format line, the name and the
// author, then the settings in their tables, then the theme and glyph set.
func renderLookExport(opts LookExportOptions, values, assets map[string]any) ([]byte, error) {
	var b bytes.Buffer
	fmt.Fprintf(&b, "look = %d\n", LookVersion)
	for _, kv := range [][2]string{{"name", opts.Name}, {"author", opts.Author}} {
		if kv[1] == "" {
			continue
		}
		line, err := toml.Marshal(map[string]string{kv[0]: kv[1]})
		if err != nil {
			return nil, err
		}
		b.Write(line)
	}
	root := map[string]any{}
	for path, v := range values {
		parts := strings.Split(path, ".")
		t := root
		for _, p := range parts[:len(parts)-1] {
			next, ok := t[p].(map[string]any)
			if !ok {
				next = map[string]any{}
				t[p] = next
			}
			t = next
		}
		t[parts[len(parts)-1]] = v
	}
	for _, key := range []string{"theme", "glyphs"} {
		if a, ok := assets[key]; ok {
			root[key] = a
		}
	}
	if len(root) > 0 {
		body, err := toml.Marshal(root)
		if err != nil {
			return nil, err
		}
		b.WriteString("\n")
		b.Write(body)
	}
	return b.Bytes(), nil
}

// lookKeyText is the value in force of a look key, as set-config takes it.
func lookKeyText(cfg *UserConfig, path string) string {
	if _, file := lookFileKeys[path]; file {
		return lookFileValueText(cfg, path)
	}
	v, _ := GetOptionValue(cfg, path)
	return v
}

// lookKeyValue is the value in force of a look key, typed as a look file
// holds it: bool, int64, string or []string.
func lookKeyValue(cfg *UserConfig, path string) (any, error) {
	if spec, file := lookFileKeys[path]; file {
		return lookFileValue(cfg, path, spec)
	}
	opt, ok := LookupOption(path)
	if !ok {
		return nil, fmt.Errorf("not an option")
	}
	text, _ := GetOptionValue(cfg, path)
	switch opt.Type {
	case OptionBool:
		return strconv.ParseBool(text)
	case OptionInt:
		n, err := strconv.ParseInt(strings.TrimSpace(text), 10, 64)
		if err != nil {
			return nil, fmt.Errorf("%q is not a whole number", text)
		}
		return n, nil
	}
	return text, nil
}

// lookFileValue reads a look key the registry does not carry.
func lookFileValue(cfg *UserConfig, path string, spec lookFileKey) (any, error) {
	field, ok := resolveOptionField(cfg, path)
	if !ok {
		return nil, fmt.Errorf("not an option")
	}
	if field.Kind() == reflect.Pointer {
		if field.IsNil() {
			// A key with no fixed default, such as active_underline,
			// follows another key while unset: there is nothing to write.
			if spec.Default == "" && spec.Type == OptionBool {
				return nil, nil
			}
			return lookDefaultValue(spec)
		}
		field = field.Elem()
	}
	switch field.Kind() {
	case reflect.Bool:
		return field.Bool(), nil
	case reflect.String:
		return field.String(), nil
	case reflect.Slice:
		items, ok := field.Interface().([]string)
		if !ok {
			return nil, fmt.Errorf("not a list of text")
		}
		return append([]string{}, items...), nil
	}
	return nil, fmt.Errorf("a value of kind %s", field.Kind())
}

// lookDefaultValue is the default of a look key the registry does not carry.
func lookDefaultValue(spec lookFileKey) (any, error) {
	switch spec.Type {
	case OptionBool:
		return strconv.ParseBool(spec.Default)
	case LookList:
		var items []string
		if spec.Default != "" {
			if err := json.Unmarshal([]byte(spec.Default), &items); err != nil {
				return nil, err
			}
		}
		return items, nil
	}
	return spec.Default, nil
}
