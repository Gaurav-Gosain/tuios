//go:build !unix

package app

import "os"

// redirectStderr leaves stderr alone where descriptor 2 cannot be replaced.
func redirectStderr(f *os.File) { _ = f.Close() }
