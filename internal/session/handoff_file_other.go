//go:build !linux && !windows && !js

package session

import (
	"os"
	"path/filepath"
	"strconv"
)

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
