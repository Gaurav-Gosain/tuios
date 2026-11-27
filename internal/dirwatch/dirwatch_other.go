//go:build !linux && !darwin && !freebsd && !netbsd && !openbsd && !dragonfly && !windows

package dirwatch

// Watcher is never made on this platform.
type Watcher struct{}

// Watch reports ErrUnsupported.
func Watch(string, func()) (*Watcher, error) { return nil, ErrUnsupported }

// Close does nothing.
func (*Watcher) Close() {}

// fileByDir is true: there is no watch here at all.
const fileByDir = true

func watchContentDir(string, func()) (*Watcher, error) { return nil, ErrUnsupported }

func watchFile(string, func()) (*Watcher, error) { return nil, ErrUnsupported }

// WatchFile reports ErrUnsupported.
func WatchFile(string, func()) (*Watcher, error) { return nil, ErrUnsupported }
