package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"

	"github.com/Gaurav-Gosain/tuios/internal/atomicfile"
)

// Take-over.
//
// config.toml wins over config.d/look.toml, so a key the person already set
// hides the look's value. A take-over lets the look win: it removes those keys
// from config.toml, and from any config.d file that comes after look.toml,
// line by line, so comments and order stay. The removed values are saved in
// config.d/look.saved.json, which is not a .toml file and so is never read as
// config. `tuios look remove` puts them back.
//
// The save keeps each file as it was before the first take-over and as tuios
// wrote it after. When the file is still what tuios wrote, the remove puts the
// old file back byte for byte. When the person changed it since, the remove
// puts back each saved key the file does not set, and keeps each key the
// person set again, which is the newer value.

// LookSavedName is the file in config.d that holds the values a take-over
// removed.
const LookSavedName = "look.saved.json"

// lookSavedVersion is the format of the saved file.
const lookSavedVersion = 1

// lookSaved is config.d/look.saved.json.
type lookSaved struct {
	Version int             `json:"version"`
	Files   []lookSavedFile `json:"files"`
}

// lookSavedFile is one file a take-over changed.
type lookSavedFile struct {
	// Path is the file, absolute.
	Path string `json:"path"`
	// Before is the file before the first take-over, when HasBefore.
	Before    string `json:"before,omitempty"`
	HasBefore bool   `json:"has_before"`
	// After is the file as the last take-over wrote it.
	After string `json:"after"`
	// Keys are the removed keys with their values, as TOML.
	Keys string `json:"keys"`
}

// LookSavedPath is the saved file for the config at mainPath.
func LookSavedPath(mainPath string) string {
	return filepath.Join(filepath.Dir(absPath(mainPath)), DropInDirName, LookSavedName)
}

// readLookSaved reads the saved file. It returns nil and no error when there
// is none.
func readLookSaved(mainPath string) (*lookSaved, error) {
	path := LookSavedPath(mainPath)
	data, err := os.ReadFile(path) //nolint:gosec // the user's own config.d
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var s lookSaved
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("tuios cannot read %s: %w", filepath.Join(DropInDirName, LookSavedName), err)
	}
	if s.Version != lookSavedVersion {
		return nil, fmt.Errorf("%s has version %d. This tuios reads version %d", filepath.Join(DropInDirName, LookSavedName), s.Version, lookSavedVersion)
	}
	return &s, nil
}

// LookTakeOver is what a take-over changes.
type LookTakeOver struct {
	// Files are the files tuios removes keys from.
	Files []LookTakeOverFile
	// Kept are the keys tuios cannot remove, with the file that keeps each.
	Kept []LookShadow
	// Changes are the options the take-over changes, on top of the look.
	Changes []LookChange

	saved *lookSaved
}

// LookTakeOverFile is one file and the keys a take-over removes from it.
type LookTakeOverFile struct {
	Path string // as a person reads it
	Keys []string

	abs string
	out []byte
}

// PlanTakeOver works out how to let the look set every key a later file
// hides. It writes nothing. Apply carries it out.
func (p *LookPlan) PlanTakeOver() (*LookTakeOver, error) {
	if p.Look == nil || len(p.Shadowed) == 0 || p.lc == nil {
		return nil, nil
	}
	lc := p.lc
	lookPath := LookPath(lc.Main)
	saved, err := readLookSaved(lc.Main)
	if err != nil {
		return nil, err
	}
	if saved == nil {
		saved = &lookSaved{Version: lookSavedVersion}
	}
	to := &LookTakeOver{}
	w := newLayerWriter(lc)
	for i, l := range lc.Layers {
		if l.Kind != LayerMain && (l.Kind != LayerDropIn || l.isLook() || l.Path < lookPath) {
			continue
		}
		var keys [][]string
		for _, s := range p.Look.Settings {
			key := splitDotted(s.Path)
			if _, ok := lookupPath(l.Values, key); ok {
				keys = append(keys, key)
			}
		}
		if len(keys) == 0 {
			continue
		}
		shown := displayPath(lc.Main, l.Path)
		keep := func() {
			for _, k := range keys {
				to.Kept = append(to.Kept, LookShadow{Path: displayKey(k), File: shown})
			}
		}
		if !w.writable(i) {
			keep()
			continue
		}
		out, removed, ok, err := takeOverEdit(l, keys)
		if err != nil {
			return nil, fmt.Errorf("failed to parse %s: %w", shown, err)
		}
		if !ok {
			keep()
			continue
		}
		f := LookTakeOverFile{Path: shown, abs: l.Path, out: out}
		for _, k := range keys {
			f.Keys = append(f.Keys, displayKey(k))
		}
		to.Files = append(to.Files, f)
		if err := saved.add(l.Path, l.Data, out, removed); err != nil {
			return nil, err
		}
	}
	to.saved = saved
	if p.next != nil && len(to.Files) > 0 {
		taken := &LayeredConfig{Main: p.next.Main, Layered: true, Layers: slices.Clone(p.next.Layers)}
		r := &LookRestore{Files: to.Files}
		if err := r.restoreLayers(taken); err != nil {
			return nil, err
		}
		if to.Changes, err = lookChanges(p.next, taken); err != nil {
			return nil, err
		}
	}
	return to, nil
}

// takeOverEdit removes keys from the lines of layer l. It returns the new
// file, the removed values as a table, and whether the line edit could say
// it. A file whose lines tuios cannot edit is never written again from its
// values: that would lose the comments.
func takeOverEdit(l ConfigLayer, keys [][]string) ([]byte, map[string]any, bool, error) {
	changes := make([]configChange, 0, len(keys))
	removed := map[string]any{}
	for _, k := range keys {
		v, _ := lookupPath(l.Values, k)
		setPath(removed, k, deepCopy(v))
		changes = append(changes, configChange{path: k, deleted: true})
	}
	out, want, ok, err := editFile(l.Data, changes)
	if err != nil || !ok {
		return nil, nil, false, err
	}
	// A table left with no key goes too, as a prune does, when the file
	// still says the same without it.
	e := newTOMLEdit(out)
	e.dropEmptyTables()
	if got, perr := parseLayer(e.bytes()); perr == nil && reflect.DeepEqual(pruneEmpty(got), pruneEmpty(want)) {
		out = e.bytes()
	}
	return out, removed, true, nil
}

// add records that a take-over changed the file at path from before to
// after, removing the values in removed.
func (s *lookSaved) add(path string, before, after []byte, removed map[string]any) error {
	i := slices.IndexFunc(s.Files, func(f lookSavedFile) bool { return f.Path == path })
	if i < 0 {
		s.Files = append(s.Files, lookSavedFile{Path: path, Before: string(before), HasBefore: true})
		i = len(s.Files) - 1
	}
	f := &s.Files[i]
	// An earlier take-over saved this file. Its first copy still holds when
	// the file is as that take-over left it. When the person changed it
	// since, the copy would undo that change, so it goes.
	if f.After != "" && f.After != string(before) {
		f.HasBefore, f.Before = false, ""
	}
	keys := map[string]any{}
	if f.Keys != "" {
		old, err := parseLayer([]byte(f.Keys))
		if err != nil {
			return err
		}
		keys = old
	}
	// A key saved earlier keeps its first value: that is the person's own.
	mergeTables(removed, keys)
	data, err := marshalTable(removed)
	if err != nil {
		return err
	}
	f.Keys = string(data)
	f.After = string(after)
	return nil
}

// write puts the take-over on disk: the saved values first, so a failure
// later never loses them, then each file.
func (to *LookTakeOver) write(mainPath string) error {
	if to == nil || len(to.Files) == 0 {
		return nil
	}
	data, err := json.MarshalIndent(to.saved, "", "  ")
	if err != nil {
		return err
	}
	path := LookSavedPath(mainPath)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	if err := atomicfile.Write(path, append(data, '\n'), 0o600, true); err != nil {
		return fmt.Errorf("failed to write %s: %w", path, err)
	}
	for _, f := range to.Files {
		if err := writeConfigBytes(f.out, f.abs); err != nil {
			return err
		}
	}
	return nil
}

// LookRestore is what a remove puts back.
type LookRestore struct {
	// Files are the files tuios puts saved keys back into.
	Files []LookTakeOverFile
	// Newer are the saved keys the person set again after the take-over.
	// tuios keeps the newer value.
	Newer []LookShadow
	// Kept are the saved keys tuios cannot put back, with their file.
	Kept []LookShadow
	// Exact is true for each file in Files that goes back byte for byte.
	Exact []bool
}

// planRestore works out what removing the look puts back. It returns nil
// when no take-over saved anything.
func planRestore(mainPath string) (*LookRestore, error) {
	saved, err := readLookSaved(mainPath)
	if err != nil || saved == nil {
		return nil, err
	}
	r := &LookRestore{}
	for _, f := range saved.Files {
		shown := displayPath(absPath(mainPath), f.Path)
		cur, err := os.ReadFile(f.Path) //nolint:gosec // a file of the user's config
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return nil, err
		}
		if f.HasBefore && string(cur) == f.After {
			r.Files = append(r.Files, LookTakeOverFile{Path: shown, abs: f.Path, out: []byte(f.Before), Keys: savedKeyNames(f.Keys)})
			r.Exact = append(r.Exact, true)
			continue
		}
		keys, err := parseLayer([]byte(f.Keys))
		if err != nil {
			return nil, err
		}
		values, err := parseLayer(cur)
		if err != nil {
			return nil, fmt.Errorf("failed to parse %s: %w", shown, err)
		}
		var leaves [][]string
		collectLeaves(keys, nil, &leaves)
		var changes []configChange
		var names []string
		for _, k := range leaves {
			if _, ok := lookupPath(values, k); ok {
				r.Newer = append(r.Newer, LookShadow{Path: displayKey(k), File: shown})
				continue
			}
			v, _ := lookupPath(keys, k)
			changes = append(changes, configChange{path: k, value: v})
			names = append(names, displayKey(k))
		}
		if len(changes) == 0 {
			continue
		}
		out, _, ok, err := editFile(cur, changes)
		if err != nil {
			return nil, fmt.Errorf("failed to parse %s: %w", shown, err)
		}
		if !ok || !fileWritable(f.Path) {
			for _, n := range names {
				r.Kept = append(r.Kept, LookShadow{Path: n, File: shown})
			}
			continue
		}
		r.Files = append(r.Files, LookTakeOverFile{Path: shown, abs: f.Path, out: out, Keys: names})
		r.Exact = append(r.Exact, false)
	}
	return r, nil
}

// savedKeyNames lists the dotted keys of a saved TOML table.
func savedKeyNames(data string) []string {
	keys, err := parseLayer([]byte(data))
	if err != nil {
		return nil
	}
	var leaves [][]string
	collectLeaves(keys, nil, &leaves)
	out := make([]string, 0, len(leaves))
	for _, k := range leaves {
		out = append(out, displayKey(k))
	}
	return out
}

// restoreLayers puts the new text of each file into the layers of next, so a
// change list shows the values the files will hold.
func (r *LookRestore) restoreLayers(next *LayeredConfig) error {
	if r == nil {
		return nil
	}
	for _, f := range r.Files {
		for i := range next.Layers {
			if next.Layers[i].Path != f.abs {
				continue
			}
			values, err := parseLayer(f.out)
			if err != nil {
				return err
			}
			delete(values, IncludeKey)
			next.Layers[i].Data, next.Layers[i].Values = f.out, values
		}
	}
	return nil
}

// write puts the saved values back and deletes the saved file. A key tuios
// could not put back stays in the saved file, so it is not lost.
func (r *LookRestore) write(mainPath string) error {
	if r == nil {
		return nil
	}
	for _, f := range r.Files {
		if err := writeConfigBytes(f.out, f.abs); err != nil {
			return err
		}
	}
	if len(r.Kept) > 0 {
		return nil
	}
	if err := os.Remove(LookSavedPath(mainPath)); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}

// ShadowFiles are the files the shadows name, in order of first mention.
func ShadowFiles(shadows []LookShadow) []string {
	var out []string
	for _, s := range shadows {
		if !slices.Contains(out, s.File) {
			out = append(out, s.File)
		}
	}
	return out
}

// ShadowKeys joins the keys of shadows that name file.
func ShadowKeys(shadows []LookShadow, file string) string {
	var keys []string
	for _, s := range shadows {
		if s.File == file {
			keys = append(keys, s.Path)
		}
	}
	return strings.Join(keys, ", ")
}
