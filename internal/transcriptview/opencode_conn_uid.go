//go:build linux || darwin

package transcriptview

import "os"

var myUID = os.Getuid
