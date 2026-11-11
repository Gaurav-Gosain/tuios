//go:build !unix

package procinfo

import "os"

// FileOwner has no answer here, where owners are ACLs and not user ids.
func FileOwner(os.FileInfo) (int, bool) { return 0, false }
