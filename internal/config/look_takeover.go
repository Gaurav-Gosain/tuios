package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
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
// last wrote it. While the file is still what tuios wrote, every difference
// from the first copy is tuios's own, so the remove puts that copy back byte
// for byte. When the person changed the file since, the copy would undo their
// change, so it is dropped, and the remove puts back each saved key the file
// does not set. A key the person set again keeps the newer value.
//
// A look applied over another puts back the saved keys the new look does not
// set: the old look took them, and the new one has no value for them.
//
// Every file is read again just before tuios writes it. A file that changed
// since the plan read it stops the command, so an edit made while tuios asked
// its questions is never lost.

// LookSavedName is the file in config.d that holds the values a take-over
// removed.
const LookSavedName = "look.saved.json"

// lookSavedVersion is the format of the saved file.
const lookSavedVersion = 1

// lookSavedError is a saved file tuios cannot read.
type lookSavedError struct{ msg string }

func (e *lookSavedError) Error() string { return e.msg }

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
	// After is the file as tuios last wrote it. An empty file is a file too,
	// so it is compared as it is, with no test for empty.
	After string `json:"after"`
	// Keys are the removed keys with their values, as TOML.
	Keys string `json:"keys"`
}

// LookSavedPath is the saved file for the config at mainPath.
func LookSavedPath(mainPath string) string {
	return filepath.Join(filepath.Dir(absPath(mainPath)), DropInDirName, LookSavedName)
}

// lookSavedBrokenPath is a free name to move a saved file tuios cannot read
// to. It is not a .toml file, so the config never reads it.
func lookSavedBrokenPath(mainPath string) string {
	base := LookSavedPath(mainPath) + ".broken"
	p := base
	for n := 2; ; n++ {
		if _, err := os.Lstat(p); errors.Is(err, fs.ErrNotExist) {
			return p
		}
		p = base + "-" + strconv.Itoa(n)
	}
}

// readLookSaved reads the saved file. It returns nil and no error when there
// is none.
func readLookSaved(mainPath string) (*lookSaved, error) {
	path := LookSavedPath(mainPath)
	data, err := os.ReadFile(path) //nolint:gosec // the user's own config.d
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	name := filepath.Join(DropInDirName, LookSavedName)
	broken := func(why string) error {
		return &lookSavedError{fmt.Sprintf("tuios cannot read %s: %s. It holds the values a take-over saved. Fix the file, or run \"tuios look remove\" to continue without them", name, lookOneLine(why))}
	}
	if err != nil {
		return nil, broken(err.Error())
	}
	var s lookSaved
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, broken(err.Error())
	}
	if s.Version != lookSavedVersion {
		return nil, broken(fmt.Sprintf("it has version %d, and this tuios reads version %d", s.Version, lookSavedVersion))
	}
	for _, f := range s.Files {
		if !filepath.IsAbs(f.Path) {
			return nil, broken("a file in it has no full path")
		}
		if _, err := parseLayer([]byte(f.Keys)); err != nil {
			return nil, broken(err.Error())
		}
	}
	return &s, nil
}

// writeLookSaved writes s, or deletes the saved file when s holds nothing.
// A nil s leaves the file as it is.
func writeLookSaved(mainPath string, s *lookSaved) error {
	if s == nil {
		return nil
	}
	path := LookSavedPath(mainPath)
	if len(s.Files) == 0 {
		if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		return nil
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	if err := atomicfile.Write(path, append(data, '\n'), 0o600, true); err != nil {
		return fmt.Errorf("failed to write %s: %w", path, err)
	}
	return nil
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

// LookTakeOverFile is one file and the keys a take-over removes from it, or a
// remove puts back.
type LookTakeOverFile struct {
	Path string // as a person reads it
	Keys []string
	// Exact is true when a remove puts the file back byte for byte.
	Exact bool

	abs  string
	out  []byte
	disk []byte // the file as the plan read it
}

// PlanTakeOver works out how to let the look set every key a later file
// hides. It writes nothing. Apply carries it out.
func (p *LookPlan) PlanTakeOver() (*LookTakeOver, error) {
	if p.Look == nil || len(p.Shadowed) == 0 || p.next == nil {
		return nil, nil
	}
	next := p.next
	look := slices.IndexFunc(next.Layers, func(l ConfigLayer) bool { return l.Path == LookPath(next.Main) })
	if look < 0 {
		return nil, nil
	}
	saved := &lookSaved{Version: lookSavedVersion}
	if p.Restore != nil {
		saved = p.Restore.saved.clone()
	} else if s, err := readLookSaved(next.Main); err != nil {
		return nil, err
	} else if s != nil {
		saved = s
	}
	to := &LookTakeOver{}
	w := newLayerWriter(next)
	// The layers after the look win over it, in the order the config merges
	// them: config.d files after look.toml, the files they include, and
	// config.toml.
	for i := look + 1; i < len(next.Layers); i++ {
		l := next.Layers[i]
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
		shown := displayPath(next.Main, l.Path)
		keep := func() {
			for _, k := range keys {
				to.Kept = append(to.Kept, LookShadow{Path: displayKey(k), File: shown, Include: l.Kind == LayerInclude})
			}
		}
		// An included file is the person's own work, shared or generated,
		// and tuios does not take keys out of it.
		if l.Kind == LayerInclude || !w.writable(i) {
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
	if len(to.Files) > 0 {
		taken := &LayeredConfig{Main: next.Main, Layered: true, Layers: slices.Clone(next.Layers)}
		if err := (&LookRestore{Files: to.Files}).restoreLayers(taken); err != nil {
			return nil, err
		}
		var err error
		if to.Changes, err = lookChanges(next, taken); err != nil {
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

// clone is a copy of s that add can change.
func (s *lookSaved) clone() *lookSaved {
	if s == nil {
		return &lookSaved{Version: lookSavedVersion}
	}
	return &lookSaved{Version: s.Version, Files: slices.Clone(s.Files)}
}

// add records that a take-over changed the file at path from current to
// after, removing the values in removed. current is the file as it is now.
func (s *lookSaved) add(path string, current, after []byte, removed map[string]any) error {
	i := slices.IndexFunc(s.Files, func(f lookSavedFile) bool { return f.Path == path })
	if i < 0 {
		s.Files = append(s.Files, lookSavedFile{Path: path, Before: string(current), HasBefore: true, After: string(current)})
		i = len(s.Files) - 1
	}
	f := &s.Files[i]
	// An earlier take-over saved this file. Its first copy still holds when
	// the file is as tuios left it. When the person changed it since, the
	// copy would undo that change, so it goes.
	if f.After != string(current) {
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
	// The file sets each key in removed now, so that value is the person's
	// latest. An older saved value stays only for a key the file no longer
	// sets.
	mergeTables(keys, removed)
	data, err := marshalTable(keys)
	if err != nil {
		return err
	}
	f.Keys = string(data)
	f.After = string(after)
	return nil
}

// LookRestore is what a plan puts back from the saved values.
type LookRestore struct {
	// Files are the files tuios puts saved keys back into.
	Files []LookTakeOverFile
	// Newer are the saved keys the person set again after the take-over.
	// tuios keeps the newer value.
	Newer []LookShadow
	// Kept are the saved keys tuios cannot put back, with their file. They
	// stay saved.
	Kept []LookShadow
	// Gone are the saved keys of a file the person deleted. tuios does not
	// make the file again.
	Gone []LookShadow

	// saved is the saved file after the restore.
	saved *lookSaved
}

// planRestore works out what to put back from the saved values. skip, when
// set, names keys that stay saved: the keys a new look sets. It returns nil
// when no take-over saved anything.
func planRestore(mainPath string, skip func(string) bool) (*LookRestore, error) {
	saved, err := readLookSaved(mainPath)
	if err != nil || saved == nil {
		return nil, err
	}
	main := absPath(mainPath)
	r := &LookRestore{saved: &lookSaved{Version: lookSavedVersion}}
	for _, f := range saved.Files {
		shown := displayPath(main, f.Path)
		keys, err := parseLayer([]byte(f.Keys))
		if err != nil {
			return nil, err
		}
		var leaves, back, stay [][]string
		collectLeaves(keys, nil, &leaves)
		for _, k := range leaves {
			if skip != nil && skip(displayKey(k)) {
				stay = append(stay, k)
			} else {
				back = append(back, k)
			}
		}
		cur, err := os.ReadFile(f.Path) //nolint:gosec // a file of the user's config
		if errors.Is(err, fs.ErrNotExist) {
			// The person deleted the file. Making it again would undo that.
			for _, k := range back {
				r.Gone = append(r.Gone, LookShadow{Path: displayKey(k), File: shown})
			}
			if len(stay) > 0 {
				r.saved.Files = append(r.saved.Files, lookSavedFile{Path: f.Path, Keys: savedKeys(keys, stay)})
			}
			continue
		}
		if err != nil {
			return nil, err
		}
		chain := f.HasBefore && string(cur) == f.After
		if chain && len(stay) == 0 && len(back) > 0 {
			r.Files = append(r.Files, LookTakeOverFile{Path: shown, abs: f.Path, out: []byte(f.Before), disk: cur, Keys: dottedKeys(back), Exact: true})
			continue
		}
		values, err := parseLayer(cur)
		if err != nil {
			return nil, fmt.Errorf("failed to parse %s: %w", shown, err)
		}
		var changes []configChange
		for _, k := range back {
			if _, ok := lookupPath(values, k); ok {
				r.Newer = append(r.Newer, LookShadow{Path: displayKey(k), File: shown})
				continue
			}
			v, _ := lookupPath(keys, k)
			changes = append(changes, configChange{path: k, value: v})
		}
		after := cur
		if len(changes) > 0 {
			out, _, ok, err := editFile(cur, changes)
			if err != nil {
				return nil, fmt.Errorf("failed to parse %s: %w", shown, err)
			}
			var put [][]string
			for _, ch := range changes {
				put = append(put, ch.path)
			}
			if ok && fileWritable(f.Path) {
				r.Files = append(r.Files, LookTakeOverFile{Path: shown, abs: f.Path, out: out, disk: cur, Keys: dottedKeys(put)})
				after = out
			} else {
				for _, k := range put {
					r.Kept = append(r.Kept, LookShadow{Path: displayKey(k), File: shown})
				}
				stay = append(stay, put...)
			}
		}
		if len(stay) > 0 {
			e := lookSavedFile{Path: f.Path, Keys: savedKeys(keys, stay), After: string(after)}
			if chain {
				e.HasBefore, e.Before = true, f.Before
			}
			r.saved.Files = append(r.saved.Files, e)
		}
	}
	return r, nil
}

// savedKeys is the TOML of the keys at paths in table.
func savedKeys(table map[string]any, paths [][]string) string {
	out := map[string]any{}
	for _, k := range paths {
		v, _ := lookupPath(table, k)
		setPath(out, k, v)
	}
	data, err := marshalTable(out)
	if err != nil {
		return ""
	}
	return string(data)
}

// dottedKeys are the dotted names of paths.
func dottedKeys(paths [][]string) []string {
	out := make([]string, 0, len(paths))
	for _, k := range paths {
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

// fileWrite is one config file Apply writes.
type fileWrite struct {
	abs, shown string
	out        []byte
	disk       diskFile
}

// configWrites lists the config files the plan writes, and the saved file
// after it, and checks that no file changed since the plan read it. A
// take-over writes over the restore of the same file: it was planned on top
// of it.
func (p *LookPlan) configWrites() ([]fileWrite, *lookSaved, error) {
	var out []fileWrite
	add := func(f LookTakeOverFile) {
		w := fileWrite{abs: f.abs, shown: f.Path, out: f.out, disk: p.disk[f.abs]}
		if i := slices.IndexFunc(out, func(o fileWrite) bool { return o.abs == f.abs }); i >= 0 {
			out[i].out = f.out
			return
		}
		out = append(out, w)
	}
	var saved *lookSaved
	if p.Restore != nil {
		for _, f := range p.Restore.Files {
			add(f)
		}
		saved = p.Restore.saved
	}
	if p.TakeOver != nil && len(p.TakeOver.Files) > 0 {
		for _, f := range p.TakeOver.Files {
			add(f)
		}
		saved = p.TakeOver.saved
	}
	for _, w := range out {
		data, err := os.ReadFile(w.abs) //nolint:gosec // a file of the user's config
		existed := err == nil
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return nil, nil, err
		}
		if existed != w.disk.existed || !bytes.Equal(data, w.disk.data) {
			return nil, nil, fmt.Errorf("%s changed while tuios asked. tuios did not change the look. Run the command again", w.shown)
		}
	}
	return out, saved, nil
}

// writeConfigFiles writes each file of a plan.
func writeConfigFiles(writes []fileWrite) error {
	for _, w := range writes {
		if err := writeConfigBytes(w.out, w.abs); err != nil {
			return err
		}
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
