package session

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
)

// The daemon's half of the rail's file section.
//
// The section used to read the filesystem in the client. That was the same
// machine as the pane for as long as a pane could only be on this machine, and
// federation ended it: a client attached to a session on another host listed its
// own disk and reported that the pane's directory did not exist. The daemon that
// owns the pane owns the disk the pane is on, so the listing is asked for here.
//
// The spoof verdict comes back with it for the same reason, and it is the more
// important half. It is a comparison between what a pane announced and where its
// shell actually is, and only this side holds both: the announcement arrived in
// the window state, and the shell is this daemon's own child. A client that
// tried to compute it for a pane on another machine would be reading a pid that
// means nothing where it is running.

// dirListingMax bounds one listing when the caller names no bound of its own.
const dirListingMax = 2000

// handleReadDir lists a directory on this machine and says whether the pane that
// named it is actually in it.
func (d *Daemon) handleReadDir(cs *connState, msg *Message) error {
	var payload ReadDirPayload
	if err := msg.ParsePayloadWithCodec(&payload, cs.codec); err != nil {
		return d.sendError(cs, ErrCodeInvalidMessage, "invalid read-dir payload")
	}
	dir := filepath.Clean(payload.Dir)
	if dir == "" || dir == "." {
		return d.sendMessage(cs, MsgDirListing, &DirListingPayload{
			Dir: payload.Dir, Err: "There is no folder to show.",
		})
	}
	out := listDir(dir, payload.Max)
	// Asked whether or not the directory could be read, because the two answers
	// are independent and this is the one that changes what a person should do
	// next: a pane pointing somewhere it is not is worth saying even when the
	// folder it named cannot be listed.
	out.Spoofed = d.paneIsSpoofed(cs.sessionID, payload.WindowID, dir)
	return d.sendMessage(cs, MsgDirListing, out)
}

// listDir reads one directory into the answer the client draws. Separate from
// the handler so it can be tested without a daemon, a session or a socket.
func listDir(dir string, max int) *DirListingPayload {
	limit := max
	if limit <= 0 || limit > dirListingMax {
		limit = dirListingMax
	}
	out := &DirListingPayload{Dir: dir}

	entries, capped, err := readDirCapped(dir, limit)
	if err != nil {
		out.Err = dirReadError(err)
		return out
	}
	out.Capped = capped

	// Directories first, then names, case insensitively. Sorted here rather than
	// in the client because the cap above is applied in whatever order the
	// filesystem hands names back, so a client sorting a capped listing would be
	// sorting an arbitrary subset and calling it the first two thousand.
	sort.Slice(entries, func(i, j int) bool {
		ei, ej := entries[i], entries[j]
		if ei.IsDir() != ej.IsDir() {
			return ei.IsDir()
		}
		return lowerName(ei.Name()) < lowerName(ej.Name())
	})
	out.Entries = make([]DirEntry, 0, len(entries))
	for _, e := range entries {
		out.Entries = append(out.Entries, DirEntry{Name: e.Name(), IsDir: e.IsDir()})
	}
	return out
}

// paneIsSpoofed reports whether the pane announced dir while its shell is
// somewhere else.
//
// Only a positive disagreement counts. A pane with no window, a window with no
// live PTY, a platform that cannot report a process directory: all of those are
// no evidence, and treating them as a disagreement would take the file actions
// away from people who did nothing wrong. Unknown is unknown.
func (d *Daemon) paneIsSpoofed(sessionID, windowID, dir string) bool {
	if sessionID == "" || windowID == "" {
		return false
	}
	sess := d.manager.GetSessionByID(sessionID)
	if sess == nil {
		return false
	}
	state := sess.GetState()
	ptyID := ""
	for i := range state.Windows {
		if state.Windows[i].ID == windowID {
			ptyID = state.Windows[i].PTYID
			break
		}
	}
	if ptyID == "" {
		return false
	}
	pty := sess.GetPTY(ptyID)
	if pty == nil {
		return false
	}
	procDir, ok := pty.ProcessCwd()
	if !ok {
		return false
	}
	return !sameDirOnDisk(procDir, dir)
}

// sameDirOnDisk answers "the same directory" rather than "the same spelling".
//
// The kernel hands back a path it has already resolved; a shell prints $PWD,
// which keeps whatever symlink the user walked in through. Comparing the two as
// strings calls every such pane a liar, so a disagreement is confirmed by
// identity on disk, which takes symlinks and bind mounts with it.
func sameDirOnDisk(a, b string) bool {
	if filepath.Clean(a) == filepath.Clean(b) {
		return true
	}
	ai, err := os.Stat(a)
	if err != nil {
		return false
	}
	bi, err := os.Stat(b)
	if err != nil {
		return false
	}
	return os.SameFile(ai, bi)
}

// readDirCapped reads at most limit names and says whether more were left.
func readDirCapped(dir string, limit int) (entries []os.DirEntry, capped bool, err error) {
	f, err := os.Open(dir)
	if err != nil {
		return nil, false, err
	}
	defer func() { _ = f.Close() }()

	// io.EOF is how a directory with fewer than limit names left in it reports
	// that it is finished, so it is the expected end and not a failure.
	entries, err = f.ReadDir(limit)
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, false, err
	}
	if len(entries) < limit {
		return entries, false, nil
	}
	more, err := f.ReadDir(1)
	if err != nil && !errors.Is(err, io.EOF) {
		// The batch above is good and the only thing this second read decides is
		// a note on one row, so a failure here loses the note rather than the
		// listing.
		return entries, false, nil
	}
	return entries, len(more) > 0, nil
}

// dirReadError turns a filesystem error into the sentence a rail row shows.
// The row is about twenty four cells wide, so these are short on purpose, and
// they say what is true rather than naming the syscall.
func dirReadError(err error) string {
	switch {
	case errors.Is(err, os.ErrNotExist):
		return "That folder is gone."
	case errors.Is(err, os.ErrPermission):
		return "No permission to read it."
	default:
		return "That folder could not be read."
	}
}

// lowerName lowercases ASCII for the sort, which is all the ordering needs and
// avoids a locale the two ends might not share.
func lowerName(s string) string {
	b := []byte(s)
	for i := range b {
		if b[i] >= 'A' && b[i] <= 'Z' {
			b[i] += 'a' - 'A'
		}
	}
	return string(b)
}
