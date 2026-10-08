package herdrplugin

import (
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"testing/fstest"
)

// The ways the PATHEXT lookup can fail, which these tests hold it to:
//
//   - bin/herdr-nvim is not found when the file is bin/herdr-nvim.exe;
//   - an absolute path is not given the same lookup as a relative one;
//   - the extensions are tried in another order than PATHEXT's, so a .bat
//     shadows the .exe herdr would run;
//   - a name that already has a PATHEXT extension gets a second one;
//   - a name with a dot that is not an extension (herdr-nvim-1.2) is not
//     tried with one;
//   - a folder named like the program is taken as the program;
//   - an error other than "does not exist" is hidden by a guess;
//   - the lookup runs on Linux or macOS, where a name is the file's name.

func fakeStat(files ...string) func(string) (fs.FileInfo, error) {
	m := fstest.MapFS{}
	for _, f := range files {
		if d, ok := strings.CutSuffix(f, "/"); ok {
			m[d] = &fstest.MapFile{Mode: fs.ModeDir}
			continue
		}
		m[f] = &fstest.MapFile{}
	}
	return func(name string) (fs.FileInfo, error) {
		return fs.Stat(m, name)
	}
}

var winExts = splitPathExt(".COM;.EXE;.BAT;.CMD")

func TestStatProgram(t *testing.T) {
	for _, tc := range []struct {
		name  string
		files []string
		path  string
		exts  []string
		want  string // "" for not found
	}{
		{"relative, no extension", []string{"p/bin/herdr-nvim.exe"}, "p/bin/herdr-nvim", winExts, "p/bin/herdr-nvim.exe"},
		{"absolute alike", []string{"c/plug/bin/token-dashboard.exe"}, "c/plug/bin/token-dashboard", winExts, "c/plug/bin/token-dashboard.exe"},
		{"as written wins", []string{"p/bin/tool", "p/bin/tool.exe"}, "p/bin/tool", winExts, "p/bin/tool"},
		{"PATHEXT order", []string{"p/bin/tool.bat", "p/bin/tool.exe"}, "p/bin/tool", winExts, "p/bin/tool.exe"},
		{"PATHEXT order, changed", []string{"p/bin/tool.bat", "p/bin/tool.exe"}, "p/bin/tool", splitPathExt(".BAT;.EXE"), "p/bin/tool.bat"},
		{"has an extension already", []string{"p/bin/tool.exe.exe"}, "p/bin/tool.exe", winExts, ""},
		{"extension in another case", []string{"p/bin/tool.EXE.exe"}, "p/bin/tool.EXE", winExts, ""},
		{"a dot that is not an extension", []string{"p/bin/herdr-nvim-1.2.exe"}, "p/bin/herdr-nvim-1.2", winExts, "p/bin/herdr-nvim-1.2.exe"},
		{"folder skipped", []string{"p/bin/tool.com/", "p/bin/tool.exe"}, "p/bin/tool", winExts, "p/bin/tool.exe"},
		{"nothing there", []string{"p/bin/other.exe"}, "p/bin/tool", winExts, ""},
		{"not Windows", []string{"p/bin/tool.exe"}, "p/bin/tool", nil, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, info, err := statProgram(tc.path, tc.exts, fakeStat(tc.files...))
			if tc.want == "" {
				if err == nil {
					t.Fatalf("found %s, want not found", got)
				}
				if got != tc.path {
					t.Fatalf("an error names %s, want the path as written %s", got, tc.path)
				}
				return
			}
			if err != nil || got != tc.want || info == nil {
				t.Fatalf("got %q, %v, want %q", got, err, tc.want)
			}
		})
	}
}

func TestStatProgramKeepsOtherErrors(t *testing.T) {
	calls := 0
	stat := func(string) (fs.FileInfo, error) {
		calls++
		return nil, fs.ErrPermission
	}
	if _, _, err := statProgram("p/bin/tool", winExts, stat); err == nil || calls != 1 {
		t.Fatalf("err %v after %d calls, want the permission error and no guesses", err, calls)
	}
}

func TestSplitPathExt(t *testing.T) {
	if got := splitPathExt(""); !slices.Equal(got, []string{".com", ".exe", ".bat", ".cmd"}) {
		t.Fatalf("default = %v", got)
	}
	if got := splitPathExt(".EXE;;CMD;.Ps1"); !slices.Equal(got, []string{".exe", ".ps1"}) {
		t.Fatalf("got %v", got)
	}
}

// TestResolveProgramOnThisPlatform runs the real ResolveProgram on the real
// filesystem. On Windows it shows the lookup end to end; elsewhere it shows
// a name stays the file's name.
func TestResolveProgramOnThisPlatform(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "bin"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "bin", "herdr-nvim.exe"), []byte("x"), 0o700); err != nil {
		t.Fatal(err)
	}
	for _, prog := range []string{"bin/herdr-nvim", "./bin/herdr-nvim", filepath.Join(root, "bin", "herdr-nvim")} {
		got, perr := ResolveProgram(prog, root)
		if runtime.GOOS == "windows" {
			if perr != nil || !sameFile(got, filepath.Join(root, "bin", "herdr-nvim.exe")) {
				t.Errorf("%s: got %q, %v", prog, got, perr)
			}
			continue
		}
		if perr == nil || perr.Code != "plugin_command_not_found" {
			t.Errorf("%s: got %q, %v, want plugin_command_not_found", prog, got, perr)
		}
	}
}

func sameFile(a, b string) bool {
	sa, err1 := os.Stat(a)
	sb, err2 := os.Stat(b)
	return err1 == nil && err2 == nil && os.SameFile(sa, sb)
}
