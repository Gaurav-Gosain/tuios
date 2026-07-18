//go:build unix

package vt

import (
	"fmt"
	"os"
	"syscall"
)

// maxShmSize caps a single shared-memory transmission so a hostile guest
// cannot force an enormous mmap. 512 MiB is far above any real image frame.
const maxShmSize = 512 * 1024 * 1024

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

	fi, err := f.Stat()
	if err != nil {
		return nil, fmt.Errorf("stat shm: %w", err)
	}
	fileSize := fi.Size()

	if size <= 0 {
		size = int(fileSize)
	}

	// clamp: mmap beyond EOF raises SIGBUS which recover cannot catch, so the
	// guest-supplied S= must never exceed the backing file.
	if fileSize < int64(size) {
		size = int(fileSize)
	}

	if size <= 0 {
		return nil, fmt.Errorf("invalid shm size")
	}
	if size > maxShmSize {
		return nil, fmt.Errorf("shm size %d exceeds maximum %d", size, maxShmSize)
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
