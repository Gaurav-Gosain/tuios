//go:build !darwin

package tuie2e

import "os"

// removeShmObject deletes a shared memory object a host stand-in never read.
func removeShmObject(name string) error { return os.Remove("/dev/shm/" + name) }
