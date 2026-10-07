package config

import (
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

// A config split over several files.
//
// config.toml may name other files in a top-level include list, and every
// *.toml file in a config.d directory beside config.toml is read too. The
// files are merged into one document before anything else sees it, so the rest
// of the package parses, fills and validates one config the way it always did.
//
// # Merge order
//
// Lowest precedence first:
//
//  1. the files config.toml includes, in the order the list gives them (a file
//     that includes others has those merged before itself)
//  2. the config.d files, in lexical order of their names
//  3. config.toml itself
//
// config.toml is last because it is the file tuios writes. It holds only what
// the person changed: tuios writes a first-start config.toml with no settings,
// and every save adds the keys that changed and nothing else. So an included
// file is hidden only where the person chose a value of their own.
//
// # Merge rules
//
//   - Tables merge key by key, at every depth. [hosts.NAME] tables are tables,
//     so hosts merge by name.
//   - A scalar from a later file replaces the earlier one.
//   - An array of plain values replaces the earlier array whole.
//   - An array of tables, such as [[keybindings.command]], merges entry by
//     entry and is never replaced whole. A later entry matches an earlier one
//     by name when both have a name, else by key. A matched entry merges key by
//     key. An entry that matches nothing is added at the end. An entry with
//     disabled = true removes the entry it matches and is not itself kept.
//
// # What is not an error
//
// An include that names a file that is not there is a warning, so one machine
// can include a file only it has. A name that starts with ? is optional, and
// its absence is not even a warning. A cycle is a warning, and the file that
// closes it is skipped. A file reached twice is merged once. A file that is
// there and does not parse is an error, as config.toml is, and so is an
// include that names a directory.

// IncludeKey is the top-level key that lists the files a config file includes.
const IncludeKey = "include"

// DropInDirName is the directory beside config.toml whose *.toml files are
// merged in.
const DropInDirName = "config.d"

// tombstoneKey marks an array-of-tables entry that removes the entry it
// matches.
const tombstoneKey = "disabled"

// maxIncludeDepth bounds a chain of includes. A cycle is caught by the stack
// check before this; the bound is for a chain that is merely absurd.
const maxIncludeDepth = 16

// LayerKind says how a file came into the config.
type LayerKind int

const (
	// LayerMain is config.toml.
	LayerMain LayerKind = iota
	// LayerInclude is a file an include list named.
	LayerInclude
	// LayerDropIn is a file in config.d.
	LayerDropIn
)

// String is the kind as a word for a listing.
func (k LayerKind) String() string {
	switch k {
	case LayerInclude:
		return "include"
	case LayerDropIn:
		return "config.d"
	default:
		return "main"
	}
}

// ConfigLayer is one file of the config.
type ConfigLayer struct {
	// Path is the file as named, made absolute. A symlink is not resolved, so
	// the listing shows the path the person wrote.
	Path string
	// Real is Path with its symlinks resolved. The watcher follows both.
	Real string
	Kind LayerKind
	// From is the file whose include list named this one, empty for the main
	// file and the config.d files.
	From string
	// Data is the file as read. It is nil for a main file that is not there
	// yet.
	Data []byte
	// Values is the file parsed, without its include key, and with the
	// relative file paths of an included file made absolute.
	Values map[string]any
}

// Writable reports whether tuios can write this file. A file is read-only when
// it has no owner write bit, or when no file can be made beside it, which is
// the case for a link into the Nix store and for a file in a read-only mount.
//
// It is asked only when a write is about to happen: the check makes and
// removes a temporary file, which is not something a plain load should do.
func (l ConfigLayer) Writable() bool { return fileWritable(l.Path) }

// LayeredConfig is the config as every file that makes it up.
type LayeredConfig struct {
	// Main is the path of config.toml.
	Main string
	// Layers are the files in merge order, lowest precedence first. The main
	// file is last.
	Layers []ConfigLayer
	// Missing are the included files that were not there, as resolved paths,
	// optional ones included. The watcher follows them.
	Missing []string
	// Warnings say what was skipped and why.
	Warnings []string
	// Layered is true when config.toml has an include key or a config.d
	// directory exists.
	Layered bool
	// DropInDir is the config.d directory, whether or not it exists.
	DropInDir string
	// MainMissing is true when config.toml is not there yet. Only a load for a
	// write accepts that.
	MainMissing bool

	merged map[string]any
}

// LoadLayered reads config.toml at mainPath and every file it brings in. A
// main file that cannot be read is returned as the error os.ReadFile gave, so
// a caller can test it with errors.Is(err, fs.ErrNotExist).
func LoadLayered(mainPath string) (*LayeredConfig, error) {
	return loadLayered(mainPath, false)
}

// loadLayered is LoadLayered. With allowMissing, a config.toml that is not
// there is an empty one, so a first write can make it.
func loadLayered(mainPath string, allowMissing bool) (*LayeredConfig, error) {
	mainPath = absPath(mainPath)
	lc := &LayeredConfig{Main: mainPath, DropInDir: filepath.Join(filepath.Dir(mainPath), DropInDirName)}
	data, err := os.ReadFile(mainPath) //nolint:gosec // the user's own config file
	if err != nil {
		if !allowMissing || !errors.Is(err, fs.ErrNotExist) {
			return nil, err
		}
		lc.MainMissing = true
		data = nil
	}
	if err == nil {
		if info, serr := os.Stat(mainPath); serr == nil && info.IsDir() {
			return nil, fmt.Errorf("%s is a directory. The config must be a file", mainPath)
		}
	}
	values, err := parseLayer(data)
	if err != nil {
		return nil, fmt.Errorf("failed to parse config file: %w", err)
	}
	_, hasInclude := values[IncludeKey]

	l := &layerLoader{lc: lc, seen: map[string]bool{}}
	l.seen[realPath(mainPath)] = true
	stack := []string{realPath(mainPath)}
	l.warnNested(mainPath, values)
	if err := l.includes(mainPath, values, stack, 0); err != nil {
		return nil, err
	}

	dropIns, dirExists := dropInFiles(lc.DropInDir)
	for _, p := range dropIns {
		if err := l.visit(p, false, LayerDropIn, "", stack, 0); err != nil {
			return nil, err
		}
	}

	delete(values, IncludeKey)
	lc.Layers = append(lc.Layers, ConfigLayer{Path: mainPath, Real: realPath(mainPath), Kind: LayerMain, Data: data, Values: values})
	lc.Layered = hasInclude || dirExists
	return lc, nil
}

// layerLoader walks the include graph.
type layerLoader struct {
	lc   *LayeredConfig
	seen map[string]bool
}

// includes visits the files one file's include list names.
func (l *layerLoader) includes(from string, values map[string]any, stack []string, depth int) error {
	list, err := includeList(values[IncludeKey])
	if err != nil {
		return fmt.Errorf("%s: %w", from, err)
	}
	for _, name := range list {
		optional := false
		if rest, ok := strings.CutPrefix(name, "?"); ok {
			name, optional = rest, true
		}
		if err := l.visit(resolveInclude(from, name), optional, LayerInclude, from, stack, depth+1); err != nil {
			return err
		}
	}
	return nil
}

// visit reads one file, merges its own includes first, and then adds it.
func (l *layerLoader) visit(path string, optional bool, kind LayerKind, from string, stack []string, depth int) error {
	real := realPath(path)
	if slices.Contains(stack, real) {
		l.lc.Warnings = append(l.lc.Warnings, fmt.Sprintf("%s includes %s, which includes it again. tuios skips the second include.", l.show(from), l.show(path)))
		return nil
	}
	if l.seen[real] {
		return nil
	}
	if depth > maxIncludeDepth {
		l.lc.Warnings = append(l.lc.Warnings, fmt.Sprintf("The include chain to %s is more than %d files deep. tuios skips it.", l.show(path), maxIncludeDepth))
		return nil
	}
	if info, err := os.Stat(path); err == nil && info.IsDir() {
		return fmt.Errorf("%s includes %s, which is a directory. An include names one file. Put a directory of files in %s", l.show(from), l.show(path), DropInDirName)
	}
	data, err := os.ReadFile(path) //nolint:gosec // a file the user's own config names
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			l.lc.Missing = append(l.lc.Missing, path)
			if !optional {
				l.lc.Warnings = append(l.lc.Warnings, fmt.Sprintf("%s includes %s, which does not exist. tuios skips it.", l.show(from), l.show(path)))
			}
			return nil
		}
		return fmt.Errorf("failed to read %s: %w", path, err)
	}
	values, err := parseLayer(data)
	if err != nil {
		return fmt.Errorf("failed to parse %s: %w", path, err)
	}
	l.seen[real] = true
	l.warnNested(path, values)
	if err := l.includes(path, values, append(slices.Clone(stack), real), depth); err != nil {
		return err
	}
	delete(values, IncludeKey)
	resolveRelativePaths(values, filepath.Dir(path))
	l.lc.Layers = append(l.lc.Layers, ConfigLayer{Path: path, Real: real, Kind: kind, From: from, Data: data, Values: values})
	return nil
}

// nestedIncludeAllowed are the tables where a key named include is a setting
// of its own, not a list of files.
var nestedIncludeAllowed = map[string]bool{"tailscale": true}

// warnNested warns about an include key inside a table. TOML puts every key
// after a [table] header into that table, so an include written below the
// first header is a setting of that table and includes nothing.
func (l *layerLoader) warnNested(path string, values map[string]any) {
	var walk func(m map[string]any, prefix []string)
	walk = func(m map[string]any, prefix []string) {
		for _, k := range slices.Sorted(maps.Keys(m)) {
			sub, ok := m[k].(map[string]any)
			if !ok {
				continue
			}
			p := append(slices.Clone(prefix), k)
			if _, has := sub[IncludeKey]; has && !nestedIncludeAllowed[strings.Join(p, ".")] {
				l.lc.Warnings = append(l.lc.Warnings, fmt.Sprintf("%s has include in [%s], so it includes nothing. Put include at the top of the file, before the first table.", l.show(path), strings.Join(p, ".")))
			}
			walk(sub, p)
		}
	}
	walk(values, nil)
}

// show names a file in a warning, the way DisplayPath does.
func (l *layerLoader) show(p string) string {
	if p == "" {
		return DropInDirName
	}
	return displayPath(l.lc.Main, p)
}

// pathKeys are the settings whose value is a file or directory. A relative
// value in an included file is relative to that file, the way a relative
// include is. A * matches any one key.
var pathKeys = [][]string{
	{"plugins", "dirs"},
	{"screenshot", "font_file"},
	{"tailscale", "socket"},
	{"notify", "*", "token_file"},
	{"notify", "*", "user_file"},
	{"notifications", "*", "sounds", "*"},
}

// resolveRelativePaths makes the relative paths under pathKeys absolute
// against dir. A path that starts with ~ is left alone: the reader expands it.
func resolveRelativePaths(values map[string]any, dir string) {
	abs := func(s string) string {
		if s == "" || filepath.IsAbs(s) || strings.HasPrefix(s, "~") {
			return s
		}
		return filepath.Join(dir, s)
	}
	var walk func(v any, pattern []string) any
	walk = func(v any, pattern []string) any {
		if len(pattern) == 0 {
			switch v := v.(type) {
			case string:
				return abs(v)
			case []any:
				for i, e := range v {
					if s, ok := e.(string); ok {
						v[i] = abs(s)
					}
				}
			}
			return v
		}
		m, ok := v.(map[string]any)
		if !ok {
			return v
		}
		for k, sub := range m {
			if pattern[0] == "*" || pattern[0] == k {
				m[k] = walk(sub, pattern[1:])
			}
		}
		return m
	}
	for _, p := range pathKeys {
		walk(values, p)
	}
}

// parseLayer parses one file into a map.
func parseLayer(data []byte) (map[string]any, error) {
	values := map[string]any{}
	if err := toml.Unmarshal(data, &values); err != nil {
		return nil, err
	}
	return values, nil
}

// includeList reads an include value: a list of strings, or one string.
func includeList(v any) ([]string, error) {
	switch v := v.(type) {
	case nil:
		return nil, nil
	case string:
		return []string{v}, nil
	case []any:
		out := make([]string, 0, len(v))
		for _, e := range v {
			s, ok := e.(string)
			if !ok {
				return nil, fmt.Errorf("include must be a list of file paths")
			}
			out = append(out, s)
		}
		return out, nil
	default:
		return nil, fmt.Errorf("include must be a list of file paths")
	}
}

// resolveInclude makes an include path absolute: ~ is the home directory, and
// a relative path is relative to the directory of the file that names it.
func resolveInclude(from, name string) string {
	name = expandHome(name)
	if !filepath.IsAbs(name) {
		name = filepath.Join(filepath.Dir(from), name)
	}
	return filepath.Clean(name)
}

// expandHome replaces a leading ~ with the home directory.
func expandHome(p string) string {
	if p != "~" && !strings.HasPrefix(p, "~/") && !strings.HasPrefix(p, `~\`) {
		return p
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return p
	}
	return filepath.Join(home, p[1:])
}

// absPath is p made absolute and clean. A path that cannot be made absolute
// is kept as given.
func absPath(p string) string {
	if a, err := filepath.Abs(p); err == nil {
		return a
	}
	return filepath.Clean(p)
}

// realPath is p with its symlinks resolved, for telling two names of one file
// apart from two files. A path that does not resolve is used as it is.
func realPath(p string) string {
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r
	}
	return p
}

// dropInFiles lists the *.toml files in dir in lexical order, and reports
// whether dir exists.
func dropInFiles(dir string) ([]string, bool) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		info, serr := os.Stat(dir)
		return nil, serr == nil && info.IsDir()
	}
	var out []string
	for _, e := range entries {
		name := e.Name()
		if strings.HasPrefix(name, ".") || !strings.HasSuffix(name, ".toml") {
			continue
		}
		p := filepath.Join(dir, name)
		if info, err := os.Stat(p); err != nil || info.IsDir() {
			continue
		}
		out = append(out, p)
	}
	slices.Sort(out)
	return out, true
}

// fileWritable reports whether a write to path can land. It is the same write
// writeConfigBytes makes: a temporary file beside the real one, renamed over
// it.
func fileWritable(path string) bool {
	target := realPath(path)
	info, err := os.Stat(target)
	if err == nil && info.Mode().Perm()&0o200 == 0 {
		return false
	}
	// A directory that is not there yet is made by the write, so the
	// question goes to the nearest one that is.
	dir := filepath.Dir(target)
	for {
		if _, err := os.Stat(dir); err == nil || filepath.Dir(dir) == dir {
			break
		}
		dir = filepath.Dir(dir)
	}
	probe, err := os.CreateTemp(dir, ".tuios-write-check-*")
	if err != nil {
		return false
	}
	_ = probe.Close()
	_ = os.Remove(probe.Name())
	return true
}

// Merged is every layer merged into one table, in merge order, with the
// tombstones applied.
func (lc *LayeredConfig) Merged() map[string]any {
	if lc.merged == nil {
		out := map[string]any{}
		for _, layer := range lc.Layers {
			mergeTables(out, layer.Values)
		}
		stripTombstones(out)
		lc.merged = out
	}
	return lc.merged
}

// Bytes is the merged config as one TOML document. When the config is
// config.toml alone, with no tombstone in it, it is the file exactly as read,
// so a single-file config takes the same path it always did.
func (lc *LayeredConfig) Bytes() ([]byte, error) {
	main := lc.mainLayer()
	if !lc.Layered && !hasTombstone(main.Values) {
		return main.Data, nil
	}
	data, err := toml.Marshal(lc.Merged())
	if err != nil {
		return nil, fmt.Errorf("failed to merge config files: %w", err)
	}
	return data, nil
}

func (lc *LayeredConfig) mainLayer() *ConfigLayer {
	return &lc.Layers[len(lc.Layers)-1]
}

// Origin is the file the value at key comes from: the last file in merge order
// that sets it. key is a dotted path such as appearance.theme. ok is false when
// no file sets it, which means the value is the built-in default.
func (lc *LayeredConfig) Origin(key []string) (string, bool) {
	for i := len(lc.Layers) - 1; i >= 0; i-- {
		if _, ok := lookupPath(lc.Layers[i].Values, key); ok {
			return lc.Layers[i].Path, true
		}
	}
	return "", false
}

// KeyOrigin is one set key and the files that set it.
type KeyOrigin struct {
	// Key is the dotted path. An entry of an array of tables is written
	// with its name, or its key, in brackets, such as
	// keybindings.command[ctrl+g].
	Key string
	// File is the file whose value is in force.
	File string
	// Hidden are the earlier files that also set it, whose values lose.
	Hidden []string
}

// Origins lists every key some file sets, in key order, with the file it comes
// from.
func (lc *LayeredConfig) Origins() []KeyOrigin {
	var leaves [][]string
	collectLeaves(lc.Merged(), nil, &leaves)
	out := make([]KeyOrigin, 0, len(leaves))
	for _, p := range leaves {
		ko := KeyOrigin{Key: displayKey(p)}
		for i := len(lc.Layers) - 1; i >= 0; i-- {
			if _, ok := lookupPath(lc.Layers[i].Values, p); !ok {
				continue
			}
			if ko.File == "" {
				ko.File = lc.Layers[i].Path
				continue
			}
			ko.Hidden = append(ko.Hidden, lc.Layers[i].Path)
		}
		if ko.File == "" {
			continue
		}
		out = append(out, ko)
	}
	slices.SortFunc(out, func(a, b KeyOrigin) int { return strings.Compare(a.Key, b.Key) })
	return out
}

// collectLeaves walks a table to the values that are not tables. An entry of
// an array of tables counts as one value when every entry has a name or a
// key. Otherwise the whole array is one value.
func collectLeaves(m map[string]any, prefix []string, out *[][]string) {
	for k, v := range m {
		p := append(slices.Clone(prefix), k)
		switch v := v.(type) {
		case map[string]any:
			if len(v) == 0 {
				*out = append(*out, p)
				continue
			}
			collectLeaves(v, p, out)
		case []any:
			if segs, ok := entrySegs(v); ok {
				for _, s := range segs {
					*out = append(*out, append(slices.Clone(p), s))
				}
				continue
			}
			*out = append(*out, p)
		default:
			*out = append(*out, p)
		}
	}
}

// displayKey writes a path the way a person would type it.
func displayKey(p []string) string {
	var b strings.Builder
	for i, seg := range p {
		if name, key, ok := elemOf(seg); ok {
			id := name
			if id == "" {
				id = key
			}
			b.WriteString("[" + id + "]")
			continue
		}
		if i > 0 {
			b.WriteByte('.')
		}
		b.WriteString(tomlKey(seg))
	}
	return b.String()
}

// ParseKeyPath splits a dotted key, honouring quotes, so a host name with a
// dot in it can be asked about.
func ParseKeyPath(key string) []string { return splitDotted(key) }

// elemSeg is the path segment for an entry of an array of tables, named by its
// name and its key, either of which may be empty. The NUL bytes cannot appear
// in a TOML key that came from a file a person wrote.
func elemSeg(name, key string) string { return "\x00" + name + "\x00" + key }

// elemOf reads an elemSeg back.
func elemOf(seg string) (name, key string, ok bool) {
	rest, ok := strings.CutPrefix(seg, "\x00")
	if !ok {
		return "", "", false
	}
	name, key, _ = strings.Cut(rest, "\x00")
	return name, key, true
}

// entryID is an entry's name and key, empty when it has none.
func entryID(e map[string]any) (name, key string) {
	name, _ = e["name"].(string)
	key, _ = e["key"].(string)
	return name, key
}

// entryMatches applies the matching rule: by name when both have a name,
// else by key.
func entryMatches(e map[string]any, name, key string) bool {
	en, ek := entryID(e)
	if name != "" && en != "" {
		return en == name
	}
	return key != "" && ek == key
}

// isTableArray reports whether a is a non-empty array whose every element is
// a table.
func isTableArray(a []any) bool {
	if len(a) == 0 {
		return false
	}
	for _, e := range a {
		if _, ok := e.(map[string]any); !ok {
			return false
		}
	}
	return true
}

// entrySegs are the segments of every entry of an array of tables. ok is
// false when a is not one, or when an entry has neither a name nor a key.
func entrySegs(a []any) ([]string, bool) {
	if !isTableArray(a) {
		return nil, false
	}
	out := make([]string, 0, len(a))
	for _, e := range a {
		name, key := entryID(e.(map[string]any))
		if name == "" && key == "" {
			return nil, false
		}
		out = append(out, elemSeg(name, key))
	}
	return out, true
}

// entryIndex finds the entry of arr the segment names, or -1.
func entryIndex(arr []any, seg string) int {
	name, key, ok := elemOf(seg)
	if !ok {
		return -1
	}
	for i, e := range arr {
		if m, ok := e.(map[string]any); ok && entryMatches(m, name, key) {
			return i
		}
	}
	return -1
}

// isTombstone reports whether e is an entry that removes the one it matches.
func isTombstone(e map[string]any) bool {
	v, _ := e[tombstoneKey].(bool)
	return v
}

// mergeTables merges src into dst by the rules at the top of this file. dst is
// changed. Nothing in src is shared with dst afterwards.
func mergeTables(dst, src map[string]any) {
	for k, sv := range src {
		dv, ok := dst[k]
		if !ok {
			dst[k] = deepCopy(sv)
			continue
		}
		switch s := sv.(type) {
		case map[string]any:
			if d, ok := dv.(map[string]any); ok {
				mergeTables(d, s)
				continue
			}
		case []any:
			if d, ok := dv.([]any); ok && isTableArray(s) && (isTableArray(d) || len(d) == 0) {
				dst[k] = mergeEntries(d, s)
				continue
			}
		}
		dst[k] = deepCopy(sv)
	}
}

// mergeEntries merges the entries of src into dst by the matching rule.
func mergeEntries(dst, src []any) []any {
	out := deepCopy(dst).([]any)
	for _, e := range src {
		em := e.(map[string]any)
		name, key := entryID(em)
		i := -1
		if name != "" || key != "" {
			i = slices.IndexFunc(out, func(x any) bool {
				xm, ok := x.(map[string]any)
				return ok && entryMatches(xm, name, key)
			})
		}
		switch {
		case isTombstone(em):
			if i >= 0 {
				out = slices.Delete(out, i, i+1)
			}
		case i >= 0:
			mergeTables(out[i].(map[string]any), em)
		default:
			out = append(out, deepCopy(em))
		}
	}
	return out
}

// stripTombstones drops every entry left with disabled = true, at every
// depth: a tombstone that matched nothing removes nothing and is not a
// setting.
func stripTombstones(m map[string]any) {
	for k, v := range m {
		switch v := v.(type) {
		case map[string]any:
			stripTombstones(v)
		case []any:
			if !isTableArray(v) {
				continue
			}
			m[k] = slices.DeleteFunc(slices.Clone(v), func(e any) bool { return isTombstone(e.(map[string]any)) })
		}
	}
}

// hasTombstone reports whether any array of tables in m has an entry with
// disabled = true.
func hasTombstone(m map[string]any) bool {
	for _, v := range m {
		switch v := v.(type) {
		case map[string]any:
			if hasTombstone(v) {
				return true
			}
		case []any:
			for _, e := range v {
				if em, ok := e.(map[string]any); ok && isTombstone(em) {
					return true
				}
			}
		}
	}
	return false
}

// deepCopy copies the tables and arrays of a parsed TOML value.
func deepCopy(v any) any {
	switch v := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(v))
		for k, e := range v {
			out[k] = deepCopy(e)
		}
		return out
	case []any:
		out := make([]any, len(v))
		for i, e := range v {
			out[i] = deepCopy(e)
		}
		return out
	default:
		return v
	}
}

// lookupPath finds the value at path in a parsed table.
func lookupPath(m map[string]any, path []string) (any, bool) {
	var cur any = m
	for _, seg := range path {
		if _, _, ok := elemOf(seg); ok {
			arr, ok := cur.([]any)
			if !ok {
				return nil, false
			}
			i := entryIndex(arr, seg)
			if i < 0 {
				return nil, false
			}
			cur = arr[i]
			continue
		}
		t, ok := cur.(map[string]any)
		if !ok {
			return nil, false
		}
		if cur, ok = t[seg]; !ok {
			return nil, false
		}
	}
	return cur, true
}

// setPath sets the value at path, making the tables on the way. An entry
// segment is always the last one: it replaces the matching entry of the
// array, or adds one at the end.
func setPath(m map[string]any, path []string, v any) {
	if len(path) == 0 {
		return
	}
	seg := path[0]
	if len(path) == 1 {
		m[seg] = deepCopy(v)
		return
	}
	if _, _, ok := elemOf(path[1]); ok {
		arr, _ := m[seg].([]any)
		if j := entryIndex(arr, path[1]); j >= 0 {
			arr[j] = deepCopy(v)
			return
		}
		m[seg] = append(arr, deepCopy(v))
		return
	}
	t, ok := m[seg].(map[string]any)
	if !ok {
		t = map[string]any{}
		m[seg] = t
	}
	setPath(t, path[1:], v)
}

// deletePath removes the value at path. It reports whether it was there. A
// table left empty by the delete is kept: an empty table is still a table the
// file sets.
func deletePath(m map[string]any, path []string) bool {
	if len(path) == 0 {
		return false
	}
	parentPath, last := path[:len(path)-1], path[len(path)-1]
	if _, _, ok := elemOf(last); ok {
		v, ok := lookupPath(m, parentPath)
		if !ok {
			return false
		}
		arr, ok := v.([]any)
		if !ok {
			return false
		}
		j := entryIndex(arr, last)
		if j < 0 {
			return false
		}
		setPath(m, parentPath, slices.Delete(slices.Clone(arr), j, j+1))
		return true
	}
	parent := any(m)
	if len(parentPath) > 0 {
		var ok bool
		if parent, ok = lookupPath(m, parentPath); !ok {
			return false
		}
	}
	t, ok := parent.(map[string]any)
	if !ok {
		return false
	}
	if _, ok := t[last]; !ok {
		return false
	}
	delete(t, last)
	return true
}

// configChange is one difference between two configs.
type configChange struct {
	path    []string
	value   any
	deleted bool
}

// diffTables lists what changed from cur to next, at the deepest level a
// change can be named: a key of a table, or an entry of an array of tables.
func diffTables(cur, next map[string]any, prefix []string, out *[]configChange) {
	keys := slices.Sorted(maps.Keys(cur))
	for _, k := range slices.Sorted(maps.Keys(next)) {
		if _, ok := cur[k]; !ok {
			keys = append(keys, k)
		}
	}
	for _, k := range keys {
		p := append(slices.Clone(prefix), k)
		c, cok := cur[k]
		n, nok := next[k]
		switch {
		case !nok:
			*out = append(*out, configChange{path: p, deleted: true})
			continue
		case !cok:
			*out = append(*out, configChange{path: p, value: n})
			continue
		}
		if cm, ok := c.(map[string]any); ok {
			if nm, ok := n.(map[string]any); ok {
				diffTables(cm, nm, p, out)
				continue
			}
		}
		if ca, ok := c.([]any); ok {
			if na, ok := n.([]any); ok && diffEntries(ca, na, p, out) {
				continue
			}
		}
		if !reflect.DeepEqual(c, n) {
			*out = append(*out, configChange{path: p, value: n})
		}
	}
}

// diffEntries diffs two arrays of tables entry by entry. It reports false
// when either is not one whose every entry has a name or a key, and the
// caller compares them whole.
func diffEntries(cur, next []any, prefix []string, out *[]configChange) bool {
	cs, cok := entrySegs(cur)
	ns, nok := entrySegs(next)
	switch {
	case cok && nok:
	case cok && len(next) == 0:
	case nok && len(cur) == 0:
	default:
		return false
	}
	cm := make(map[string]any, len(cs))
	for i, s := range cs {
		cm[s] = cur[i]
	}
	nm := make(map[string]any, len(ns))
	for i, s := range ns {
		nm[s] = next[i]
	}
	for _, s := range cs {
		p := append(slices.Clone(prefix), s)
		n, ok := nm[s]
		switch {
		case !ok:
			*out = append(*out, configChange{path: p, deleted: true})
		case !reflect.DeepEqual(cm[s], n):
			*out = append(*out, configChange{path: p, value: n})
		}
	}
	for _, s := range ns {
		if _, ok := cm[s]; !ok {
			*out = append(*out, configChange{path: append(slices.Clone(prefix), s), value: nm[s]})
		}
	}
	return true
}
