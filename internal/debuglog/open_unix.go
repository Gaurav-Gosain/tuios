//go:build unix

package debuglog

import (
	"os"
	"syscall"

	"github.com/Gaurav-Gosain/tuios/internal/errfmt"
)

func openPrivate(path string) (*os.File, error) {
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY|syscall.O_NOFOLLOW, 0o600)
	if err != nil {
		return nil, err
	}
	st, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, err
	}
	if sys, ok := st.Sys().(*syscall.Stat_t); ok && int(sys.Uid) != os.Getuid() {
		_ = f.Close()
		return nil, errfmt.Errorf("%s belongs to another user", path)
	}
	if !st.Mode().IsRegular() {
		_ = f.Close()
		return nil, errfmt.Errorf("%s is not a regular file", path)
	}
	if st.Mode().Perm()&0o077 != 0 {
		if err := f.Chmod(0o600); err != nil {
			_ = f.Close()
			return nil, err
		}
	}
	return f, nil
}
