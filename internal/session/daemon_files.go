package session

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"

	"github.com/Gaurav-Gosain/tuios/internal/pathroot"

	"github.com/Gaurav-Gosain/tuios/internal/jsonx"
)

const dirListingMax = 2000

func (d *Daemon) handleReadDir(cs *connState, msg *Message) error {
	var payload ReadDirPayload
	if err := msg.ParsePayload(&payload); err != nil {
		return d.replyError(cs, msg, ErrCodeInvalidMessage, "invalid read-dir payload")
	}
	dir := filepath.Clean(payload.Dir)
	if dir == "" || dir == "." {
		return d.reply(cs, msg, MsgDirListing, &DirListingPayload{
			Dir: payload.Dir, Err: "There is no folder to show.",
		})
	}
	if host := d.windowHost(cs.sessionID, payload.WindowID); host != "" {
		return d.reply(cs, msg, MsgDirListing, d.remoteListing(host, dir, payload.Max))
	}
	dir = pathroot.ExpandHome(dir)

	if verr := d.checkLinkRead(cs, dir); verr != nil {
		return d.reply(cs, msg, MsgDirListing, &DirListingPayload{Dir: payload.Dir, Err: verr.Message})
	}
	out := listDir(dir, payload.Max)
	if spoofCheckWanted(payload) {
		out.Spoofed = d.paneIsSpoofed(cs.sessionID, payload.WindowID, dir)
	}
	return d.reply(cs, msg, MsgDirListing, out)
}

func spoofCheckWanted(p ReadDirPayload) bool {
	return !p.Pinned && p.WindowID != ""
}

func (d *Daemon) windowHost(sessionID, windowID string) string {
	if sessionID == "" || windowID == "" {
		return ""
	}
	sess := d.manager.GetSessionByID(sessionID)
	if sess == nil {
		return ""
	}
	state := sess.GetState()
	for i := range state.Windows {
		if state.Windows[i].ID == windowID {
			return state.Windows[i].Host
		}
	}
	return ""
}

func listDir(dir string, max int) *DirListingPayload {
	limit := max
	if limit <= 0 || limit > dirListingMax {
		limit = dirListingMax
	}
	out := &DirListingPayload{Dir: dir}

	entries, capped, err := ReadDirCapped(dir, limit)
	if err != nil {
		out.Err = DirReadError(err)
		return out
	}
	out.Capped = capped

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

// ReadDirCapped reads at most limit names from dir and says whether there were
// more. The client's own listing of a local pane's directory reads through it
// too, so the two ends cap and fail the same way.
//
// os.ReadDir is the obvious call and the wrong one here: it reads the whole
// directory and sorts it before returning, so its cost is the directory's size
// and there is no point at which a caller can stop it. This opens the directory
// and takes one bounded batch instead, then asks for one more name only to find
// out whether the listing is complete.
//
// The batch arrives in whatever order the filesystem hands it over, unlike
// os.ReadDir's sorted result. The caller sorts it either way, so the only thing
// that changes is which names a capped listing holds, and on a directory that
// large there is no ordering that makes the cut the right one.
func ReadDirCapped(dir string, limit int) (entries []os.DirEntry, capped bool, err error) {
	f, err := os.Open(dir)
	if err != nil {
		return nil, false, err
	}
	defer func() { _ = f.Close() }()

	entries, err = f.ReadDir(limit)
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, false, err
	}
	if len(entries) < limit {
		return entries, false, nil
	}
	more, err := f.ReadDir(1)
	if err != nil && !errors.Is(err, io.EOF) {
		return entries, false, nil
	}
	return entries, len(more) > 0, nil
}

// DirReadError turns a filesystem error into the sentence a rail row shows.
// The row is about twenty four cells wide, so these are short on purpose, and
// they say what is true rather than naming the syscall. The wrapped error
// carries the whole path, which is already on the header row above it and
// does not fit twice.
func DirReadError(err error) string {
	switch {
	case errors.Is(err, os.ErrNotExist):
		return "That folder is gone."
	case errors.Is(err, os.ErrPermission):
		return "No permission to read it."
	default:
		return "That folder could not be read."
	}
}

func lowerName(s string) string {
	b := []byte(s)
	for i := range b {
		if b[i] >= 'A' && b[i] <= 'Z' {
			b[i] += 'a' - 'A'
		}
	}
	return string(b)
}

func (d *Daemon) remoteListing(host, dir string, maxEntries int) *DirListingPayload {
	if d.federation == nil {
		return &DirListingPayload{Dir: dir, Err: "No link to " + host + "."}
	}
	ctx, cancel := context.WithTimeout(d.ctx, federationVerbBudget)
	defer cancel()

	raw, err := d.federation.Call(ctx, host, "read-dir", map[string]any{
		"dir": dir,
		"max": maxEntries,
	})
	if err != nil {
		message, _ := federationErrorText(err)
		if message == "" {
			message = host + " could not list it."
		}
		return &DirListingPayload{Dir: dir, Err: message}
	}
	var out DirListingPayload
	if jsonx.Unmarshal(raw, &out) != nil {
		return &DirListingPayload{Dir: dir, Err: host + " sent a listing this build cannot read."}
	}
	if out.Dir == "" {
		out.Dir = dir
	}
	out.Spoofed = false
	return &out
}
