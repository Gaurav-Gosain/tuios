//go:build windows

package vt

import "fmt"

func loadSharedMemory(_ string, _ int) ([]byte, error) {
	return nil, fmt.Errorf("shared memory not supported on Windows")
}
