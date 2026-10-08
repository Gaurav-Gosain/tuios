package herdrplugin

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// defaultPathExt is what Windows and Go use when PATHEXT is not set.
const defaultPathExt = ".com;.exe;.bat;.cmd"

// pathExts is the extensions Windows adds to a program named without one,
// in the order PATHEXT gives them. It is empty elsewhere, where a program's
// name is its file's name.
func pathExts() []string {
	if runtime.GOOS != "windows" {
		return nil
	}
	return splitPathExt(os.Getenv("PATHEXT"))
}

// splitPathExt parses PATHEXT as Go's os/exec does: entries separated by
// semicolons, each lowercased, an entry without a leading dot dropped.
func splitPathExt(v string) []string {
	if v == "" {
		v = defaultPathExt
	}
	var exts []string
	for e := range strings.SplitSeq(strings.ToLower(v), ";") {
		if e == "" || e[0] != '.' {
			continue
		}
		exts = append(exts, e)
	}
	return exts
}

// statProgram finds the file a program path names. The path as written
// comes first. When it does not exist, and its extension is not one of
// exts, each of exts is added in turn, as Windows does for bin/herdr-nvim
// when the file is bin/herdr-nvim.exe. The first that exists is the program.
// The error is the one for the path as written.
func statProgram(path string, exts []string, stat func(string) (fs.FileInfo, error)) (string, fs.FileInfo, error) {
	info, err := stat(path)
	if err == nil || !errors.Is(err, fs.ErrNotExist) || len(exts) == 0 {
		return path, info, err
	}
	if ext := strings.ToLower(filepath.Ext(path)); ext != "" {
		for _, e := range exts {
			if e == ext {
				return path, info, err
			}
		}
	}
	for _, e := range exts {
		if i, serr := stat(path + e); serr == nil && !i.IsDir() {
			return path + e, i, nil
		}
	}
	return path, info, err
}
