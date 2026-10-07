package config

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

// Writing a config that is split over several files.
//
// A save never flattens the files into config.toml. It works out what changed
// against the config the files make now, and writes each change to the file
// that holds the key:
//
//   - A key some file sets is written to the last file that sets it, the one
//     whose value is in force.
//   - A new key goes to the last file that has its table, so a new
//     [hosts.NAME] lands beside the other hosts. A key whose table no file has
//     goes to config.toml.
//   - A removed key is removed from every file that sets it.
//   - A file tuios cannot write is never written. The change goes to
//     config.toml, which wins over every other file, and the WriteNote says
//     so. A removal from such a file cannot be done, and the note says that
//     too.
//
// A config with no include key and no config.d directory is saved the way it
// always was: config.toml rendered whole.

// WriteNote says where a save put a change when that is not the file that
// holds the key. The zero value has nothing to say.
type WriteNote struct {
	// Main is config.toml.
	Main string
	// Redirected are read-only files that hold a changed key. The change went
	// to Main instead.
	Redirected []string
	// Kept are read-only files that hold a key the save removed. The key is
	// still there.
	Kept []string
}

// Empty reports whether the note has nothing to say.
func (n WriteNote) Empty() bool { return len(n.Redirected) == 0 && len(n.Kept) == 0 }

// Message is the note as text for a person, empty when there is nothing to
// say.
func (n WriteNote) Message() string {
	var parts []string
	for _, f := range n.Redirected {
		parts = append(parts, fmt.Sprintf("tuios cannot write %s. It wrote the change to %s.", displayPath(n.Main, f), displayPath(n.Main, n.Main)))
	}
	for _, f := range n.Kept {
		parts = append(parts, fmt.Sprintf("tuios cannot write %s. Remove the setting there by hand.", displayPath(n.Main, f)))
	}
	return strings.Join(parts, " ")
}

func (n *WriteNote) addRedirected(p string) {
	if !containsString(n.Redirected, p) {
		n.Redirected = append(n.Redirected, p)
	}
}

func (n *WriteNote) addKept(p string) {
	if !containsString(n.Kept, p) {
		n.Kept = append(n.Kept, p)
	}
}

func containsString(list []string, s string) bool {
	for _, e := range list {
		if e == s {
			return true
		}
	}
	return false
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

// loadConfigFile is LoadLayered followed by the parse every reader wants. The
// warnings of the load travel on the config, so the client can show them.
func loadConfigFile(path string) (*UserConfig, *LayeredConfig, error) {
	lc, err := LoadLayered(path)
	if err != nil {
		return nil, nil, err
	}
	data, err := lc.Bytes()
	if err != nil {
		return nil, lc, err
	}
	cfg, err := ParseUserConfig(data)
	if err != nil {
		return nil, lc, err
	}
	cfg.LoadWarnings = append([]string(nil), lc.Warnings...)
	return cfg, lc, nil
}

// readConfigMerged is ReadConfigFile for a reader that takes a missing file
// as an empty one.
func readConfigMerged(path string) ([]byte, error) {
	if path == "" {
		return nil, fmt.Errorf("no config file path is set")
	}
	data, err := ReadConfigFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to read config file: %w", err)
	}
	return data, nil
}

// saveConfigData writes a whole config, given as the TOML of a UserConfig
// (data) and as the file a single-file config would hold (full).
func saveConfigData(path string, data, full []byte) (WriteNote, error) {
	lc, err := LoadLayered(path)
	if errors.Is(err, fs.ErrNotExist) {
		return WriteNote{}, writeConfigBytes(full, path)
	}
	if err != nil {
		return WriteNote{}, fmt.Errorf("the config files have an error, so tuios did not save: %w", err)
	}
	if !lc.Layered {
		return WriteNote{}, writeConfigBytes(full, path)
	}
	return saveLayered(lc, data)
}

// saveLayered writes the difference between the config the files make now and
// next (the TOML of a UserConfig) to the files that hold each key.
func saveLayered(lc *LayeredConfig, next []byte) (WriteNote, error) {
	note := WriteNote{Main: lc.Main}
	curData, err := lc.Bytes()
	if err != nil {
		return note, err
	}
	curCfg, err := ParseUserConfig(curData)
	if err != nil {
		return note, fmt.Errorf("the config files have an error, so tuios did not save: %w", err)
	}
	curTOML, err := MarshalUserConfig(curCfg)
	if err != nil {
		return note, err
	}
	cur, err := parseLayer(curTOML)
	if err != nil {
		return note, err
	}
	want, err := parseLayer(next)
	if err != nil {
		return note, err
	}
	var changes []configChange
	diffTables(cur, want, nil, &changes)
	if len(changes) == 0 {
		return note, nil
	}

	w := newLayerWriter(lc)
	for _, ch := range changes {
		if ch.deleted {
			for i := range lc.Layers {
				if _, ok := lookupPath(lc.Layers[i].Values, ch.path); !ok {
					continue
				}
				if !w.writable(i) {
					note.addKept(lc.Layers[i].Path)
					continue
				}
				w.add(i, ch)
			}
			continue
		}
		i := lc.ownerIndex(ch.path)
		if !w.writable(i) {
			note.addRedirected(lc.Layers[i].Path)
			i = len(lc.Layers) - 1
		}
		w.add(i, ch)
	}
	return note, w.flush()
}

// ownerIndex is the layer a change to path is written to, before the
// read-only check. It is the last layer that sets path. For a new key it is
// the last layer that has the table of named entries the key goes into, such
// as [hosts] for a new [hosts.NAME], so a new host lands beside the others.
// Any other new key goes to config.toml.
func (lc *LayeredConfig) ownerIndex(path []string) int {
	for i := len(lc.Layers) - 1; i >= 0; i-- {
		if _, ok := lookupPath(lc.Layers[i].Values, path); ok {
			return i
		}
	}
	for n := len(path) - 1; n >= 1; n-- {
		for i := len(lc.Layers) - 1; i >= 0; i-- {
			v, ok := lookupPath(lc.Layers[i].Values, path[:n])
			if ok && isEntryCollection(v) {
				return i
			}
		}
	}
	return len(lc.Layers) - 1
}

// isEntryCollection reports whether v holds named entries: a table whose
// every value is a table, such as [hosts], or a mergeable array of tables. A
// table with plain values, such as [appearance], is a table of settings, and
// holding one says nothing about where a new setting belongs.
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
		_, ok := tableArrayIdentity(v)
		return ok
	}
	return false
}

// WriteTarget is the file a change to key would be written to, and the note
// that says so when the file that holds it is read-only.
func (lc *LayeredConfig) WriteTarget(key []string) (string, WriteNote) {
	note := WriteNote{Main: lc.Main}
	i := lc.ownerIndex(key)
	if i != len(lc.Layers)-1 && !lc.Layers[i].Writable() {
		note.addRedirected(lc.Layers[i].Path)
		i = len(lc.Layers) - 1
	}
	return lc.Layers[i].Path, note
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

// layerWriter collects the changes for each file and writes each file once.
type layerWriter struct {
	lc       *LayeredConfig
	canWrite map[int]bool
	changes  map[int][]configChange
	order    []int
}

func newLayerWriter(lc *LayeredConfig) *layerWriter {
	return &layerWriter{lc: lc, canWrite: map[int]bool{}, changes: map[int][]configChange{}}
}

// writable is Writable for layer i, asked once per save. config.toml is always
// taken as writable: it is the file of last resort, and a failure to write it
// is reported as the error it is.
func (w *layerWriter) writable(i int) bool {
	if i == len(w.lc.Layers)-1 {
		return true
	}
	ok, seen := w.canWrite[i]
	if !seen {
		ok = w.lc.Layers[i].Writable()
		w.canWrite[i] = ok
	}
	return ok
}

func (w *layerWriter) add(i int, ch configChange) {
	if _, ok := w.changes[i]; !ok {
		w.order = append(w.order, i)
	}
	w.changes[i] = append(w.changes[i], ch)
}

// flush writes every file that has a change.
func (w *layerWriter) flush() error {
	for _, i := range w.order {
		layer := w.lc.Layers[i]
		// The file as read, include key and all, so the edit keeps it.
		values, err := parseLayer(layer.Data)
		if err != nil {
			return fmt.Errorf("failed to parse %s: %w", layer.Path, err)
		}
		for _, ch := range w.changes[i] {
			if ch.deleted {
				deletePath(values, ch.path)
				continue
			}
			setPath(values, ch.path, ch.value)
		}
		data, err := marshalTable(values)
		if err != nil {
			return fmt.Errorf("failed to write %s: %w", layer.Path, err)
		}
		if layer.Kind == LayerMain {
			data = append([]byte(configFileHeader(layer.Path)), data...)
		}
		if bytes.Equal(data, layer.Data) {
			continue
		}
		if err := writeConfigBytes(data, layer.Path); err != nil {
			return err
		}
	}
	return nil
}

// marshalTable writes a parsed table as TOML in the style of every file tuios
// writes. The include key, when there is one, goes first, where a person
// reading the file looks for it.
func marshalTable(values map[string]any) ([]byte, error) {
	var buf bytes.Buffer
	enc := toml.NewEncoder(&buf).SetIndentSymbol("  ")
	if inc, ok := values[IncludeKey]; ok {
		rest := make(map[string]any, len(values))
		for k, v := range values {
			if k != IncludeKey {
				rest[k] = v
			}
		}
		if err := toml.NewEncoder(&buf).Encode(map[string]any{IncludeKey: inc}); err != nil {
			return nil, err
		}
		buf.WriteByte('\n')
		values = rest
	}
	if err := enc.Encode(values); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
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
		return ""
	}
	inc, ok := values[IncludeKey]
	if !ok {
		return ""
	}
	out, err := toml.Marshal(map[string]any{IncludeKey: inc})
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// DisplayPath writes p for a person: relative to the directory of config.toml
// when it is inside it, with ~ for the home directory otherwise.
func (lc *LayeredConfig) DisplayPath(p string) string { return displayPath(lc.Main, p) }

// displayPath is DisplayPath for the config whose main file is main.
func displayPath(main, p string) string {
	if p == main {
		return filepath.Base(main)
	}
	dir := filepath.Dir(main)
	if rel, err := filepath.Rel(dir, p); err == nil && !strings.HasPrefix(rel, "..") {
		return rel
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		if rel, err := filepath.Rel(home, p); err == nil && !strings.HasPrefix(rel, "..") {
			return filepath.Join("~", rel)
		}
	}
	return p
}
