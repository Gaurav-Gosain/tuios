package release

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// NewestInstalled is the tuios binary a restart should run, and the version
// it reports: the newest one installed, which is not always the code this
// process runs.
//
// There are two places to look, and either can be the new one.
//
//   - The path this process was started from. An update renames a new file
//     onto that name, and the running process keeps the old inode, so the
//     name holds the new build. /proc/self/exe is never used: on Linux it is
//     a link to the old inode, and after an update it reads "... (deleted)".
//   - The tuios on PATH. An immutable install (the Nix store, mise, asdf,
//     Linuxbrew) puts the new build at a new path and moves a link on PATH,
//     so the old path still holds the old build.
//
// When both are there and are different files, the one whose --version is
// newer wins. When the two cannot be compared, the started-from path wins.
func NewestInstalled() (path, version string, err error) {
	var paths []string
	if exe, exeErr := os.Executable(); exeErr == nil {
		exe = strings.TrimSuffix(exe, " (deleted)")
		if fi, statErr := os.Stat(exe); statErr == nil && fi.Mode().IsRegular() {
			paths = append(paths, exe)
		}
		err = exeErr
	}
	if p, lookErr := exec.LookPath(ExecutableName("tuios")); lookErr == nil {
		if abs, absErr := filepath.Abs(p); absErr == nil {
			p = abs
		}
		if len(paths) == 0 || !sameFile(paths[0], p) {
			paths = append(paths, p)
		}
	}
	if len(paths) == 0 {
		if err == nil {
			err = errors.New("no tuios binary was found where this one started or on PATH")
		}
		return "", "", err
	}
	path, version = paths[0], BinaryVersion(paths[0])
	for _, p := range paths[1:] {
		v := BinaryVersion(p)
		if newer, ok := Newer(version, v); ok && newer {
			path, version = p, v
		}
	}
	return path, version, nil
}

// sameFile reports whether two paths name one file, through links.
func sameFile(a, b string) bool {
	fa, errA := os.Stat(a)
	fb, errB := os.Stat(b)
	return errA == nil && errB == nil && os.SameFile(fa, fb)
}

// BinaryVersion is the version a tuios binary reports with --version, or ""
// when it cannot be run or read. The first line reads
// "tuios version 0.8.0 [pure-Go backend]".
func BinaryVersion(bin string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, bin, "--version").Output()
	if err != nil {
		return ""
	}
	line, _, _ := strings.Cut(string(out), "\n")
	fields := strings.Fields(line)
	if len(fields) >= 3 && fields[1] == "version" {
		return fields[2]
	}
	return ""
}
