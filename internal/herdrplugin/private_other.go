//go:build !unix

package herdrplugin

import "os"

func privateToMe(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
