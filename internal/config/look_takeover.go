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

	"github.com/Gaurav-Gosain/tuios/internal/errfmt"
	"github.com/Gaurav-Gosain/tuios/internal/jsonx"
	"github.com/Gaurav-Gosain/tuios/internal/sortx"
)

// LookSavedName is the file in config.d that holds the values a take-over
// removed.
const LookSavedName = "look.saved.json"

const lookSavedVersion = 1

type lookSavedError struct{ msg string }

func (e *lookSavedError) Error() string { return e.msg }

type lookSaved struct {
	Version int             `json:"version"`
	Files   []lookSavedFile `json:"files"`
}

type lookSavedFile struct {
	Path      string `json:"path"`
	Before    string `json:"before,omitempty"`
	HasBefore bool   `json:"has_before"`
	After     string `json:"after"`
	Keys      string `json:"keys"`
}

// LookSavedPath is the saved file for the config at mainPath.
func LookSavedPath(mainPath string) string {
	return filepath.Join(filepath.Dir(absPath(mainPath)), DropInDirName, LookSavedName)
}

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
	if err := jsonx.Unmarshal(data, &s); err != nil {
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
	disk []byte
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
		if l.Kind == LayerInclude || !w.writable(i) {
			keep()
			continue
		}
		out, removed, ok, err := takeOverEdit(l, keys)
		if err != nil {
			return nil, errfmt.Errorf("failed to parse %s: %w", shown, err)
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
	e := newTOMLEdit(out)
	e.dropEmptyTables()
	if got, perr := parseLayer(e.bytes()); perr == nil && reflect.DeepEqual(pruneEmpty(got), pruneEmpty(want)) {
		out = e.bytes()
	}
	return out, removed, true, nil
}

func (s *lookSaved) clone() *lookSaved {
	if s == nil {
		return &lookSaved{Version: lookSavedVersion}
	}
	return &lookSaved{Version: s.Version, Files: slices.Clone(s.Files)}
}

func (s *lookSaved) add(path string, current, after []byte, removed map[string]any) error {
	i := slices.IndexFunc(s.Files, func(f lookSavedFile) bool { return f.Path == path })
	if i < 0 {
		s.Files = append(s.Files, lookSavedFile{Path: path, Before: string(current), HasBefore: true, After: string(current)})
		i = len(s.Files) - 1
	}
	f := &s.Files[i]
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

	saved *lookSaved
}

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
			return nil, errfmt.Errorf("failed to parse %s: %w", shown, err)
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
				return nil, errfmt.Errorf("failed to parse %s: %w", shown, err)
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

func dottedKeys(paths [][]string) []string {
	out := make([]string, 0, len(paths))
	for _, k := range paths {
		out = append(out, displayKey(k))
	}
	return out
}

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

type fileWrite struct {
	abs, shown string
	out        []byte
	disk       diskFile
}

func (p *LookPlan) configWrites() ([]fileWrite, *lookSaved) {
	var out []fileWrite
	add := func(f LookTakeOverFile) {
		if i := slices.IndexFunc(out, func(o fileWrite) bool { return o.abs == f.abs }); i >= 0 {
			out[i].out = f.out
			return
		}
		out = append(out, fileWrite{abs: f.abs, shown: f.Path, out: f.out, disk: p.disk[f.abs]})
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
	main := absPath(p.Main)
	sortx.SortStableFunc(out, func(a, b fileWrite) int {
		switch {
		case a.abs == main && b.abs != main:
			return 1
		case b.abs == main && a.abs != main:
			return -1
		}
		return 0
	})
	return out, saved
}

type stagedWrite struct {
	target, tmp string
	data        []byte
	config      bool
}

func stageFile(path string, data []byte, config bool) (stagedWrite, error) {
	target, mode := path, os.FileMode(0o600)
	if config {
		if resolved, err := filepath.EvalSymlinks(path); err == nil {
			target = resolved
		}
		if info, err := os.Stat(target); err == nil {
			mode = info.Mode().Perm()
		}
	}
	dir := filepath.Dir(target)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return stagedWrite{}, err
	}
	f, err := os.CreateTemp(dir, "."+filepath.Base(target)+".*.tmp")
	if err != nil {
		return stagedWrite{}, errfmt.Errorf("failed to write %s: %w", path, err)
	}
	err = f.Chmod(mode)
	if err == nil {
		_, err = f.Write(data)
	}
	if err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		_ = os.Remove(f.Name())
		return stagedWrite{}, errfmt.Errorf("failed to write %s: %w", path, err)
	}
	return stagedWrite{target: target, tmp: f.Name(), data: data, config: config}, nil
}

func (p *LookPlan) commitFiles(look []byte, before func() error) error {
	writes, saved := p.configWrites()
	var staged []stagedWrite
	drop := func() {
		for _, s := range staged {
			_ = os.Remove(s.tmp)
		}
	}
	add := func(path string, data []byte, config bool) error {
		s, err := stageFile(path, data, config)
		if err != nil {
			drop()
			return err
		}
		staged = append(staged, s)
		return nil
	}
	if look != nil {
		if err := add(LookPath(p.Main), look, false); err != nil {
			return err
		}
	}
	deleteSaved := saved != nil && len(saved.Files) == 0
	if saved != nil && !deleteSaved {
		data, err := json.MarshalIndent(saved, "", "  ")
		if err != nil {
			drop()
			return err
		}
		if err := add(LookSavedPath(p.Main), append(data, '\n'), false); err != nil {
			return err
		}
	}
	for _, w := range writes {
		if err := add(w.abs, w.out, true); err != nil {
			return err
		}
	}
	for _, w := range writes {
		data, err := os.ReadFile(w.abs) //nolint:gosec // a file of the user's config
		existed := err == nil
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			drop()
			return err
		}
		if existed != w.disk.existed || !bytes.Equal(data, w.disk.data) {
			drop()
			return errfmt.Errorf("%s changed while tuios asked. tuios did not change the look. Run the command again", w.shown)
		}
	}
	if before != nil {
		if err := before(); err != nil {
			drop()
			return err
		}
	}
	dirs := map[string]bool{}
	for i, s := range staged {
		if s.config {
			noteSelfWrite(s.data)
		}
		if err := os.Rename(s.tmp, s.target); err != nil {
			for _, rest := range staged[i:] {
				_ = os.Remove(rest.tmp)
			}
			return errfmt.Errorf("failed to write %s: %w", s.target, err)
		}
		dirs[filepath.Dir(s.target)] = true
	}
	if deleteSaved {
		if err := os.Remove(LookSavedPath(p.Main)); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
	}
	for dir := range dirs {
		if df, err := os.Open(dir); err == nil { //nolint:gosec // a folder of the user's config
			_ = df.Sync()
			_ = df.Close()
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
