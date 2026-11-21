package app

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/errfmt"
	"github.com/adrg/xdg"
)

var trashDirFunc = homeTrashDir

func homeTrashDir() (string, error) {
	if !trashAvailable() {
		return "", errors.New("no trash on this system")
	}
	if xdg.DataHome == "" {
		return "", errors.New("no data directory")
	}
	return filepath.Join(xdg.DataHome, "Trash"), nil
}

func trashAvailable() bool { return runtime.GOOS != "windows" }

func trashPaths(paths []string, now time.Time) (int, error) {
	dir, err := trashDirFunc()
	if err != nil {
		return 0, err
	}
	files := filepath.Join(dir, "files")
	info := filepath.Join(dir, "info")
	if err := os.MkdirAll(files, 0o700); err != nil {
		return 0, err
	}
	if err := os.MkdirAll(info, 0o700); err != nil {
		return 0, err
	}

	ok := 0
	var firstErr error
	for _, p := range paths {
		if err := trashOne(files, info, p, now); err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		ok++
	}
	return ok, firstErr
}

func trashOne(filesDir, infoDir, path string, now time.Time) error {
	abs, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	if _, err := os.Lstat(abs); err != nil {
		return err
	}
	base := filepath.Base(abs)
	if base == "" || base == "." || base == string(os.PathSeparator) {
		return errfmt.Errorf("cannot trash %s", path)
	}

	name, infoFile, err := claimTrashName(infoDir, base)
	if err != nil {
		return err
	}
	body := "[Trash Info]\nPath=" + trashInfoPath(abs) +
		"\nDeletionDate=" + now.Format("2006-01-02T15:04:05") + "\n"
	if _, err := infoFile.WriteString(body); err != nil {
		_ = infoFile.Close()
		_ = os.Remove(filepath.Join(infoDir, name+".trashinfo"))
		return err
	}
	if err := infoFile.Close(); err != nil {
		_ = os.Remove(filepath.Join(infoDir, name+".trashinfo"))
		return err
	}

	if err := os.Rename(abs, filepath.Join(filesDir, name)); err != nil {
		_ = os.Remove(filepath.Join(infoDir, name+".trashinfo"))
		return err
	}
	return nil
}

func claimTrashName(infoDir, base string) (string, *os.File, error) {
	ext := filepath.Ext(base)
	stem := base[:len(base)-len(ext)]
	for i := range 1000 {
		name := base
		if i > 0 {
			name = fmt.Sprintf("%s.%d%s", stem, i, ext)
		}
		f, err := os.OpenFile(filepath.Join(infoDir, name+".trashinfo"),
			os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err == nil {
			return name, f, nil
		}
		if !os.IsExist(err) {
			return "", nil, err
		}
	}
	return "", nil, errfmt.Errorf("%s: the trash already holds 1000 of these", base)
}

func trashInfoPath(p string) string {
	var b strings.Builder
	for i := 0; i < len(p); i++ {
		c := p[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
			b.WriteByte(c)
		case c == '-' || c == '_' || c == '.' || c == '~' || c == '/':
			b.WriteByte(c)
		default:
			fmt.Fprintf(&b, "%%%02X", c)
		}
	}
	return b.String()
}

func trashError(err error) string {
	if errors.Is(err, syscall.EXDEV) {
		return "That file is on another disk. tuios can not trash it. Use permanent delete."
	}
	if err != nil && strings.Contains(err.Error(), "no trash on this system") {
		return "This system has no trash. Use permanent delete."
	}
	return fileOpError(err)
}
