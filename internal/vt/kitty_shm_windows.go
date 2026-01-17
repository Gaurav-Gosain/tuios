//go:build windows

package vt

import "fmt"

func loadSharedMemory(name string, size int) ([]byte, error) {
	return nil, fmt.Errorf("shared memory not supported on Windows")
}
