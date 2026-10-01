// Package dirwatch tells a caller when the names in one directory change.
//
// It exists for the rail's files section, which lists one folder and has to
// notice a file added, removed or renamed in it by anything: the pane's shell,
// another program, another machine writing to a shared disk. Polling the
// folder would put filesystem work on a client that is doing nothing, which
// the render loop is built to avoid (see docs/perf.md), so the kernel is asked
// to say when the folder changes and the watcher sleeps until it does.
//
// It watches the directory and nothing under it, and only for changes to its
// entries. A write into a file in the folder is not a change to the folder's
// listing, and a log being appended to beside the shell must not wake the
// client on every line. That is why this is not fsnotify on Linux and the
// BSDs: fsnotify's inotify mask includes writes, and its kqueue backend opens
// a descriptor for every file in the folder, which for a folder of two
// thousand names is two thousand descriptors to answer one question.
package dirwatch

import "errors"

// ErrUnsupported is returned by Watch on a platform with no way to watch a
// directory. The caller keeps a listing that refreshes only when asked.
var ErrUnsupported = errors.New("dirwatch: not supported on this platform")
