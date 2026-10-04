package config

import (
	"fmt"
	"slices"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

// PluginsConfig is the [plugins] table: which herdr plugins tuios runs, and
// where it finds plugins besides the places it looks by default. See
// internal/herdrplugin.
//
// A plugin is found and listed without being enabled. Nothing it names runs
// until its id is in Enabled. herdr's own enabled flag is not read: a plugin
// herdr runs is off in tuios until the person enables it here.
type PluginsConfig struct {
	// Enabled are the ids of the plugins tuios runs.
	Enabled []string `toml:"enabled,omitempty"`
	// Dirs are more plugin folders, or manifests, to list. tuios plugins
	// link adds to it.
	Dirs []string `toml:"dirs,omitempty"`
}

// pluginsTable is the path of the [plugins] table.
var pluginsTable = []string{"plugins"}

// PluginsInFile reads the [plugins] table of the file at path. A missing
// file is an empty table.
func PluginsInFile(path string) (PluginsConfig, error) {
	data, err := readConfigForEdit(path)
	if err != nil {
		return PluginsConfig{}, err
	}
	var doc struct {
		Plugins PluginsConfig `toml:"plugins"`
	}
	if err := toml.Unmarshal(data, &doc); err != nil {
		return PluginsConfig{}, fmt.Errorf("config.toml has an error: %w", err)
	}
	return doc.Plugins, nil
}

// SetPluginEnabledInFile adds id to [plugins] enabled, or removes it, and
// rewrites only the enabled key of the file at path. Every comment and every
// other table stays where it is. It reports whether the file changed.
func SetPluginEnabledInFile(path, id string, on bool) (bool, error) {
	cur, err := PluginsInFile(path)
	if err != nil {
		return false, err
	}
	list := slices.Clone(cur.Enabled)
	has := slices.Contains(list, id)
	switch {
	case on && has, !on && !has:
		return false, nil
	case on:
		list = append(list, id)
	default:
		list = slices.DeleteFunc(list, func(s string) bool { return s == id })
	}
	return true, setPluginsKey(path, "enabled", list)
}

// AddPluginDirInFile adds dir to [plugins] dirs. It reports whether the
// file changed.
func AddPluginDirInFile(path, dir string) (bool, error) {
	cur, err := PluginsInFile(path)
	if err != nil {
		return false, err
	}
	if slices.Contains(cur.Dirs, dir) {
		return false, nil
	}
	return true, setPluginsKey(path, "dirs", append(slices.Clone(cur.Dirs), dir))
}

// RemovePluginDirInFile removes dir from [plugins] dirs. It reports whether
// the file changed.
func RemovePluginDirInFile(path, dir string) (bool, error) {
	cur, err := PluginsInFile(path)
	if err != nil {
		return false, err
	}
	if !slices.Contains(cur.Dirs, dir) {
		return false, nil
	}
	return true, setPluginsKey(path, "dirs", slices.DeleteFunc(slices.Clone(cur.Dirs), func(s string) bool { return s == dir }))
}

// setPluginsKey writes key = [values] in the [plugins] table, replacing the
// lines the key had, adding the key after the header, or adding the table at
// the end of the file.
func setPluginsKey(path, key string, values []string) error {
	data, err := readConfigForEdit(path)
	if err != nil {
		return err
	}
	parts := make([]string, len(values))
	for i, v := range values {
		parts[i] = tomlString(v)
	}
	line := key + " = [" + strings.Join(parts, ", ") + "]"
	lines := splitLines(string(data))
	start, end, found := findTableBlock(lines, pluginsTable)
	var out []string
	if !found {
		out = append(out, trimTrailingBlank(lines)...)
		if len(out) > 0 {
			out = append(out, "")
		}
		out = append(out, "[plugins]", line)
		return writePluginsEdit(out, path, key, values)
	}
	from, to := keySpan(lines[start+1:end], key)
	if from < 0 {
		out = append(out, lines[:start+1]...)
		out = append(out, line)
		out = append(out, lines[start+1:]...)
	} else {
		from, to = from+start+1, to+start+1
		out = append(out, lines[:from]...)
		out = append(out, line)
		out = append(out, lines[to:]...)
	}
	return writePluginsEdit(out, path, key, values)
}

// writePluginsEdit writes an edited file only when it reads back with the
// key at the values asked for. A file that sets plugins in another form,
// such as plugins.enabled = [...] at the top or an inline table, is not
// one the line edit knows, and writing it could break the file.
func writePluginsEdit(lines []string, path, key string, values []string) error {
	data := []byte(joinLines(lines))
	var doc struct {
		Plugins PluginsConfig `toml:"plugins"`
	}
	if err := toml.Unmarshal(data, &doc); err != nil {
		return fmt.Errorf("config.toml sets [plugins] in a form tuios cannot edit. Edit the [plugins] table in %s by hand: %w", path, err)
	}
	got := doc.Plugins.Enabled
	if key == "dirs" {
		got = doc.Plugins.Dirs
	}
	if !slices.Equal(got, values) && !(len(got) == 0 && len(values) == 0) {
		return fmt.Errorf("config.toml sets [plugins] in a form tuios cannot edit. Edit the [plugins] table in %s by hand", path)
	}
	return writeConfigBytes(data, path)
}

// keySpan finds the lines key = [...] takes in a table body, an array that
// may run over several lines. from is -1 when the key is not there.
func keySpan(body []string, key string) (from, to int) {
	for i, l := range body {
		k, rest, ok := strings.Cut(strings.TrimSpace(l), "=")
		if !ok || unquoteKey(k) != key {
			continue
		}
		depth := 0
		for j := i; j < len(body); j++ {
			text := rest
			if j > i {
				text = body[j]
			}
			depth += bracketDepth(text)
			if depth <= 0 {
				return i, j + 1
			}
		}
		return i, len(body)
	}
	return -1, -1
}

// bracketDepth counts [ minus ] outside strings and comments in one line.
func bracketDepth(s string) int {
	depth := 0
	var quote byte
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case quote != 0:
			if c == '\\' && quote == '"' {
				i++
			} else if c == quote {
				quote = 0
			}
		case c == '"' || c == '\'':
			quote = c
		case c == '#':
			return depth
		case c == '[':
			depth++
		case c == ']':
			depth--
		}
	}
	return depth
}
