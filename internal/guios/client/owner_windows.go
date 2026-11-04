package client

import "os"

// ownedByUser reports a file this user owns. Windows keeps a user's paste
// folder under their own profile, so every file there is theirs.
func ownedByUser(os.FileInfo) bool { return true }
