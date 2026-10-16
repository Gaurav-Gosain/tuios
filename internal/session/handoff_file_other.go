//go:build !linux && !windows && !js

package session

import (
	"os"
	"path/filepath"
	"strconv"
)

// newHandoffFile makes the descriptor the manifest is written to: a file only
// the owner can read, under dir, unlinked as soon as it is open, so only the
// descriptor names it. It lives in the page cache, and the exec keeps the
// descriptor and its offset.
func newHandoffFile(dir string) (*os.File, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	path := filepath.Join(dir, "manifest-"+strconv.Itoa(os.Getpid()))
	_ = os.Remove(path)
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return nil, err
	}
	if err := os.Remove(path); err != nil {
		_ = f.Close()
		return nil, err
	}
	return f, nil
}
