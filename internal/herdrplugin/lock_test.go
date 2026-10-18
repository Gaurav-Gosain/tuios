package herdrplugin

import (
	"path/filepath"
	"strings"
	"testing"
)

// A security boundary: plugins.lock can come from a dotfiles repository the
// person did not write, and SOURCE comes from a web page. A subdir or a ref
// must not leave the checkout or reach git as an option, and a lock entry
// must not name a folder outside the data folder.
//
// Ways it could fail: a .. part, an absolute or Windows path, a cleaned path
// that differs from the written one, a leading dash, a control character, an
// id that escapes through PathComponent, a short or upper-case commit.
func TestLockPathsStayInside(t *testing.T) {
	for _, bad := range []string{"..", "../x", "a/../../x", "a/..", "/etc", "a//b", "a/./b", "./a", "a/", `a\..\b`, "C:/x", "-x", "a\x00b", "a\nb"} {
		if CheckSubdir(bad) == nil {
			t.Errorf("CheckSubdir(%q) passed", bad)
		}
	}
	for _, good := range []string{"", "plugin", "plugins/sidebar", "a.b/c-d_e"} {
		if err := CheckSubdir(good); err != nil {
			t.Errorf("CheckSubdir(%q): %v", good, err)
		}
	}
	for _, bad := range []string{"-c", "--upload-pack=x", "a..b", "a b", "a~1", "a^", "a:b", "a\x01"} {
		if CheckRef(bad) == nil {
			t.Errorf("CheckRef(%q) passed", bad)
		}
	}
	for _, good := range []string{"", "main", "v0.3.1", "feature/x", "refs/tags/v1"} {
		if err := CheckRef(good); err != nil {
			t.Errorf("CheckRef(%q): %v", good, err)
		}
	}

	commit := strings.Repeat("ab", 20)
	ok := LockEntry{ID: "e2e.probe", Source: "https://example.com/r.git", Commit: commit, Subdir: "p"}
	if err := CheckLockEntry(ok); err != nil {
		t.Fatalf("a valid entry: %v", err)
	}
	for name, change := range map[string]func(*LockEntry){
		"slash id":       func(e *LockEntry) { e.ID = "a/b" },
		"padded id":      func(e *LockEntry) { e.ID = " a" },
		"short commit":   func(e *LockEntry) { e.Commit = commit[:12] },
		"upper commit":   func(e *LockEntry) { e.Commit = strings.ToUpper(commit) },
		"subdir escape":  func(e *LockEntry) { e.Subdir = "../../.ssh" },
		"option ref":     func(e *LockEntry) { e.Ref = "--upload-pack=sh" },
		"option source":  func(e *LockEntry) { e.Source = "--upload-pack=sh" },
		"relative local": func(e *LockEntry) { e.Source = LocalPrefix + "x" },
	} {
		e := ok
		change(&e)
		if CheckLockEntry(e) == nil {
			t.Errorf("%s: CheckLockEntry passed %+v", name, e)
		}
	}

	// herdr's id rules allow "." and "..". PathComponent keeps them inside.
	d := Dirs{TuiosData: t.TempDir()}
	for _, id := range []string{ok.ID, "..", ".", "a..b"} {
		e := ok
		e.ID = id
		root := d.PluginRoot(e)
		if rel, err := filepath.Rel(d.ManagedDir(), root); err != nil || rel == ".." || strings.HasPrefix(rel, "../") || !strings.HasPrefix(rel, PathComponent(id)+string(filepath.Separator)) {
			t.Errorf("PluginRoot of %q is %s, outside its folder in %s", id, root, d.ManagedDir())
		}
	}
}
