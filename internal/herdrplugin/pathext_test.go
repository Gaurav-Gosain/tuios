package herdrplugin

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// The ways the PATHEXT lookup can fail, which this test holds it to:
//
//   - bin/herdr-nvim is not found when the file is bin/herdr-nvim.exe;
//   - ./bin/herdr-nvim or an absolute path is not given the same lookup;
//   - the extensions are tried in another order than PATHEXT's, so a .bat
//     shadows the .exe herdr would run;
//   - a name with a dot that is not an extension (herdr-nvim-1.2) is not
//     tried with one;
//   - the lookup runs on Linux or macOS, where a name is the file's name.
func TestResolveProgramOnThisPlatform(t *testing.T) {
	root := t.TempDir()
	write := func(name string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(root, "bin", name), []byte("x"), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(filepath.Join(root, "bin"), 0o700); err != nil {
		t.Fatal(err)
	}
	write("herdr-nvim.exe")
	write("tool.exe")
	write("tool.bat")
	write("herdr-nvim-1.2.exe")
	if runtime.GOOS == "windows" {
		t.Setenv("PATHEXT", ".COM;.EXE;.BAT;.CMD")
	}

	for _, tc := range []struct{ prog, want string }{
		{"bin/herdr-nvim", "herdr-nvim.exe"},
		{"./bin/herdr-nvim", "herdr-nvim.exe"},
		{filepath.Join(root, "bin", "herdr-nvim"), "herdr-nvim.exe"},
		{"bin/tool", "tool.exe"},
		{"bin/herdr-nvim-1.2", "herdr-nvim-1.2.exe"},
	} {
		got, perr := ResolveProgram(tc.prog, root)
		if runtime.GOOS == "windows" {
			if perr != nil || !sameFile(got, filepath.Join(root, "bin", tc.want)) {
				t.Errorf("%s: got %q, %v, want %s", tc.prog, got, perr, tc.want)
			}
			continue
		}
		if perr == nil || perr.Code != "plugin_command_not_found" {
			t.Errorf("%s: got %q, %v, want plugin_command_not_found", tc.prog, got, perr)
		}
	}
}

func sameFile(a, b string) bool {
	sa, err1 := os.Stat(a)
	sb, err2 := os.Stat(b)
	return err1 == nil && err2 == nil && os.SameFile(sa, sb)
}
