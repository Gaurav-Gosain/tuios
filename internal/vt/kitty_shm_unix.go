//go:build unix

package vt

import (
	"fmt"
	"os"
	"syscall"
)

func loadSharedMemory(name string, size int) ([]byte, error) {
	if name == "" {
		return nil, fmt.Errorf("empty shared memory name")
	}

	shmPath := "/dev/shm/" + name

	f, err := os.Open(shmPath)
	if err != nil {
		return nil, fmt.Errorf("open shm: %w", err)
	}
	defer func() { _ = f.Close() }()

	if size <= 0 {
		fi, err := f.Stat()
		if err != nil {
			return nil, fmt.Errorf("stat shm: %w", err)
		}
		size = int(fi.Size())
	}

	if size <= 0 {
		return nil, fmt.Errorf("invalid shm size")
	}

	data, err := syscall.Mmap(int(f.Fd()), 0, size, syscall.PROT_READ, syscall.MAP_SHARED)
	if err != nil {
		return nil, fmt.Errorf("mmap shm: %w", err)
	}

	result := make([]byte, len(data))
	copy(result, data)

	if err := syscall.Munmap(data); err != nil {
		return result, nil
	}

	return result, nil
}
