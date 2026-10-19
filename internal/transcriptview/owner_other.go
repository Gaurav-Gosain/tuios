//go:build !unix && !windows

package transcriptview

import "os"

// serviceFileSafe cannot tell who owns a file here, so the service is never
// asked.
func serviceFileSafe(*os.File, os.FileInfo) bool { return false }
