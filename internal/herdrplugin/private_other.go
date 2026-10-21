//go:build !unix

package herdrplugin

import "os"

// privateToMe reports whether path exists. Windows keeps a user's state
// folder private by its ACL, and the runner does not start a native
// plugin's commands there.
func privateToMe(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
