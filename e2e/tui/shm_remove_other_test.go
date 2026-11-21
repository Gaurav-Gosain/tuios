//go:build !darwin

package tuie2e

import "os"

func removeShmObject(name string) error { return os.Remove("/dev/shm/" + name) }
