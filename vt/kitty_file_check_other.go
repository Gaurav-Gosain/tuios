//go:build !unix

package vt

import "os"

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
