package config

import (
	"errors"
	"fmt"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"

	"github.com/Gaurav-Gosain/tuios/internal/errfmt"
	"github.com/Gaurav-Gosain/tuios/internal/pathroot"
	"github.com/pelletier/go-toml/v2"
)

// WriteNote says where a save put a change when that is not the file that
// holds the key. The zero value has nothing to say.
type WriteNote struct {
	// Main is config.toml.
	Main string
	// Redirected are the changes that went to another file because the file
	// that holds the key is read-only.
	Redirected []Redirect
	// Kept are read-only files that hold a key the save removed. The key is
	// still there.
	Kept []string
	// Moved are the changes that went to config.toml because tuios could not
	// change the lines of the file that holds the key without rewriting it.
	Moved []Redirect
}

// Redirect is one read-only file and the file its change went to.
type Redirect struct {
	From, To string
}

// Empty reports whether the note has nothing to say.
func (n WriteNote) Empty() bool {
	return len(n.Redirected) == 0 && len(n.Kept) == 0 && len(n.Moved) == 0
}

// Message is the note as text for a person, empty when there is nothing to
// say.
func (n WriteNote) Message() string {
	var parts []string
	for _, r := range n.Redirected {
		parts = append(parts, fmt.Sprintf("tuios cannot write %s. It wrote the change to %s.", displayPath(n.Main, r.From), displayPath(n.Main, r.To)))
	}
	for _, f := range n.Kept {
		parts = append(parts, fmt.Sprintf("tuios cannot write %s. Remove the setting there by hand.", displayPath(n.Main, f)))
	}
	for _, r := range n.Moved {
		parts = append(parts, fmt.Sprintf("tuios cannot change the lines of %s. It wrote the change to %s.", displayPath(n.Main, r.From), displayPath(n.Main, r.To)))
	}
	return strings.Join(parts, " ")
}

func (n *WriteNote) addRedirected(from, to string) {
	r := Redirect{From: from, To: to}
	if slices.Contains(n.Redirected, r) {
		return
	}
	n.Redirected = append(n.Redirected, r)
}

func (n *WriteNote) addMoved(from, to string) {
	r := Redirect{From: from, To: to}
	if slices.Contains(n.Moved, r) {
		return
	}
	n.Moved = append(n.Moved, r)
}

func (n *WriteNote) addKept(p string) {
	if slices.Contains(n.Kept, p) {
		return
	}
	n.Kept = append(n.Kept, p)
}

// ReadConfigFile is the config at path as one TOML document, with every
// include and config.d file merged in. A main file that is not there is
// returned as the error os.ReadFile gave.
func ReadConfigFile(path string) ([]byte, error) {
	lc, err := LoadLayered(path)
	if err != nil {
		return nil, err
	}
	return lc.Bytes()
}

func loadConfigFile(path string) (*UserConfig, *LayeredConfig, error) {
	lc, err := LoadLayered(path)
	if err != nil {
		return nil, nil, err
	}
	cfg, err := parseLayered(lc)
	return cfg, lc, err
}

func parseLayered(lc *LayeredConfig) (*UserConfig, error) {
	data, err := lc.Bytes()
	if err != nil {
		return nil, err
	}
	cfg, err := ParseUserConfig(data)
	if err != nil {
		return nil, err
	}
	warnings := append([]string(nil), lc.Warnings...)
	warnings = append(warnings, UnknownKeybindTables(lc)...)
	warnings = append(warnings, UnknownKeybindActions(lc)...)
	cfg.LoadWarnings = append(warnings, StartupLayoutWarnings(lc, cfg)...)
	return cfg, nil
}

func readConfigMerged(path string) ([]byte, error) {
	if path == "" {
		return nil, errfmt.Errorf("no config file path is set")
	}
	data, err := ReadConfigFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, errfmt.Errorf("failed to read config file: %w", err)
	}
	return data, nil
}

func effectiveTable(lc *LayeredConfig) (map[string]any, error) {
	cfg, err := parseLayered(lc)
	if err != nil {
		return nil, err
	}
	data, err := MarshalUserConfig(cfg)
	if err != nil {
		return nil, err
	}
	return parseLayer(data)
}

const errSaveFailed = "the config files have an error, so tuios did not save"

type saveKeys struct {
	held, forced [][]string
}

func saveConfigData(path string, base, next []byte, gen uint64, keys saveKeys) (WriteNote, error) {
	lc, err := loadLayered(path, true)
	if err != nil {
		return WriteNote{}, errfmt.Errorf("%s: %w", errSaveFailed, err)
	}
	cur, err := effectiveTable(lc)
	if err != nil {
		return WriteNote{}, errfmt.Errorf("%s: %w", errSaveFailed, err)
	}
	from := cur
	if base != nil {
		if from, err = parseLayer(base); err != nil {
			return WriteNote{}, err
		}
	}
	want, err := parseLayer(next)
	if err != nil {
		return WriteNote{}, err
	}
	var changes []configChange
	diffTables(from, want, nil, &changes)
	changes = skipHeld(changes, keys.held)
	changes = addForced(changes, want, keys.forced)
	kept := changes[:0]
	for _, ch := range changes {
		if keyGen[path+"\x01"+displayKey(ch.path)] > gen {
			continue
		}
		v, ok := lookupPath(cur, ch.path)
		if ch.deleted && !ok || !ch.deleted && ok && reflect.DeepEqual(v, ch.value) {
			continue
		}
		kept = append(kept, ch)
	}
	note, err := applyChanges(lc, kept)
	if err == nil {
		for _, ch := range kept {
			keyGen[path+"\x01"+displayKey(ch.path)] = gen
		}
	}
	return note, err
}

func applyChanges(lc *LayeredConfig, changes []configChange) (WriteNote, error) {
	note := WriteNote{Main: lc.Main}
	if len(changes) == 0 {
		return note, nil
	}
	w := newLayerWriter(lc)
	for _, ch := range changes {
		if ch.deleted {
			if err := w.planDelete(ch, &note); err != nil {
				return note, err
			}
			continue
		}
		i, err := w.target(ch.path, &note)
		if err != nil {
			return note, err
		}
		ch.shadow = w.needsShadow(i, ch.path, ch.value)
		w.add(i, ch)
	}
	return note, w.flush(&note)
}

var errNoWritableFile = errors.New("tuios cannot write config.toml or any file it includes. Make config.toml writable, or include a writable file")

type layerWriter struct {
	lc       *LayeredConfig
	canWrite map[int]bool
	changes  map[int][]configChange
	order    []int
}

func newLayerWriter(lc *LayeredConfig) *layerWriter {
	return &layerWriter{lc: lc, canWrite: map[int]bool{}, changes: map[int][]configChange{}}
}

func (w *layerWriter) writable(i int) bool {
	ok, seen := w.canWrite[i]
	if !seen {
		ok = w.lc.Layers[i].Writable()
		w.canWrite[i] = ok
	}
	return ok
}

func (w *layerWriter) lastWritable() int {
	for i := range slices.Backward(w.lc.Layers) {
		if w.writable(i) {
			return i
		}
	}
	return -1
}

func (w *layerWriter) holders(path []string) []int {
	var out []int
	for i := range w.lc.Layers {
		if _, ok := lookupPathOf(w.lc.Layers[i].Values, path, true); ok {
			out = append(out, i)
		}
	}
	return out
}

func (w *layerWriter) needsShadow(t int, path []string, value any) bool {
	if _, _, ok := elemOf(path[len(path)-1]); !ok {
		return false
	}
	entry, ok := value.(map[string]any)
	if !ok || isTombstone(entry) {
		return false
	}
	before := map[string]any{}
	for _, l := range w.lc.Layers[:t] {
		mergeTables(before, l.Values)
	}
	stripTombstones(before)
	v, ok := lookupPath(before, path)
	if !ok {
		return false
	}
	old, ok := v.(map[string]any)
	if !ok {
		return false
	}
	for k := range old {
		if _, has := entry[k]; !has {
			return true
		}
	}
	return false
}

func (w *layerWriter) target(path []string, note *WriteNote) (int, error) {
	holders := w.holders(path)
	owner := -1
	if len(holders) > 0 {
		owner = holders[len(holders)-1]
		if w.lc.Layers[owner].isLook() {
			if main := len(w.lc.Layers) - 1; w.writable(main) {
				return main, nil
			}
		} else if w.writable(owner) {
			return owner, nil
		}
	} else {
		for n := len(path) - 1; n >= 1 && owner < 0; n-- {
			for i, layer := range slices.Backward(w.lc.Layers) {
				v, ok := lookupPath(layer.Values, path[:n])
				if !ok || !isEntryCollection(v) || layer.isLook() {
					continue
				}
				if w.writable(i) {
					return i, nil
				}
			}
		}
	}
	t := w.lastWritable()
	if t < 0 {
		return -1, errNoWritableFile
	}
	if len(holders) > 0 && t < holders[len(holders)-1] {
		return -1, errfmt.Errorf("tuios cannot save %s. %s sets it, and tuios cannot write that file. Change it there", displayKey(path), displayPath(w.lc.Main, w.lc.Layers[holders[len(holders)-1]].Path))
	}
	if owner >= 0 {
		note.addRedirected(w.lc.Layers[owner].Path, w.lc.Layers[t].Path)
	}
	return t, nil
}

func (w *layerWriter) planDelete(ch configChange, note *WriteNote) error {
	holders := w.holders(ch.path)
	tomb := false
	for _, i := range holders {
		if w.writable(i) {
			w.add(i, ch)
			continue
		}
		if _, _, ok := elemOf(ch.path[len(ch.path)-1]); ok {
			tomb = true
			continue
		}
		note.addKept(w.lc.Layers[i].Path)
	}
	if !tomb {
		return nil
	}
	entry := tombstoneFor(ch.path[len(ch.path)-1])
	t := w.lastWritable()
	if t < 0 || t < holders[len(holders)-1] {
		return errfmt.Errorf("tuios cannot remove %s. A file that tuios cannot write sets it. Remove it there", displayKey(ch.path))
	}
	w.add(t, configChange{path: ch.path, value: entry})
	return nil
}

func (w *layerWriter) add(i int, ch configChange) {
	if _, ok := w.changes[i]; !ok {
		w.order = append(w.order, i)
	}
	w.changes[i] = append(w.changes[i], ch)
}

func (w *layerWriter) flush(note *WriteNote) error {
	main := len(w.lc.Layers) - 1
	type planned struct {
		i   int
		out []byte
	}
	var ready []planned
	for _, i := range w.order {
		if i == main {
			continue
		}
		layer := w.lc.Layers[i]
		out, _, ok, err := editFile(layer.Data, w.changes[i])
		if err != nil {
			return errfmt.Errorf("failed to parse %s: %w", layer.Path, err)
		}
		if ok {
			ready = append(ready, planned{i, out})
			continue
		}
		if err := w.moveToMain(i, note); err != nil {
			return err
		}
	}
	if _, ok := w.changes[main]; ok {
		layer := w.lc.Layers[main]
		data := layer.Data
		if w.lc.MainMissing {
			data = []byte(configFileHeader(layer.Path))
		}
		out, err := editMain(data, w.changes[main], layer.Path)
		if err != nil {
			return err
		}
		if string(out) != string(layer.Data) || w.lc.MainMissing {
			ready = append(ready, planned{main, out})
		}
	}
	for _, p := range ready {
		if string(p.out) == string(w.lc.Layers[p.i].Data) && p.i != main {
			continue
		}
		if err := writeConfigBytes(p.out, w.lc.Layers[p.i].Path); err != nil {
			return err
		}
	}
	return nil
}

func (w *layerWriter) moveToMain(i int, note *WriteNote) error {
	main := len(w.lc.Layers) - 1
	layer := w.lc.Layers[i]
	var keys []string
	for _, ch := range w.changes[i] {
		keys = append(keys, displayKey(ch.path))
		if ch.deleted {
			return errfmt.Errorf("tuios cannot remove %s from %s without writing the whole file again, and it does not rewrite a file you wrote. Remove it there by hand", displayKey(ch.path), displayPath(w.lc.Main, layer.Path))
		}
	}
	if !w.writable(main) {
		return errfmt.Errorf("tuios cannot change %s in %s without writing the whole file again, and it cannot write config.toml. Change it there by hand", strings.Join(keys, ", "), displayPath(w.lc.Main, layer.Path))
	}
	for _, ch := range w.changes[i] {
		ch.shadow = w.needsShadow(main, ch.path, ch.value)
		w.add(main, ch)
	}
	note.addMoved(layer.Path, w.lc.Layers[main].Path)
	log.Printf("Config: tuios could not edit the lines of %s, so it wrote the change to %s", layer.Path, w.lc.Main)
	return nil
}

func editMain(data []byte, changes []configChange, path string) ([]byte, error) {
	out, want, ok, err := editFile(data, changes)
	if err != nil {
		return nil, errfmt.Errorf("failed to parse %s: %w", path, err)
	}
	if ok {
		return out, nil
	}
	log.Printf("Config: tuios could not edit the lines of %s, so it wrote the whole file again", path)
	full, err := marshalTable(want)
	if err != nil {
		return nil, errfmt.Errorf("failed to write %s: %w", path, err)
	}
	return append([]byte(configFileHeader(path)), full...), nil
}

func isEntryCollection(v any) bool {
	switch v := v.(type) {
	case map[string]any:
		if len(v) == 0 {
			return false
		}
		for _, e := range v {
			if _, ok := e.(map[string]any); !ok {
				return false
			}
		}
		return true
	case []any:
		return isTableArray(v)
	}
	return false
}

// WriteTarget is the file a change to key is written to, and the note that
// says so when the file that holds it is read-only.
func (lc *LayeredConfig) WriteTarget(key []string) (string, WriteNote, error) {
	note := WriteNote{Main: lc.Main}
	w := newLayerWriter(lc)
	i, err := w.target(key, &note)
	if err != nil {
		return "", note, err
	}
	return lc.Layers[i].Path, note, nil
}

// Holders are the files that set key, in merge order.
func (lc *LayeredConfig) Holders(key []string) []ConfigLayer {
	var out []ConfigLayer
	for _, l := range lc.Layers {
		if _, ok := lookupPath(l.Values, key); ok {
			out = append(out, l)
		}
	}
	return out
}

func marshalTable(values map[string]any) ([]byte, error) {
	var buf strings.Builder
	if inc, ok := values[IncludeKey]; ok {
		buf.WriteString(IncludeKey)
		buf.WriteString(" = ")
		buf.WriteString(encodeValue(inc))
		buf.WriteString("\n\n")
		rest := make(map[string]any, len(values))
		for k, v := range values {
			if k != IncludeKey {
				rest[k] = v
			}
		}
		values = rest
	}
	enc := toml.NewEncoder(&buf).SetIndentSymbol("  ")
	if err := enc.Encode(values); err != nil {
		return nil, err
	}
	return []byte(buf.String()), nil
}

// IncludeLine is the include key of the config file at path as one TOML line,
// or "" when the file has none or cannot be read.
func IncludeLine(path string) string {
	data, err := os.ReadFile(path) //nolint:gosec // the user's own config file
	if err != nil {
		return ""
	}
	values, err := parseLayer(data)
	if err != nil {
		return scanIncludeLine(data)
	}
	inc, ok := values[IncludeKey]
	if !ok {
		return ""
	}
	return IncludeKey + " = " + encodeValue(inc)
}

func scanIncludeLine(data []byte) string {
	e := newTOMLEdit(data)
	for i := 0; i < len(e.lines); i++ {
		s := strings.TrimSpace(e.lines[i])
		if strings.HasPrefix(s, "[") {
			return ""
		}
		k, rest, ok := cutOutsideQuotes(e.lines[i], '=')
		if !ok || strings.TrimSpace(k) != IncludeKey {
			continue
		}
		var st valueScan
		st.feed(rest)
		end := i + 1
		for !st.done() && end < len(e.lines) {
			st.feed(e.lines[end])
			end++
		}
		text := strings.Join(e.lines[i:end], "\n")
		values, err := parseLayer([]byte(text))
		if err != nil {
			return ""
		}
		inc, ok := values[IncludeKey]
		if !ok {
			return ""
		}
		if _, err := includeList(inc); err != nil {
			return ""
		}
		return IncludeKey + " = " + encodeValue(inc)
	}
	return ""
}

// FirstRunConfig is the config.toml tuios writes when there is none: the
// comment header and nothing else, so every other file of the config applies.
// The one exception is [startup]. A config without it means the floating,
// standalone session tuios had before, so a first start writes tiled and
// daemon on, unless another file of the config already sets them. include is
// a line to keep, such as an include list, or "".
func FirstRunConfig(path, include string) []byte {
	var sb strings.Builder
	sb.WriteString(configFileHeader(path))
	if include != "" {
		sb.WriteString(include)
		sb.WriteString("\n\n")
	}
	var startup []string
	lc, err := loadLayered(path, true)
	for _, key := range []string{"tiled", "daemon"} {
		set := false
		if err == nil {
			for _, l := range lc.Layers {
				if l.Kind == LayerMain {
					continue
				}
				if _, ok := lookupPath(l.Values, []string{"startup", key}); ok {
					set = true
				}
			}
		}
		if !set {
			startup = append(startup, key+" = true")
		}
	}
	if len(startup) > 0 {
		sb.WriteString("[startup]\n")
		sb.WriteString(strings.Join(startup, "\n"))
		sb.WriteString("\n")
	}
	return []byte(sb.String())
}

// ResetConfig writes config.toml at path as a first start writes it, with
// its include list kept.
func ResetConfig(path string) error {
	return writeConfigBytes(FirstRunConfig(path, IncludeLine(path)), path)
}

// PruneResult is what PruneConfig removed, or would remove.
type PruneResult struct {
	// Keys are the dotted keys removed.
	Keys []string
	// Uncovered are the removed keys another file sets, with that file. Its
	// value applies once the key is gone from config.toml.
	Uncovered []KeyOrigin
}

// PruneConfig removes from config.toml every key whose value is the default,
// unless its absence means something else, as it does for [startup]. A config.toml
// written by an older tuios sets every key, which hides every included file;
// this is the way back to a config.toml that holds only what the person
// chose. With dryRun it changes nothing and reports what it would remove.
func PruneConfig(path string, dryRun bool) (PruneResult, error) {
	var res PruneResult
	lc, err := LoadLayered(path)
	if err != nil {
		return res, err
	}
	main := lc.mainLayer()
	with := func(values map[string]any) (map[string]any, error) {
		probe := LayeredConfig{Main: lc.Main, Layered: true}
		probe.Layers = []ConfigLayer{{Path: main.Path, Kind: LayerMain, Values: values}}
		return effectiveTable(&probe)
	}
	before, err := with(main.Values)
	if err != nil {
		return res, err
	}
	defData, err := MarshalUserConfig(DefaultConfig())
	if err != nil {
		return res, err
	}
	defaults, err := parseLayer(defData)
	if err != nil {
		return res, err
	}
	var leaves [][]string
	collectLeaves(main.Values, nil, &leaves)
	var candidates [][]string
	for _, p := range leaves {
		v, _ := lookupPath(main.Values, p)
		if d, ok := lookupPath(defaults, p); ok && reflect.DeepEqual(d, v) {
			candidates = append(candidates, p)
		}
	}
	same := func(drop [][]string) bool {
		values := deepCopy(main.Values).(map[string]any)
		for _, p := range drop {
			deletePath(values, p)
		}
		after, err := with(values)
		return err == nil && reflect.DeepEqual(after, before)
	}
	var drop [][]string
	if same(candidates) {
		drop = candidates
	} else {
		for _, p := range candidates {
			if same(append(drop[:len(drop):len(drop)], p)) {
				drop = append(drop, p)
			}
		}
	}
	for _, p := range drop {
		res.Keys = append(res.Keys, displayKey(p))
		for i := len(lc.Layers) - 2; i >= 0; i-- {
			if _, ok := lookupPath(lc.Layers[i].Values, p); ok {
				res.Uncovered = append(res.Uncovered, KeyOrigin{Key: displayKey(p), File: lc.Layers[i].Path})
				break
			}
		}
	}
	if dryRun || len(drop) == 0 {
		return res, nil
	}
	if !main.Writable() {
		return res, errfmt.Errorf("tuios cannot write %s", main.Path)
	}
	changes := make([]configChange, 0, len(drop))
	for _, p := range drop {
		changes = append(changes, configChange{path: p, deleted: true})
	}
	out, want, ok, err := editFile(main.Data, changes)
	if err != nil {
		return res, err
	}
	if ok {
		e := newTOMLEdit(out)
		e.dropEmptyTables()
		if got, perr := parseLayer(e.bytes()); perr == nil && reflect.DeepEqual(pruneEmpty(got), pruneEmpty(want)) {
			out = e.bytes()
		}
	} else {
		full, merr := marshalTable(want)
		if merr != nil {
			return res, merr
		}
		out = append([]byte(configFileHeader(path)), full...)
	}
	return res, writeConfigBytes(out, main.Path)
}

func pruneEmpty(m map[string]any) map[string]any {
	for k, v := range m {
		if sub, ok := v.(map[string]any); ok {
			pruneEmpty(sub)
			if len(sub) == 0 {
				delete(m, k)
			}
		}
	}
	return m
}

// DisplayPath writes p for a person: relative to the directory of config.toml
// when it is inside it, with ~ for the home directory otherwise.
func (lc *LayeredConfig) DisplayPath(p string) string { return displayPath(lc.Main, p) }

func displayPath(main, p string) string {
	if p == main {
		return filepath.Base(main)
	}
	dir := filepath.Dir(main)
	if rel, ok := pathroot.Within(dir, p); ok {
		return rel
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		if rel, ok := pathroot.Within(home, p); ok {
			return filepath.Join("~", rel)
		}
	}
	return p
}
