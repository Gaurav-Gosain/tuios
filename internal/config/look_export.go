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

	"github.com/Gaurav-Gosain/tuios/internal/errfmt"
	"github.com/Gaurav-Gosain/tuios/internal/jsonx"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
)

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
		return nil, errfmt.Errorf("no configuration is loaded")
	}
	for key, v := range map[string]string{"name": opts.Name, "author": opts.Author} {
		if hasControlChar(v) || len(v) > maxLookString {
			return nil, errfmt.Errorf("the %s must be one line of at most %d bytes", key, maxLookString)
		}
	}
	if utf8.RuneCountInString(opts.Name) > MaxLookName {
		return nil, errfmt.Errorf("the name must be at most %d characters", MaxLookName)
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
		kept, dropped, err := lookItems(path, v)
		if err != nil {
			ex.Notes = append(ex.Notes, fmt.Sprintf("%s is left out: %v", path, err))
			continue
		}
		for _, item := range dropped {
			ex.Notes = append(ex.Notes, fmt.Sprintf("%s: %s is left out. A look cannot carry it", path, item))
		}
		if list, ok := kept.([]string); kept == nil || ok && len(list) == 0 && len(dropped) > 0 {
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
			return nil, errfmt.Errorf("tuios cannot make a look of this config: %w", err)
		}
	}
	ex.Text = text
	ex.Keys = len(values)
	return ex, nil
}

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
		return errfmt.Errorf("theme %s: %w", id, err)
	}
	own := strings.TrimPrefix(id, LookAssetPrefix)
	table["id"] = own
	dropOwnName(table, own)
	assets["theme"] = table
	values["appearance.theme"] = own
	ex.Theme = own
	return nil
}

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
		return errfmt.Errorf("glyph set %s: %w", id, err)
	}
	delete(table, "ascii")
	own := strings.TrimPrefix(id, LookAssetPrefix)
	table["id"] = own
	dropOwnName(table, own)
	assets["glyphs"] = table
	values["appearance.glyphs"] = own
	ex.Glyphs = own
	return nil
}

func dropOwnName(table map[string]any, own string) {
	if name, _ := table["display_name"].(string); name == own || name == LookAssetPrefix+own {
		delete(table, "display_name")
	}
}

func lookAssetTable(data []byte) (map[string]any, error) {
	table := map[string]any{}
	dec := json.NewDecoder(bytes.NewReader(data))
	if err := dec.Decode(&table); err != nil {
		return nil, err
	}
	return table, nil
}

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

func lookKeyText(cfg *UserConfig, path string) string {
	if _, file := lookFileKeys[path]; file {
		return lookFileValueText(cfg, path)
	}
	v, _ := GetOptionValue(cfg, path)
	return v
}

func lookKeyValue(cfg *UserConfig, path string) (any, error) {
	if spec, file := lookFileKeys[path]; file {
		return lookFileValue(cfg, path, spec)
	}
	opt, ok := LookupOption(path)
	if !ok {
		return nil, errfmt.Errorf("not an option")
	}
	text, _ := GetOptionValue(cfg, path)
	switch opt.Type {
	case OptionBool:
		return strconv.ParseBool(text)
	case OptionInt:
		n, err := strconv.ParseInt(strings.TrimSpace(text), 10, 64)
		if err != nil {
			return nil, errfmt.Errorf("%q is not a whole number", text)
		}
		return n, nil
	}
	return text, nil
}

func lookFileValue(cfg *UserConfig, path string, spec lookFileKey) (any, error) {
	field, ok := resolveOptionField(cfg, path)
	if !ok {
		return nil, errfmt.Errorf("not an option")
	}
	if field.Kind() == reflect.Pointer {
		if field.IsNil() {
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
			return nil, errfmt.Errorf("not a list of text")
		}
		return append([]string{}, items...), nil
	}
	return nil, errfmt.Errorf("a value of kind %s", field.Kind())
}

func lookDefaultValue(spec lookFileKey) (any, error) {
	switch spec.Type {
	case OptionBool:
		return strconv.ParseBool(spec.Default)
	case LookList:
		var items []string
		if spec.Default != "" {
			if err := jsonx.Unmarshal([]byte(spec.Default), &items); err != nil {
				return nil, err
			}
		}
		return items, nil
	}
	return spec.Default, nil
}
