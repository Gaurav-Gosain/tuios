//go:build !unix && !windows

package transcriptview

import "os"

func serviceFileSafe(*os.File, os.FileInfo) bool { return false }
