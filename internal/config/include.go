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

	"github.com/Gaurav-Gosain/tuios/internal/pathroot"

	"github.com/pelletier/go-toml/v2"

	"github.com/Gaurav-Gosain/tuios/internal/errfmt"
	"github.com/Gaurav-Gosain/tuios/internal/sortx"
)

// IncludeKey is the top-level key that lists the files a config file includes.
const IncludeKey = "include"

// DropInDirName is the directory beside config.toml whose *.toml files are
// merged in.
const DropInDirName = "config.d"

const tombstoneKey = "disabled"

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
			return nil, errfmt.Errorf("%s is a directory. The config must be a file", mainPath)
		}
	}
	values, err := parseLayer(data)
	if err != nil {
		return nil, errfmt.Errorf("failed to parse config file: %w", err)
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

type layerLoader struct {
	lc   *LayeredConfig
	seen map[string]bool
}

func (l *layerLoader) includes(from string, values map[string]any, stack []string, depth int) error {
	list, err := includeList(values[IncludeKey])
	if err != nil {
		return errfmt.Errorf("%s: %w", from, err)
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

func (l *layerLoader) visit(path string, optional bool, kind LayerKind, from string, stack []string, depth int) error {
	real := realPath(path)
	if from != "" && real == realPath(from) {
		l.lc.Warnings = append(l.lc.Warnings, fmt.Sprintf("%s includes itself. tuios skips the include.", l.show(from)))
		return nil
	}
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
		return errfmt.Errorf("%s includes %s, which is a directory. An include names one file. Put a directory of files in %s", l.show(from), l.show(path), DropInDirName)
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
		if optional || kind == LayerDropIn {
			l.lc.Warnings = append(l.lc.Warnings, fmt.Sprintf("tuios cannot read %s, so it skips it: %v", l.show(path), err))
			return nil
		}
		return errfmt.Errorf("failed to read %s: %w", path, err)
	}
	values, err := parseLayer(data)
	if err != nil {
		return errfmt.Errorf("failed to parse %s: %w", path, err)
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

var nestedIncludeAllowed = map[string]bool{"tailscale": true}

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

func (l *layerLoader) show(p string) string {
	if p == "" {
		return DropInDirName
	}
	return displayPath(l.lc.Main, p)
}

var pathKeys = [][]string{
	{"plugins", "dirs"},
	{"screenshot", "font_file"},
	{"tailscale", "socket"},
	{"notify", "*", "token_file"},
	{"notify", "*", "user_file"},
	{"notifications", "*", "sounds", "*"},
}

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

func parseLayer(data []byte) (map[string]any, error) {
	values := map[string]any{}
	if err := toml.Unmarshal(data, &values); err != nil {
		return nil, err
	}
	return values, nil
}

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
				return nil, errfmt.Errorf("include must be a list of file paths")
			}
			out = append(out, s)
		}
		return out, nil
	default:
		return nil, errfmt.Errorf("include must be a list of file paths")
	}
}

func resolveInclude(from, name string) string {
	name = pathroot.ExpandHome(name)
	if !filepath.IsAbs(name) {
		name = filepath.Join(filepath.Dir(from), name)
	}
	return filepath.Clean(name)
}

func absPath(p string) string {
	if a, err := filepath.Abs(p); err == nil {
		return a
	}
	return filepath.Clean(p)
}

func realPath(p string) string {
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r
	}
	return p
}

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
		if info, err := os.Stat(p); err == nil && info.IsDir() {
			continue
		}
		out = append(out, p)
	}
	slices.Sort(out)
	return out, true
}

func fileWritable(path string) bool {
	target := realPath(path)
	info, err := os.Stat(target)
	if err == nil && info.Mode().Perm()&0o200 == 0 {
		return false
	}
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
		return nil, errfmt.Errorf("failed to merge config files: %w", err)
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
	for _, v := range slices.Backward(lc.Layers) {
		if _, ok := lookupPath(v.Values, key); ok {
			return v.Path, true
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
		for _, v := range slices.Backward(lc.Layers) {
			if _, ok := lookupPath(v.Values, p); !ok {
				continue
			}
			if ko.File == "" {
				ko.File = v.Path
				continue
			}
			ko.Hidden = append(ko.Hidden, v.Path)
		}
		if ko.File == "" {
			continue
		}
		out = append(out, ko)
	}
	sortx.SortFunc(out, func(a, b KeyOrigin) int { return strings.Compare(a.Key, b.Key) })
	return out
}

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

func displayKey(p []string) string {
	var b strings.Builder
	for i, seg := range p {
		if name, key, ok := elemOf(seg); ok {
			id := name
			if id == "" {
				id = key
			}
			b.WriteString("[")
			b.WriteString(id)
			b.WriteString("]")
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

func elemSeg(name, key string) string { return "\x00" + name + "\x00" + key }

func elemOf(seg string) (name, key string, ok bool) {
	rest, ok := strings.CutPrefix(seg, "\x00")
	if !ok {
		return "", "", false
	}
	name, key, _ = strings.Cut(rest, "\x00")
	return name, key, true
}

func entryID(e map[string]any) (name, key string) {
	name, _ = e["name"].(string)
	key, _ = e["key"].(string)
	return name, key
}

func entryMatches(e map[string]any, name, key string) bool {
	en, ek := entryID(e)
	if name != "" && en != "" {
		return en == name
	}
	return key != "" && ek == key
}

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

func entryIndex(arr []any, seg string) int { return entryIndexOf(arr, seg, false) }

func entryIndexOf(arr []any, seg string, tombs bool) int {
	name, key, ok := elemOf(seg)
	if !ok {
		return -1
	}
	for i, e := range arr {
		m, ok := e.(map[string]any)
		if !ok || !entryMatches(m, name, key) {
			continue
		}
		if isTombstone(m) && !tombs {
			continue
		}
		return i
	}
	return -1
}

func isTombstone(e map[string]any) bool {
	v, _ := e[tombstoneKey].(bool)
	return v
}

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

func lookupPath(m map[string]any, path []string) (any, bool) {
	return lookupPathOf(m, path, false)
}

func lookupPathOf(m map[string]any, path []string, tombs bool) (any, bool) {
	var cur any = m
	for _, seg := range path {
		if _, _, ok := elemOf(seg); ok {
			arr, ok := cur.([]any)
			if !ok {
				return nil, false
			}
			i := entryIndexOf(arr, seg, tombs)
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
		rest := slices.Delete(slices.Clone(arr), j, j+1)
		if len(rest) == 0 {
			deletePath(m, parentPath)
			return true
		}
		setPath(m, parentPath, rest)
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

type configChange struct {
	path    []string
	value   any
	deleted bool
	shadow  bool
}

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
			if ca, ok := c.([]any); ok && diffEntries(ca, nil, p, out) {
				continue
			}
			*out = append(*out, configChange{path: p, deleted: true})
			continue
		case !cok:
			if na, ok := n.([]any); ok && diffEntries(nil, na, p, out) {
				continue
			}
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
