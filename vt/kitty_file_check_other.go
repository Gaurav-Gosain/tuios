//go:build !unix

package vt

import "os"

// kittyOpenRegular opens path and asks the open file whether it is a regular
// file. See the unix version for the flags this platform does not have.
func kittyOpenRegular(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return errKittyNotRegular
	}
	return nil
}
