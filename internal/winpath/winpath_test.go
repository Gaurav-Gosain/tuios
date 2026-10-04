package winpath

import (
	"strings"
	"testing"
)

// The conversion is pure, so it is tested here and on every platform. The
// e2e suite does not run on Windows, where it matters.
//
// The ways it could fail, written down before the code:
//   - A drive path keeps its POSIX form: /c/Users reads as \c\Users, which is
//     no folder on Windows (issue #413).
//   - The drive letter keeps its case, or a lone /c loses the root backslash
//     and becomes "C:", which Windows reads as the current folder on drive C.
//   - A top folder whose name is one letter and more (/cd, /c1) is read as a
//     drive.
//   - Cygwin's /cygdrive/c prefix is not taken off, or /cygdrive alone is
//     mapped to a folder that does not exist.
//   - A path that is already a Windows path (file:///C:/Users gives /C:/Users)
//     is put under the MSYS2 root.
//   - A path under the MSYS2 root (/home/x, /usr/bin, /) is not joined to the
//     root, or is joined with a doubled or missing separator.
//   - /tmp, which MSYS2 mounts on the user's temp folder, is put under the
//     root instead.
//   - With no root known, a root-relative path is made up rather than refused.
//   - .. climbs above the root. It may leave a drive for the root, as it
//     does in cygpath.
//   - A UNC path (//server/share) loses one of its leading slashes.
//   - A relative or empty path is accepted.
func TestFromPOSIX(t *testing.T) {
	msys := Mounts{Root: `C:\msys64`, Tmp: `C:\Users\x\AppData\Local\Temp`}
	noRoot := Mounts{}
	rootSlash := Mounts{Root: `D:\tools\msys64\`}

	cases := []struct {
		name string
		in   string
		m    Mounts
		want string
		ok   bool
	}{
		{"the report in the issue", "/c/work/workspace/ai", msys, `C:\work\workspace\ai`, true},
		{"drive root", "/c", msys, `C:\`, true},
		{"drive root with a slash", "/c/", msys, `C:\`, true},
		{"upper case drive", "/D/data", msys, `D:\data`, true},
		{"drive with no root known", "/c/Users/x", noRoot, `C:\Users\x`, true},
		{"two letter top folder", "/cd/x", msys, `C:\msys64\cd\x`, true},
		{"letter and digit top folder", "/c1", msys, `C:\msys64\c1`, true},
		{"cygdrive", "/cygdrive/e/src", msys, `E:\src`, true},
		{"cygdrive drive root", "/cygdrive/e", msys, `E:\`, true},
		{"cygdrive alone", "/cygdrive", msys, "", false},
		{"cygdrive with no drive", "/cygdrive/ee", msys, "", false},
		{"windows path from a file URL", "/C:/Users/x", msys, `C:\Users\x`, true},
		{"windows drive root from a file URL", "/C:/", msys, `C:\`, true},
		{"windows path already", `C:\Users\x`, msys, `C:\Users\x`, true},
		{"windows path with slashes", "c:/Users/x", msys, `C:\Users\x`, true},
		{"home under the root", "/home/x", msys, `C:\msys64\home\x`, true},
		{"usr under the root", "/usr/bin", msys, `C:\msys64\usr\bin`, true},
		{"the root itself", "/", msys, `C:\msys64`, true},
		{"root given with a trailing separator", "/home", rootSlash, `D:\tools\msys64\home`, true},
		{"tmp", "/tmp", msys, `C:\Users\x\AppData\Local\Temp`, true},
		{"under tmp", "/tmp/build", msys, `C:\Users\x\AppData\Local\Temp\build`, true},
		{"tmp with no temp mount", "/tmp/build", Mounts{Root: `C:\cygwin64`}, `C:\cygwin64\tmp\build`, true},
		{"a folder that starts with tmp", "/tmpx", msys, `C:\msys64\tmpx`, true},
		{"root path with no root known", "/home/x", noRoot, "", false},
		{"dot dot inside a drive", "/c/a/../b", msys, `C:\b`, true},
		{"dot dot out of a drive", "/c/../home", msys, `C:\msys64\home`, true},
		{"dot dot above the root", "/c/../../..", msys, `C:\msys64`, true},
		{"doubled slashes", "/c//Users///x/", msys, `C:\Users\x`, true},
		{"unc", "//server/share/dir", msys, `\\server\share\dir`, true},
		{"unc share root", "//server/share", msys, `\\server\share`, true},
		{"unc with no share", "//server", msys, "", false},
		{"relative", "work/ai", msys, "", false},
		{"empty", "", msys, "", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ok := FromPOSIX(c.in, c.m)
			if got != c.want || ok != c.ok {
				t.Errorf("FromPOSIX(%q) = %q, %v; want %q, %v", c.in, got, ok, c.want, c.ok)
			}
		})
	}
}

// The ways the root search could fail:
//   - A PATH entry for usr\bin is taken as the root without the runtime DLL
//     that proves it is an MSYS2 tree (some other tool's usr\bin).
//   - The root keeps the \usr\bin or \bin tail, or the case of the match
//     changes the root's spelling.
//   - Cygwin, whose runtime sits in bin and not usr\bin, is missed.
//   - EXEPATH, the Git for Windows root, is ignored when PATH does not help.
//   - /tmp is mapped for Cygwin, whose /tmp is a real folder under the root.
func TestFindMounts(t *testing.T) {
	files := map[string]bool{
		`c:\msys64\usr\bin\msys-2.0.dll`:            true,
		`c:\cygwin64\bin\cygwin1.dll`:               true,
		`c:\program files\git\usr\bin\msys-2.0.dll`: true,
	}
	exists := func(p string) bool { return files[strings.ToLower(p)] }
	const tmp = `C:\Users\x\AppData\Local\Temp`

	cases := []struct {
		name, path, exepath string
		want                Mounts
	}{
		{"msys2 on PATH", `C:\Windows\system32;C:\msys64\ucrt64\bin;C:\msys64\usr\bin`, "", Mounts{Root: `C:\msys64`, Tmp: tmp}},
		{"msys2 with a trailing separator and odd case", `C:\MSYS64\USR\BIN\`, "", Mounts{Root: `C:\MSYS64`, Tmp: tmp}},
		{"msys2 with forward slashes", `C:/msys64/usr/bin`, "", Mounts{Root: `C:\msys64`, Tmp: tmp}},
		{"usr bin with no runtime", `C:\other\usr\bin`, "", Mounts{}},
		{"cygwin", `C:\cygwin64\bin;C:\Windows`, "", Mounts{Root: `C:\cygwin64`}},
		{"git for windows by EXEPATH", `C:\Windows`, `C:\Program Files\Git`, Mounts{Root: `C:\Program Files\Git`, Tmp: tmp}},
		{"nothing", `C:\Windows;;`, "", Mounts{}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := FindMounts(c.path, c.exepath, tmp, exists)
			if got != c.want {
				t.Errorf("FindMounts = %+v; want %+v", got, c.want)
			}
		})
	}
}
