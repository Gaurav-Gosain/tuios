package session

import "os"

// previewFileOwner has nothing to say on Windows.
func previewFileOwner(os.FileInfo) (owner, group string) { return "", "" }
