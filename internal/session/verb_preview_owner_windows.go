package session

import "os"

func previewFileOwner(os.FileInfo) (owner, group string) { return "", "" }
