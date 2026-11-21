//go:build !unix

package app

import "os"

func redirectStderr(f *os.File) { _ = f.Close() }
