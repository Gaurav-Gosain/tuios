package session

import (
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"sync"
)

// How much a linked machine may write here.
//
// The write roots say where a link may write (verb_files_confine.go). These
// limits say how much. A machine with the files capability must not be able
// to fill a disk: the drop folder is often on tmpfs, in memory, and a full
// home disk stops every program the person runs.
//
//   - The drop folder holds at most dropKeepBytes. A write into it is checked
//     against what the folder holds, plus what other writes into it have
//     claimed, before any byte moves. A link writes only inside a drop that
//     file-drop-dir made, never in the drop folder itself, so pruneDrops, which
//     removes whole drops, can always make room.
//   - Every other write must leave files_min_free on its disk.
//   - A linked machine has at most linkWriteStreamsMax write streams and
//     linkPartsMax part files open here at once.

const (
	// linkWriteStreamsMax bounds the open-file-stream writes one linked
	// machine has open at once. A copy uses one at a time per file.
	linkWriteStreamsMax = 8
	// linkPartsMax bounds the part files one linked machine has here that
	// are not committed or aborted yet.
	linkPartsMax = 64
)

// linkFileUsage is what each linked machine has open here, by peer name.
type linkFileUsage struct {
	mu      sync.Mutex
	streams map[string]int
	parts   map[string]map[string]bool
	// dropClaimed is the bytes the open writes into the drop folder may
	// still add.
	dropClaimed int64
	// fsClaimed is the same for each other filesystem, by fsKey: the free
	// space a disk reports does not yet count the writes that are open on it.
	fsClaimed map[string]int64
}

// linkPeerKey names the machine on cs for the limits.
func linkPeerKey(cs *connState) string {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	if cs.linkPeer == "" {
		return "*"
	}
	return cs.linkPeer
}

// partDone forgets a part that was committed or aborted.
func (u *linkFileUsage) partDone(cs *connState, part string) {
	if cs == nil || !cs.viaLink {
		return
	}
	peer := linkPeerKey(cs)
	u.mu.Lock()
	delete(u.parts[peer], part)
	u.mu.Unlock()
}

// dirBytes is the bytes the regular files under dir hold.
func dirBytes(dir string) int64 {
	var n int64
	_ = filepath.WalkDir(dir, func(_ string, de fs.DirEntry, err error) error {
		if err == nil && de.Type().IsRegular() {
			if fi, err := de.Info(); err == nil {
				n += fi.Size()
			}
		}
		return nil
	})
	return n
}

// claimLinkWrite checks a write of length bytes from offset into the part
// part, for the final path path, against the limits, and claims its room. It
// returns release, which the caller runs when the stream ends. A call that
// did not come over a link passes with a release that does nothing.
func (d *Daemon) claimLinkWrite(cs *connState, path, part string, offset, length int64) (release func(), verr *verbError) {
	none := func() {}
	if cs == nil || !cs.viaLink {
		return none, nil
	}
	peer := linkPeerKey(cs)
	policy := d.linkPolicy(cs)
	partNow := int64(0)
	partExists := false
	if fi, err := os.Lstat(part); err == nil {
		partNow, partExists = fi.Size(), true
	}
	grow := max(offset+length-partNow, 0)

	u := &d.linkFiles
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.streams == nil {
		u.streams, u.parts = map[string]int{}, map[string]map[string]bool{}
	}
	if u.streams[peer] >= linkWriteStreamsMax {
		return none, newVerbError(ErrVerbBusy, "this machine already takes "+strconv.Itoa(linkWriteStreamsMax)+" copies at once from "+peerName(policy.Peer)+". Wait for one to end, then try again.")
	}
	if u.parts[peer] == nil {
		u.parts[peer] = map[string]bool{}
	}
	for p := range u.parts[peer] {
		if _, err := os.Lstat(p); err != nil {
			delete(u.parts[peer], p)
		}
	}
	if !partExists && !u.parts[peer][part] && len(u.parts[peer]) >= linkPartsMax {
		return none, newVerbError(ErrVerbBusy, peerName(policy.Peer)+" has "+strconv.Itoa(linkPartsMax)+" unfinished copies on this machine. Finish or cancel some, then try again.")
	}

	inDrop := false
	fs := ""
	if drop, err := d.dropRoot(); err == nil && pathUnder(realParent(path), realPath(drop)) {
		inDrop = true
		held := dirBytes(realPath(drop))
		if held+u.dropClaimed+grow > dropKeepBytes {
			return none, newVerbError(ErrVerbDiskFull, "the drop folder holds at most "+strconv.Itoa(dropKeepBytes>>20)+" MiB, and this file does not fit. Nothing was written.")
		}
	} else if policy.FilesMinFree > 0 {
		dir := filepath.Dir(realParent(path))
		fs = fsKey(dir)
		if u.fsClaimed == nil {
			u.fsClaimed = map[string]int64{}
		}
		if free := diskFree(dir); free >= 0 && free-u.fsClaimed[fs]-grow < policy.FilesMinFree {
			return none, newVerbError(ErrVerbDiskFull, "the disk on this machine would have less than "+sizeWords(policy.FilesMinFree)+" free after this file, so it was not written. Free space, or lower files_min_free in the config on this machine.")
		}
	}

	u.streams[peer]++
	u.parts[peer][part] = true
	if inDrop {
		u.dropClaimed += grow
	}
	if fs != "" {
		u.fsClaimed[fs] += grow
	}
	var once sync.Once
	return func() {
		once.Do(func() {
			u.mu.Lock()
			u.streams[peer]--
			if inDrop {
				u.dropClaimed -= grow
			}
			if fs != "" {
				u.fsClaimed[fs] -= grow
				if u.fsClaimed[fs] <= 0 {
					delete(u.fsClaimed, fs)
				}
			}
			u.mu.Unlock()
		})
	}, nil
}

// peerName is a linked machine's name for a message.
func peerName(peer string) string {
	if peer == "" {
		return "the linked machine"
	}
	return peer
}

// sizeWords writes n bytes as a person reads them: "1 GiB", "500 MiB".
func sizeWords(n int64) string {
	switch {
	case n >= 1<<30 && n%(1<<30) == 0:
		return strconv.FormatInt(n>>30, 10) + " GiB"
	case n >= 1<<20:
		return strconv.FormatInt(n>>20, 10) + " MiB"
	}
	return strconv.FormatInt(n, 10) + " bytes"
}
