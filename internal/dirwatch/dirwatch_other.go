//go:build !linux && !darwin && !freebsd && !netbsd && !openbsd && !dragonfly && !windows

package dirwatch

// Watcher is never made on this platform.
type Watcher struct{}

// Watch reports ErrUnsupported.
func Watch(string, func()) (*Watcher, error) { return nil, ErrUnsupported }

// Close does nothing.
func (*Watcher) Close() {}
