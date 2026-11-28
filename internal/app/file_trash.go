package app

import (
	"errors"
	"strings"
	"syscall"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/filetrash"
)

// The trash is internal/filetrash. These are the rail's and the Files
// view's words for it.

// trashAvailable reports whether this system has a trash tuios can put a file
// in.
func trashAvailable() bool { return filetrash.Available() }

// trashPaths moves every path to the trash and reports how many went.
func trashPaths(paths []string, now time.Time) (int, error) { return filetrash.Put(paths, now) }

// trashPathsUndoable is trashPaths that answers where each path went.
func trashPathsUndoable(paths []string, now time.Time) ([]filetrash.Item, error) {
	return filetrash.PutUndoable(paths, now)
}

// trashError turns a trash failure into one sentence, with the cross-disk case
// naming the way round it.
func trashError(err error) string {
	if errors.Is(err, syscall.EXDEV) {
		return "That file is on another disk. tuios can not trash it. Use permanent delete."
	}
	if errors.Is(err, filetrash.ErrNoTrash) || err != nil && strings.Contains(err.Error(), "no trash on this system") {
		return "This system has no trash. Use permanent delete."
	}
	return fileOpError(err)
}
