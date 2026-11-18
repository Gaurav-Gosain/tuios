package config

import (
	"reflect"
	"slices"
	"strings"
	"testing"
)

// configLeaves walks UserConfig and calls fn with the dotted path and the Go
// type of every key a person can write. A map of tables adds NAME as a part
// of the path. A map of values, a list and a list of tables are one key each.
func configLeaves(fn func(path string, t reflect.Type)) {
	var walk func(t reflect.Type, prefix string)
	walk = func(t reflect.Type, prefix string) {
		for t.Kind() == reflect.Pointer {
			t = t.Elem()
		}
		for f := range t.Fields() {
			tag, _, _ := strings.Cut(f.Tag.Get("toml"), ",")
			if tag == "-" {
				continue
			}
			ft := f.Type
			for ft.Kind() == reflect.Pointer {
				ft = ft.Elem()
			}
			if f.Anonymous && tag == "" {
				walk(ft, prefix)
				continue
			}
			if tag == "" {
				continue
			}
			path := prefix + tag
			switch {
			case ft.Kind() == reflect.Struct:
				walk(ft, path+".")
			case ft.Kind() == reflect.Map:
				et := ft.Elem()
				for et.Kind() == reflect.Pointer {
					et = et.Elem()
				}
				if et.Kind() == reflect.Struct {
					walk(et, path+".NAME.")
				} else {
					fn(path, ft)
				}
			default:
				fn(path, ft)
			}
		}
	}
	walk(reflect.TypeFor[UserConfig](), "")
}

// skipped reports whether fileKeysSkipped names the key: a key under a
// skipped table whose value is a map or a list, such as a section of
// bindings. A plain key under such a table still needs a row.
func skipped(path string, t reflect.Type) bool {
	for prefix := range fileKeysSkipped {
		if path != prefix && !strings.HasPrefix(path, prefix+".") {
			continue
		}
		if t.Kind() == reflect.Map || t.Kind() == reflect.Slice || strings.HasPrefix(path, "keybindings.menus.") {
			return true
		}
	}
	return false
}

// TestEveryConfigKeyIsDocumented fails on a key of UserConfig that neither
// the option registry nor fileKeys describes, so docs/config-options.md
// covers the whole file.
func TestEveryConfigKeyIsDocumented(t *testing.T) {
	var missing []string
	configLeaves(func(path string, ft reflect.Type) {
		if _, ok := LookupOption(path); ok {
			return
		}
		if IsFileKey(path) || skipped(path, ft) {
			return
		}
		missing = append(missing, path)
	})
	if len(missing) > 0 {
		t.Errorf("%d config keys have no row in the option registry or in fileKeys (file_keys.go):\n  %s",
			len(missing), strings.Join(missing, "\n  "))
	}
}

// fileKeyType is the Go type of the field a fileKeys path names. NAME walks
// into a map.
func fileKeyType(path string) (reflect.Type, bool) {
	t := reflect.TypeFor[UserConfig]()
	for p := range strings.SplitSeq(path, ".") {
		for t.Kind() == reflect.Pointer {
			t = t.Elem()
		}
		if p == "NAME" {
			if t.Kind() != reflect.Map {
				return nil, false
			}
			t = t.Elem()
			continue
		}
		if t.Kind() != reflect.Struct {
			return nil, false
		}
		f, ok := tomlField(t, p)
		if !ok {
			return nil, false
		}
		t = f
	}
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	return t, true
}

// TestFileKeysMatchTheConfig fails on a fileKeys row whose path is not in
// UserConfig, is in the option registry too, or names the wrong type.
func TestFileKeysMatchTheConfig(t *testing.T) {
	seen := map[string]bool{}
	for _, k := range fileKeys {
		if seen[k.Path] {
			t.Errorf("%s: two rows", k.Path)
		}
		seen[k.Path] = true
		if _, ok := LookupOption(k.Path); ok {
			t.Errorf("%s: in the option registry too; keep one row", k.Path)
		}
		if k.Description == "" || k.Section == "" {
			t.Errorf("%s: no description or section", k.Path)
		}
		ft, ok := fileKeyType(k.Path)
		if !ok {
			t.Errorf("%s: no such key in UserConfig", k.Path)
			continue
		}
		var want []string
		switch ft.Kind() {
		case reflect.Bool:
			want = []string{OptionBool}
		case reflect.Int, reflect.Int64:
			want = []string{OptionInt}
		case reflect.String:
			want = []string{OptionString}
		case reflect.Slice:
			if e := ft.Elem(); e.Kind() == reflect.Struct || e.Kind() == reflect.Interface {
				want = []string{FileKeyTables}
			} else {
				want = []string{FileKeyList}
			}
		case reflect.Map:
			if ft.Elem().Kind() == reflect.Interface {
				want = []string{FileKeyTable}
			} else {
				want = []string{FileKeyMap}
			}
		case reflect.Struct:
			want = []string{FileKeyTable}
		}
		if !slices.Contains(want, k.Type) {
			t.Errorf("%s: type %s, but the field is a %s, so %v", k.Path, k.Type, ft, want)
		}
	}
}

// TestFileKeyDefaultsComeFromTheCode fails on a Default written in fileKeys
// for a field that DefaultConfig sets: that default comes from DefaultConfig,
// so the two cannot say different things.
func TestFileKeyDefaultsComeFromTheCode(t *testing.T) {
	def := DefaultConfig()
	for _, k := range fileKeys {
		if v, ok := fileKeyDefault(def, k.Path); ok && k.Default != "" && k.Default != v {
			t.Errorf("%s: Default %q, but DefaultConfig sets %q. Remove the Default", k.Path, k.Default, v)
		}
	}
}

// fileKeySample is a path that a fileKeys row covers, with NAME as a name.
func fileKeySample(k FileKey) string {
	p := strings.ReplaceAll(k.Path, "NAME", "sample")
	if k.Type == FileKeyTable || k.Type == FileKeyTables || k.Type == FileKeyMap {
		p += ".sample"
	}
	return p
}

// TestFileKeysCannotBeSetAtRunTime holds the line between the two tables:
// set-option and look apply refuse every fileKeys path. The settings page
// is checked in internal/app (TestSettingsPageSetsNoFileKey). A look may set
// the dock keys of lookFileKeys, which only change how the bar looks.
func TestFileKeysCannotBeSetAtRunTime(t *testing.T) {
	for _, k := range fileKeys {
		for _, p := range []string{k.Path, fileKeySample(k)} {
			if _, ok := LookupOption(p); ok {
				t.Errorf("%s: the option registry carries it, so set-option can set it", p)
			}
			if err := SetOptionValue(DefaultConfig(), p, "1"); err == nil {
				t.Errorf("%s: set-option accepted it", p)
			}
			_, lookDock := lookFileKeys[p]
			if IsLookKey(p) && !lookDock {
				t.Errorf("%s: a look can set it", p)
			}
			if err := SetLookValue(DefaultConfig(), p, "1"); err == nil && !lookDock {
				t.Errorf("%s: look apply accepted it", p)
			}
		}
	}
}
