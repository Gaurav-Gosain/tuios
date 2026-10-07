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
// config.toml is last because it is the file tuios writes. A setting changed
// from inside tuios has to take effect, and it can only do that from the file
// that wins. A key a read-only file (a Nix store link, say) holds is written to
// config.toml for that reason, and config.toml then wins over it.
//
// # Merge rules
//
//   - Tables merge key by key, at every depth. [hosts.NAME] tables are tables,
//     so hosts merge by name.
//   - A scalar from a later file replaces the earlier one.
//   - An array replaces the earlier array whole.
//   - An array of tables whose entries all carry a name (or, failing that, a
//     key) merges by that field: an entry with a name already present merges
//     into it key by key, and a new name is added at the end. This is how
//     [[keybindings.command]] and [[agents.risk.rule]] entries from two files
//     combine. Any other array of tables replaces, as an array does.
//
// # What is not an error
//
// An include that names a file that is not there is a warning, so one machine
// can include a file only it has. A cycle is a warning, and the file that
// closes it is skipped. A file reached twice is merged once. A file that is
// there and does not parse is an error, as config.toml is.

// IncludeKey is the top-level key that lists the files a config file includes.
const IncludeKey = "include"

// DropInDirName is the directory beside config.toml whose *.toml files are
// merged in.
const DropInDirName = "config.d"

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
	Kind LayerKind
	// From is the file whose include list named this one, empty for the main
	// file and the config.d files.
	From string
	// Data is the file as read.
	Data []byte
	// Values is the file parsed, without its include key.
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
	// Missing are the included files that were not there, as resolved paths.
	Missing []string
	// Warnings say what was skipped and why.
	Warnings []string
	// Layered is true when config.toml has an include key or a config.d
	// directory exists. When it is false the config is config.toml alone and
	// every write works the way it did before includes existed.
	Layered bool
	// DropInDir is the config.d directory, whether or not it exists.
	DropInDir string

	merged map[string]any
}

// LoadLayered reads config.toml at mainPath and every file it brings in. A
// main file that cannot be read is returned as the error os.ReadFile gave, so
// a caller can test it with errors.Is(err, fs.ErrNotExist).
func LoadLayered(mainPath string) (*LayeredConfig, error) {
	mainPath = absPath(mainPath)
	lc := &LayeredConfig{Main: mainPath, DropInDir: filepath.Join(filepath.Dir(mainPath), DropInDirName)}
	data, err := os.ReadFile(mainPath) //nolint:gosec // the user's own config file
	if err != nil {
		return nil, err
	}
	values, err := parseLayer(data)
	if err != nil {
		return nil, fmt.Errorf("failed to parse config file: %w", err)
	}
	_, hasInclude := values[IncludeKey]

	l := &layerLoader{lc: lc, seen: map[string]bool{}}
	l.seen[realPath(mainPath)] = true
	stack := []string{realPath(mainPath)}
	if err := l.includes(mainPath, values, stack, 0); err != nil {
		return nil, err
	}

	dropIns, dirExists := dropInFiles(lc.DropInDir)
	for _, p := range dropIns {
		if err := l.visit(p, LayerDropIn, "", stack, 0); err != nil {
			return nil, err
		}
	}

	delete(values, IncludeKey)
	lc.Layers = append(lc.Layers, ConfigLayer{Path: mainPath, Kind: LayerMain, Data: data, Values: values})
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
		if err := l.visit(resolveInclude(from, name), LayerInclude, from, stack, depth+1); err != nil {
			return err
		}
	}
	return nil
}

// visit reads one file, merges its own includes first, and then adds it.
func (l *layerLoader) visit(path string, kind LayerKind, from string, stack []string, depth int) error {
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
	data, err := os.ReadFile(path) //nolint:gosec // a file the user's own config names
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			l.lc.Missing = append(l.lc.Missing, path)
			l.lc.Warnings = append(l.lc.Warnings, fmt.Sprintf("%s includes %s, which does not exist. tuios skips it.", l.show(from), l.show(path)))
			return nil
		}
		return fmt.Errorf("failed to read %s: %w", path, err)
	}
	values, err := parseLayer(data)
	if err != nil {
		return fmt.Errorf("failed to parse %s: %w", path, err)
	}
	l.seen[real] = true
	if err := l.includes(path, values, append(slices.Clone(stack), real), depth); err != nil {
		return err
	}
	delete(values, IncludeKey)
	l.lc.Layers = append(l.lc.Layers, ConfigLayer{Path: path, Kind: kind, From: from, Data: data, Values: values})
	return nil
}

// show names a file in a warning, the way DisplayPath does.
func (l *layerLoader) show(p string) string {
	if p == "" {
		return DropInDirName
	}
	return displayPath(l.lc.Main, p)
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
		if e.IsDir() {
			continue
		}
		out = append(out, filepath.Join(dir, name))
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
	probe, err := os.CreateTemp(filepath.Dir(target), ".tuios-write-check-*")
	if err != nil {
		return false
	}
	_ = probe.Close()
	_ = os.Remove(probe.Name())
	return true
}

// Merged is every layer merged into one table, in merge order.
func (lc *LayeredConfig) Merged() map[string]any {
	if lc.merged == nil {
		out := map[string]any{}
		for _, layer := range lc.Layers {
			mergeTables(out, layer.Values)
		}
		lc.merged = out
	}
	return lc.merged
}

// Bytes is the merged config as one TOML document. When the config is not
// layered it is config.toml exactly as read, so a single-file config takes the
// same path it always did.
func (lc *LayeredConfig) Bytes() ([]byte, error) {
	if !lc.Layered {
		return lc.mainLayer().Data, nil
	}
	data, err := toml.Marshal(lc.Merged())
	if err != nil {
		return nil, fmt.Errorf("failed to merge config files: %w", err)
	}
	return data, nil
}

// Files are the paths of every file read, in merge order.
func (lc *LayeredConfig) Files() []string {
	out := make([]string, 0, len(lc.Layers))
	for _, l := range lc.Layers {
		out = append(out, l.Path)
	}
	return out
}

func (lc *LayeredConfig) mainLayer() *ConfigLayer {
	return &lc.Layers[len(lc.Layers)-1]
}

// layerIndex finds the layer for path, or -1.
func (lc *LayeredConfig) layerIndex(path string) int {
	for i := range lc.Layers {
		if lc.Layers[i].Path == path {
			return i
		}
	}
	return -1
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
	// with its name in brackets, such as keybindings.command[ctrl+g].
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
		out = append(out, ko)
	}
	slices.SortFunc(out, func(a, b KeyOrigin) int { return strings.Compare(a.Key, b.Key) })
	return out
}

// collectLeaves walks a table to the values that are not tables. An entry of
// a mergeable array of tables counts as one value.
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
			if field, ok := tableArrayIdentity(v); ok {
				for _, e := range v {
					id, _ := e.(map[string]any)[field].(string)
					*out = append(*out, append(slices.Clone(p), elemSeg(id)))
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
		if id, ok := elemID(seg); ok {
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

// elemSeg is the path segment for the array entry with the given name. The NUL
// prefix cannot appear in a TOML key that came from a file a person wrote.
func elemSeg(id string) string { return "\x00" + id }

// elemID reads an elemSeg back.
func elemID(seg string) (string, bool) {
	if id, ok := strings.CutPrefix(seg, "\x00"); ok {
		return id, true
	}
	return "", false
}

// tableArrayIdentity says which field names the entries of an array of
// tables: name when every entry has a string name, key when every entry has a
// string key. ok is false for any other array, which then merges as a value.
func tableArrayIdentity(a []any) (string, bool) {
	if len(a) == 0 {
		return "", false
	}
	for _, field := range []string{"name", "key"} {
		all := true
		for _, e := range a {
			m, ok := e.(map[string]any)
			if !ok {
				return "", false
			}
			if _, ok := m[field].(string); !ok {
				all = false
				break
			}
		}
		if all {
			return field, true
		}
	}
	return "", false
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
			if d, ok := dv.([]any); ok {
				if merged, ok := mergeTableArrays(d, s); ok {
					dst[k] = merged
					continue
				}
			}
		}
		dst[k] = deepCopy(sv)
	}
}

// mergeTableArrays merges two arrays of tables that share an identity field.
// ok is false when they do not, and the caller replaces.
func mergeTableArrays(dst, src []any) ([]any, bool) {
	df, dok := tableArrayIdentity(dst)
	sf, sok := tableArrayIdentity(src)
	if !dok || !sok || df != sf {
		return nil, false
	}
	out := deepCopy(dst).([]any)
	for _, e := range src {
		em := e.(map[string]any)
		id := em[sf].(string)
		i := slices.IndexFunc(out, func(x any) bool { return x.(map[string]any)[sf] == id })
		if i < 0 {
			out = append(out, deepCopy(em))
			continue
		}
		mergeTables(out[i].(map[string]any), em)
	}
	return out, true
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
		if id, ok := elemID(seg); ok {
			arr, ok := cur.([]any)
			if !ok {
				return nil, false
			}
			i := elemIndex(arr, id)
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

// elemIndex finds the array entry named id, by its name or its key field.
func elemIndex(arr []any, id string) int {
	field, ok := tableArrayIdentity(arr)
	if !ok {
		return -1
	}
	return slices.IndexFunc(arr, func(x any) bool { return x.(map[string]any)[field] == id })
}

// setPath sets the value at path, making the tables on the way. An entry
// segment is always the last one: it replaces the named entry of the array, or
// adds one at the end.
func setPath(m map[string]any, path []string, v any) {
	if len(path) == 0 {
		return
	}
	seg := path[0]
	if len(path) == 1 {
		m[seg] = deepCopy(v)
		return
	}
	if id, ok := elemID(path[1]); ok {
		arr, _ := m[seg].([]any)
		if j := elemIndex(arr, id); j >= 0 {
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
	if id, ok := elemID(last); ok {
		arrPath := parentPath
		v, ok := lookupPath(m, arrPath)
		if !ok {
			return false
		}
		arr, ok := v.([]any)
		if !ok {
			return false
		}
		j := elemIndex(arr, id)
		if j < 0 {
			return false
		}
		setPath(m, arrPath, slices.Delete(slices.Clone(arr), j, j+1))
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
// change can be named: a key of a table, or an entry of a mergeable array.
func diffTables(cur, next map[string]any, prefix []string, out *[]configChange) {
	keys := slices.Sorted(maps.Keys(cur))
	for k := range next {
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
			if na, ok := n.([]any); ok && diffTableArrays(ca, na, p, out) {
				continue
			}
		}
		if !reflect.DeepEqual(c, n) {
			*out = append(*out, configChange{path: p, value: n})
		}
	}
}

// diffTableArrays diffs two arrays of tables entry by entry. It reports false
// when they are not both mergeable by the same field, and the caller compares
// them whole.
func diffTableArrays(cur, next []any, prefix []string, out *[]configChange) bool {
	cf, cok := tableArrayIdentity(cur)
	nf, nok := tableArrayIdentity(next)
	switch {
	case cok && nok && cf == nf:
	case cok && len(next) == 0:
		nf = cf
	case nok && len(cur) == 0:
		cf = nf
	default:
		return false
	}
	byID := func(a []any, field string) map[string]any {
		m := map[string]any{}
		for _, e := range a {
			em := e.(map[string]any)
			m[em[field].(string)] = em
		}
		return m
	}
	cm, nm := byID(cur, cf), byID(next, nf)
	for _, id := range slices.Sorted(maps.Keys(cm)) {
		p := append(slices.Clone(prefix), elemSeg(id))
		n, ok := nm[id]
		switch {
		case !ok:
			*out = append(*out, configChange{path: p, deleted: true})
		case !reflect.DeepEqual(cm[id], n):
			*out = append(*out, configChange{path: p, value: n})
		}
	}
	for _, e := range next {
		id := e.(map[string]any)[nf].(string)
		if _, ok := cm[id]; !ok {
			*out = append(*out, configChange{path: append(slices.Clone(prefix), elemSeg(id)), value: e})
		}
	}
	return true
}
