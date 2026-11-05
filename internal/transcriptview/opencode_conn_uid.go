//go:build linux || darwin

package transcriptview

import "os"

// myUID is the user a connection's server side must belong to. A test
// changes it to see the check refuse. Only linux and darwin read a
// socket's owner, so only they have it.
var myUID = os.Getuid
