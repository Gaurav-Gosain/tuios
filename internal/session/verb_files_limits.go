package session

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/units"
)

const (
	linkWriteStreamsMax = 8
	linkPartsMax        = 64
)

type linkFileUsage struct {
	mu          sync.Mutex
	streams     map[string]int
	parts       map[string]map[string]bool
	dropClaimed int64
	fsClaimed   map[string]int64
	entries     map[string]*entryUse
}

type entryUse struct {
	used float64
	at   time.Time
}

func (u *linkFileUsage) takeEntries(peer string, n int, limit int64, now time.Time) bool {
	if limit <= 0 || n <= 0 {
		return true
	}
	if u.entries == nil {
		u.entries = map[string]*entryUse{}
	}
	e := u.entries[peer]
	if e == nil {
		e = &entryUse{at: now}
		u.entries[peer] = e
	}
	back := now.Sub(e.at).Seconds() * float64(limit) / config.FilesEntryWindow.Seconds()
	e.used, e.at = max(e.used-back, 0), now
	if e.used+float64(n) > float64(limit) {
		return false
	}
	e.used += float64(n)
	return true
}

func tooManyEntries(policy config.LinkPolicy) *verbError {
	return hintedVerbError(ErrVerbBusy, peerName(policy.Peer)+" made too many new files and folders on "+thisMachineName()+". It may make "+strconv.FormatInt(policy.FilesMaxEntries, 10)+" in ten minutes (files_max_entries). Nothing was written.", &VerbHint{
		Detail: "Wait a few minutes, then try again. To allow more, raise files_max_entries in the config of " + thisMachineName() + ".",
	})
}

func (d *Daemon) claimLinkDirs(cs *connState, fsys fileFS, dir string) *verbError {
	if cs == nil || !cs.viaLink {
		return nil
	}
	return d.claimLinkEntries(cs, missingDirs(fsys, dir))
}

func (d *Daemon) claimLinkEntries(cs *connState, n int) *verbError {
	if cs == nil || !cs.viaLink || n <= 0 {
		return nil
	}
	peer := linkPeerKey(cs)
	policy := d.linkPolicy(cs)
	u := &d.linkFiles
	u.mu.Lock()
	defer u.mu.Unlock()
	if !u.takeEntries(peer, n, policy.FilesMaxEntries, time.Now()) {
		return tooManyEntries(policy)
	}
	return nil
}

func missingDirs(fsys fileFS, dir string) int {
	n := 0
	for {
		if _, err := fsys.Lstat(dir); !errors.Is(err, fs.ErrNotExist) {
			return n
		}
		n++
		up := filepath.Dir(dir)
		if up == dir {
			return n
		}
		dir = up
	}
}

func linkPeerKey(cs *connState) string {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	if cs.linkPeer == "" {
		return "*"
	}
	return cs.linkPeer
}

func (u *linkFileUsage) partDone(cs *connState, part string) {
	if cs == nil || !cs.viaLink {
		return
	}
	peer := linkPeerKey(cs)
	u.mu.Lock()
	delete(u.parts[peer], part)
	u.mu.Unlock()
}

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

func (d *Daemon) claimLinkWrite(cs *connState, path, part string, offset, length int64) (release func(), verr *verbError) {
	return d.claimLinkWriteIn(cs, path, part, offset, length, true)
}

func (d *Daemon) claimLinkStream(cs *connState) (release func(), verr *verbError) {
	none := func() {}
	if cs == nil || !cs.viaLink {
		return none, nil
	}
	peer := linkPeerKey(cs)
	u := &d.linkFiles
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.streams == nil {
		u.streams, u.parts = map[string]int{}, map[string]map[string]bool{}
	}
	if u.streams[peer] >= linkWriteStreamsMax {
		return none, newVerbError(ErrVerbBusy, thisMachineName()+" already takes "+strconv.Itoa(linkWriteStreamsMax)+" copies at once from "+peerName(d.linkPolicy(cs).Peer)+". Wait for one to end, then try again.")
	}
	u.streams[peer]++
	var once sync.Once
	return func() {
		once.Do(func() {
			u.mu.Lock()
			u.streams[peer]--
			u.mu.Unlock()
		})
	}, nil
}

func (d *Daemon) claimLinkWriteIn(cs *connState, path, part string, offset, length int64, stream bool) (release func(), verr *verbError) {
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
	if stream && u.streams[peer] >= linkWriteStreamsMax {
		return none, newVerbError(ErrVerbBusy, thisMachineName()+" already takes "+strconv.Itoa(linkWriteStreamsMax)+" copies at once from "+peerName(policy.Peer)+". Wait for one to end, then try again.")
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
		return none, newVerbError(ErrVerbBusy, peerName(policy.Peer)+" has "+strconv.Itoa(linkPartsMax)+" unfinished copies on "+thisMachineName()+". Finish or cancel some, then try again.")
	}

	inDrop := false
	fs := ""
	if drop, err := d.dropRoot(); err == nil && pathUnder(realParent(path), realPath(drop)) {
		inDrop = true
		held := dirBytes(realPath(drop))
		if held+u.dropClaimed+grow > d.dropKeepBytes {
			return none, newVerbError(ErrVerbDiskFull, "the drop folder holds at most "+units.Bytes(d.dropKeepBytes)+", and this file does not fit. Nothing was written.")
		}
	} else if policy.FilesMinFree > 0 {
		dir := filepath.Dir(realParent(path))
		fs = fsKey(dir)
		if u.fsClaimed == nil {
			u.fsClaimed = map[string]int64{}
		}
		if free := diskFree(dir); free >= 0 && free-u.fsClaimed[fs]-grow < policy.FilesMinFree {
			me := thisMachineName()
			return none, newVerbError(ErrVerbDiskFull, "the disk on "+me+" would have less than "+units.Bytes(policy.FilesMinFree)+" free after this file, so it was not written. Free space there, or lower files_min_free in the config of "+me+".")
		}
	}

	if !partExists && !u.parts[peer][part] && !u.takeEntries(peer, 1, policy.FilesMaxEntries, time.Now()) {
		return none, tooManyEntries(policy)
	}

	if stream {
		u.streams[peer]++
	}
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
			if stream {
				u.streams[peer]--
			}
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

func thisMachineName() string {
	if n := config.ThisMachine(); n != "" {
		return n
	}
	return "this machine"
}

func peerName(peer string) string {
	if peer == "" {
		return "the linked machine"
	}
	return peer
}
