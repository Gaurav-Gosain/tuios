package herdrplugin

import (
	"fmt"
	"os"
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
	for _, bad := range []string{"ext::sh -c x", "-u", "https://a/b\x1b[2J"} {
		if CheckSource(bad) == nil {
			t.Errorf("CheckSource(%q) passed", bad)
		}
	}
	for _, good := range []string{"", "main", "v0.3.1", "feature/x", "refs/tags/v1", "pkg@1.0"} {
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
		"dot id":         func(e *LockEntry) { e.ID = ".hidden" },
		"dotdot id":      func(e *LockEntry) { e.ID = ".." },
		"plain http":     func(e *LockEntry) { e.Source = "http://example.com/r.git" },
	} {
		e := ok
		change(&e)
		if CheckLockEntry(e) == nil {
			t.Errorf("%s: CheckLockEntry passed %+v", name, e)
		}
	}

	// A state dir must be one folder in plugins/<id>/.
	for _, bad := range []string{"", "plugins/other/x", "plugins/e2e.probe/..", "plugins/e2e.probe/a/../../../x", "/etc", "plugins/e2e.probe/", "plugins/e2e.probe/a\\b"} {
		if CheckDir(ok.ID, bad) == nil {
			t.Errorf("CheckDir(%q) passed", bad)
		}
	}
	// herdr's id rules allow "." and "..". PathComponent keeps them inside.
	d := Dirs{TuiosData: t.TempDir()}
	for _, id := range []string{ok.ID, "..", ".", "a..b"} {
		e := ok
		e.ID = id
		dir := NewDir(e)
		if err := CheckDir(id, dir); err != nil {
			t.Errorf("NewDir for %q: %v", id, err)
		}
		root := d.PluginRoot(e, dir)
		if rel, err := filepath.Rel(d.ManagedDir(), root); err != nil || rel == ".." || strings.HasPrefix(rel, "../") || !strings.HasPrefix(rel, PathComponent(id)+string(filepath.Separator)) {
			t.Errorf("PluginRoot of %q is %s, outside its folder in %s", id, root, d.ManagedDir())
		}
	}
}

// A security boundary: the install prompt prints a manifest's argv,
// version, events and placements, so a terminal control in any of them
// could rewrite what the person approves. Parse refuses them.
//
// Ways it could fail: a check on one field and not another, ESC let through
// as printable, a C1 or bidi control let through, a tab refused.
func TestManifestRefusesTerminalControls(t *testing.T) {
	const base = `id = "e2e.probe"
name = "Probe"
version = "%VERSION%"
min_herdr_version = "0.9.3"
[[build]]
command = ["sh", "%ARG%"]
[[events]]
on = "%EVENT%"
command = ["true"]
[[panes]]
id = "p"
title = "P"
placement = "%PLACE%"
command = ["true"]
`
	fill := func(field, value string) string {
		s := base
		for k, v := range map[string]string{"%VERSION%": "1.0.0", "%ARG%": "ok\tok", "%EVENT%": "pane.created", "%PLACE%": "split"} {
			if k == field {
				v = value
			}
			s = strings.ReplaceAll(s, k, v)
		}
		return s
	}
	if _, perr := Parse([]byte(fill("", ""))); perr != nil {
		t.Fatalf("the clean manifest, with a tab in an argument: %v", perr)
	}
	for _, field := range []string{"%VERSION%", "%ARG%", "%EVENT%", "%PLACE%"} {
		for _, bad := range []string{`\u001b[2J`, `x\u001b]0;t\u0007`, `\r`, `\u009b1A`, `\u202e`, `\u007f`} {
			if _, perr := Parse([]byte(fill(field, "ok"+bad+"x"))); perr == nil {
				t.Errorf("Parse took %q in %s", bad, field)
			}
		}
	}
}

// A manifest that is a link to a file outside the checkout is not inside
// it, and a plain file is.
func TestInsideRefusesALinkOut(t *testing.T) {
	checkout, outside := t.TempDir(), t.TempDir()
	target := filepath.Join(outside, ManifestName)
	if err := os.WriteFile(target, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(checkout, ManifestName)
	if err := os.Symlink(target, link); err != nil {
		t.Skip("no symlinks here:", err)
	}
	plain := filepath.Join(checkout, "plain.toml")
	_ = os.WriteFile(plain, nil, 0o600)
	if Inside(link, checkout) || Inside(target, checkout) || !Inside(plain, checkout) {
		t.Fatalf("Inside: link %v, target %v, plain %v; want false, false, true", Inside(link, checkout), Inside(target, checkout), Inside(plain, checkout))
	}
}

// A security boundary: a [[theme]] or [[saver]] file comes from a checkout
// the person did not write, and git checks out a committed link as a link.
// Ways it could fail: the manifest names a link out of the folder (to a
// secret, to /dev/zero, to a FIFO), a file under a link to a folder out, a
// link that does not resolve, or a refusal of a plain file, of a link that
// stays inside, or of a file not there yet.
func TestManifestFilesStayInside(t *testing.T) {
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secret.json"), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		name, file, link, target string
		ok                       bool
	}{
		{"link to a file out", "theme.json", "theme.json", filepath.Join(outside, "secret.json"), false},
		{"link to a device", "theme.json", "theme.json", os.DevNull, false},
		{"file under a link to a folder out", "out/secret.json", "out", outside, false},
		{"link that dangles", "theme.json", "theme.json", filepath.Join(outside, "gone.json"), false},
		{"link inside the folder", "theme.json", "theme.json", filepath.Join("in", "plain.json"), true},
		{"plain file", "in/plain.json", "", "", true},
		{"file not there yet", "later.json", "", "", true},
	} {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.MkdirAll(filepath.Join(dir, "in"), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, "in", "plain.json"), []byte("{}"), 0o600); err != nil {
				t.Fatal(err)
			}
			if c.link != "" {
				if err := os.Symlink(c.target, filepath.Join(dir, c.link)); err != nil {
					t.Skip("no symlinks here:", err)
				}
			}
			for _, part := range []string{"[[theme]]\nfile = %q\n", "[[saver]]\nid = \"s\"\nfile = %q\n"} {
				manifest := "api = 1\nid = \"example.files\"\nname = \"Files\"\nversion = \"0.1.0\"\n" + fmt.Sprintf(part, c.file)
				if err := os.WriteFile(filepath.Join(dir, NativeManifestName), []byte(manifest), 0o600); err != nil {
					t.Fatal(err)
				}
				if _, perr := Load(dir); (perr == nil) != c.ok {
					t.Fatalf("%sLoad: %v, want ok %v", strings.SplitN(part, "\n", 2)[0], perr, c.ok)
				}
			}
		})
	}
}
