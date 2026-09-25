package session

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/worktree"
)

// bundle-worktree: a worktree's work, read out in pieces so it can cross a
// link.
//
// `tuios worktree pull build:api-fan-2` is the caller. It asks the daemon on
// build for the worktree's commits as a git bundle and its uncommitted work
// as a binary patch, reads both back in chunks over its one connection, and
// makes a local worktree session from them. The chunking is why this is not
// stash get: stash get answers in one reply line, capped at 8 MB, and a
// branch's history can be larger.
//
// The first call makes the transfer and answers with what is in it and the
// first chunk. Later calls pass the token and an offset. The transfer lives
// as long as the connection that made it, and at most bundleIdle past the
// last read, so a caller that goes away leaves nothing behind. Only the
// connection that made a transfer can read it.
//
// What a caller reads here is the worktree's git objects and its files, which
// any caller that can run a command in a pane can read already. Over a link it
// needs the write capability, the one that lets a machine type into a shell
// here, checked before the handler and again in checkLinkPolicy.

// bundleChunkBytes is the most one reply carries, before base64. It is well
// under the 16 MB a reply line may hold. A variable so a test can make a small
// transfer take several chunks.
var bundleChunkBytes = 4 << 20

const (
	// bundleMaxBytes bounds one transfer. A branch past it is better moved by
	// git push, and the refusal says so.
	bundleMaxBytes = 512 << 20
	// bundleMaxOpen bounds the transfers one daemon holds at once.
	bundleMaxOpen = 4
	// bundleIdle is how long a transfer is kept with nobody reading it.
	bundleIdle = 10 * time.Minute
)

// bundleStore is the open transfers. The zero value is ready.
type bundleStore struct {
	mu   sync.Mutex
	open map[string]*bundleTransfer
}

// bundleTransfer is one transfer: a file of bundle bytes then patch bytes.
type bundleTransfer struct {
	token   string
	owner   *connState
	session string
	dir     string
	path    string
	size    int64
	touched time.Time
	gone    chan struct{}
	// dropping is set, under the store's lock, by the one drop that removes
	// this transfer. See drop.
	dropping bool
}

// put adds a transfer and starts the goroutine that removes it when its
// connection ends, the daemon stops, or nobody reads it for bundleIdle.
func (s *bundleStore) put(d *Daemon, t *bundleTransfer) bool {
	s.mu.Lock()
	if s.open == nil {
		s.open = map[string]*bundleTransfer{}
	}
	if len(s.open) >= bundleMaxOpen {
		s.mu.Unlock()
		return false
	}
	t.touched = time.Now()
	t.gone = make(chan struct{})
	s.open[t.token] = t
	s.mu.Unlock()

	var ownerDone <-chan struct{}
	if t.owner != nil {
		ownerDone = t.owner.done
	}
	go func() {
		tick := time.NewTicker(time.Minute)
		defer tick.Stop()
		for {
			select {
			case <-ownerDone:
			case <-d.ctx.Done():
			case <-t.gone:
				return
			case <-tick.C:
				s.mu.Lock()
				idle := time.Since(t.touched)
				s.mu.Unlock()
				if idle < bundleIdle {
					continue
				}
			}
			s.drop(t.token)
			return
		}
	}()
	return true
}

// get returns the transfer for token, if cs made it.
func (s *bundleStore) get(cs *connState, token string) *bundleTransfer {
	s.mu.Lock()
	defer s.mu.Unlock()
	t := s.open[token]
	if t == nil || t.owner != cs || t.dropping {
		return nil
	}
	t.touched = time.Now()
	return t
}

// drop removes a transfer's files and then the transfer. The files go
// first: a transfer is gone once it has left the store, and until then it is
// only dropping, which get refuses. Deleting it from the store first let a
// caller see it gone while its files were still on disk.
func (s *bundleStore) drop(token string) {
	s.mu.Lock()
	t := s.open[token]
	if t == nil || t.dropping {
		s.mu.Unlock()
		return
	}
	t.dropping = true
	s.mu.Unlock()

	close(t.gone)
	_ = os.RemoveAll(t.dir)

	s.mu.Lock()
	delete(s.open, token)
	s.mu.Unlock()
}

// count is how many transfers are open.
func (s *bundleStore) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.open)
}

func (d *Daemon) verbBundleWorktree(cs *connState, params json.RawMessage) (any, *verbError) {
	var p struct {
		Session string `json:"session"`
		Full    bool   `json:"full"`
		Token   string `json:"token"`
		Offset  int64  `json:"offset"`
		Release bool   `json:"release"`
	}
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	if verr := d.checkLinkPolicy(cs, linkCapFiles, "bundle-worktree"); verr != nil {
		return nil, verr
	}
	if p.Token != "" {
		return d.readBundleChunk(cs, p.Token, p.Offset, p.Release)
	}
	if p.Release || p.Offset != 0 {
		return nil, invalidParam("token", "offset and release name a transfer: pass the token the first call returned")
	}
	if p.Session == "" {
		return nil, hintedVerbError(ErrVerbInvalidParams, "session is required (bundle-worktree never guesses which worktree to read)",
			&VerbHint{Param: "session", Command: "tuios worktree ls", Available: d.worktreeSessionNames()})
	}
	sess, info, verr := d.worktreeTarget(p.Session)
	if verr != nil {
		return nil, verr
	}
	if _, err := os.Stat(info.Path); err != nil {
		return nil, hintedVerbError(ErrVerbNotWorktree, "the worktree of "+sess.Name+" is gone: "+info.Path+" no longer exists", nil)
	}
	return d.openBundle(cs, sess.Name, info, p.Full)
}

// liveHead reads the branch and commit the worktree's HEAD is on now. The
// session's info.Branch is the branch the session was made on, and a managed
// worktree session never refreshes it, so an agent that switched branch or
// detached HEAD since would otherwise be carried as the old branch with a
// head that is not on it.
func liveHead(sessionName, path string) (branch, head string, verr *verbError) {
	branch, err := worktree.CurrentBranch(path)
	if err != nil {
		return "", "", hintedVerbError(ErrVerbGitFailed, err.Error(), nil)
	}
	if branch == "" {
		return "", "", hintedVerbError(ErrVerbNotWorktree, "the worktree of "+sessionName+" has a detached HEAD, so there is no branch to carry", &VerbHint{
			Detail: "Check out a branch in the worktree first.",
		})
	}
	head, err = worktree.HeadCommit(path)
	if err != nil {
		return "", "", hintedVerbError(ErrVerbGitFailed, err.Error(), nil)
	}
	return branch, head, nil
}

// openBundle makes the transfer for a worktree and answers with its first
// chunk.
//
// The branch bundled, the head reported and the commit the patch is made
// against all come from the worktree's HEAD as git has it now, not from the
// branch recorded on the session. HEAD is read again once the bundle and the
// patch are written, and a HEAD that moved in between refuses the transfer,
// so the three always agree.
func (d *Daemon) openBundle(cs *connState, sessionName string, info *WorktreeInfo, full bool) (any, *verbError) {
	branch, head, verr := liveHead(sessionName, info.Path)
	if verr != nil {
		return nil, verr
	}
	exclude := ""
	if !full && info.Base != "" {
		if mb, err := worktree.MergeBase(info.Path, info.Base, head); err == nil {
			exclude = mb
		}
	}

	dir, err := os.MkdirTemp("", "tuios-bundle-*")
	if err != nil {
		return nil, newVerbError(ErrVerbInternal, "cannot make a directory for the transfer: "+err.Error())
	}
	keep := false
	defer func() {
		if !keep {
			_ = os.RemoveAll(dir)
		}
	}()

	bundlePath := filepath.Join(dir, "commits.bundle")
	bundleErr := worktree.Bundle(info.Path, branch, exclude, bundlePath)
	switch {
	case errors.Is(bundleErr, worktree.ErrEmptyBundle):
		bundlePath = ""
	case bundleErr != nil:
		return nil, hintedVerbError(ErrVerbGitFailed, bundleErr.Error(), &VerbHint{Detail: "git could not bundle the branch. The worktree is as it was."})
	}
	patchPath := filepath.Join(dir, "work.patch")
	changes, err := worktree.WorkingPatch(info.Path, patchPath)
	if err != nil {
		return nil, hintedVerbError(ErrVerbGitFailed, err.Error(), &VerbHint{Detail: "git could not read the uncommitted work. The worktree is as it was."})
	}
	// The agent in the worktree can commit or switch branch while the bundle
	// and the patch are written. Either would leave a head that is not the
	// bundled branch's tip, or a patch made against another commit.
	if nowBranch, nowHead, verr := liveHead(sessionName, info.Path); verr != nil {
		return nil, verr
	} else if nowBranch != branch || nowHead != head {
		return nil, hintedVerbError(ErrVerbGitFailed, "the worktree of "+sessionName+" moved from "+branch+" at "+head+" while its work was read", &VerbHint{
			Detail: "Something in the worktree committed or switched branch. Nothing was changed. Try again.",
		})
	}
	if tip, err := worktree.BranchCommit(info.Path, branch); err != nil || tip != head {
		return nil, hintedVerbError(ErrVerbGitFailed, "branch "+branch+" is not at the worktree's HEAD "+head, &VerbHint{
			Detail: "Something in the worktree moved the branch while its work was read. Nothing was changed. Try again.",
		})
	}

	transferPath := filepath.Join(dir, "transfer")
	bundleBytes, patchBytes, sum, err := joinTransfer(transferPath, bundlePath, patchPath)
	if err != nil {
		return nil, newVerbError(ErrVerbInternal, "cannot write the transfer: "+err.Error())
	}
	size := bundleBytes + patchBytes
	if size > bundleMaxBytes {
		return nil, hintedVerbError(ErrVerbInvalidParams, "the worktree's work is larger than a transfer may be", &VerbHint{
			Param:  "full",
			Detail: "Push the branch from that machine and fetch it here instead. Without full, only the commits past the base are carried.",
		})
	}
	origin, _ := worktree.OriginURL(info.RepoRoot)

	token, err := newBundleToken()
	if err != nil {
		return nil, newVerbError(ErrVerbInternal, "cannot make a transfer token: "+err.Error())
	}
	t := &bundleTransfer{token: token, owner: cs, session: sessionName, dir: dir, path: transferPath, size: size}
	if !d.bundles.put(d, t) {
		return nil, hintedVerbError(ErrVerbRateLimited, "this daemon already has the most transfers it holds open at once", &VerbHint{
			Detail: "Each ends when the connection that made it closes. Try again when one has finished.",
		})
	}
	keep = true

	out := map[string]any{
		"type":         "worktree_bundle",
		"session":      sessionName,
		"token":        token,
		"repo":         info.Repo,
		"origin_url":   origin,
		"branch":       branch,
		"base":         info.Base,
		"base_commit":  exclude,
		"head":         head,
		"full":         exclude == "",
		"bundle_bytes": bundleBytes,
		"patch_bytes":  patchBytes,
		"changes":      changes,
		"size":         size,
		"sha256":       sum,
	}
	chunk, verr := d.readBundleChunk(cs, token, 0, false)
	if verr != nil {
		return nil, verr
	}
	for k, v := range chunk {
		if k != "type" {
			out[k] = v
		}
	}
	return out, nil
}

// readBundleChunk answers one chunk of a transfer, and drops the transfer
// after its last chunk or on release.
func (d *Daemon) readBundleChunk(cs *connState, token string, offset int64, release bool) (map[string]any, *verbError) {
	t := d.bundles.get(cs, token)
	if t == nil {
		return nil, invalidParam("token", "no transfer with that token is open on this connection. A transfer ends with the connection that made it, and after ten minutes unread.")
	}
	if release {
		d.bundles.drop(token)
		return map[string]any{"type": "worktree_bundle_chunk", "token": token, "released": true, "done": true}, nil
	}
	if offset < 0 || offset > t.size {
		return nil, invalidParam("offset", "offset is past the end of the transfer")
	}
	f, err := os.Open(t.path)
	if err != nil {
		return nil, newVerbError(ErrVerbInternal, "cannot read the transfer: "+err.Error())
	}
	defer func() { _ = f.Close() }()
	buf := make([]byte, min(int64(bundleChunkBytes), t.size-offset))
	if _, err := f.ReadAt(buf, offset); err != nil && !errors.Is(err, io.EOF) {
		return nil, newVerbError(ErrVerbInternal, "cannot read the transfer: "+err.Error())
	}
	next := offset + int64(len(buf))
	done := next >= t.size
	if done {
		d.bundles.drop(token)
	}
	return map[string]any{
		"type":    "worktree_bundle_chunk",
		"token":   token,
		"offset":  offset,
		"next":    next,
		"done":    done,
		"content": base64.StdEncoding.EncodeToString(buf),
	}, nil
}

// joinTransfer writes the bundle, then the patch, to dest, and returns the
// size of each and the SHA-256 of the whole. An empty bundlePath is a
// transfer with no commits.
func joinTransfer(dest, bundlePath, patchPath string) (bundleBytes, patchBytes int64, sum string, err error) {
	out, err := os.OpenFile(dest, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return 0, 0, "", err
	}
	defer func() { _ = out.Close() }()
	h := sha256.New()
	w := io.MultiWriter(out, h)
	copyFile := func(path string) (int64, error) {
		if path == "" {
			return 0, nil
		}
		f, err := os.Open(path)
		if err != nil {
			return 0, err
		}
		defer func() { _ = f.Close() }()
		return io.Copy(w, io.LimitReader(f, bundleMaxBytes+1))
	}
	if bundleBytes, err = copyFile(bundlePath); err != nil {
		return 0, 0, "", err
	}
	if patchBytes, err = copyFile(patchPath); err != nil {
		return 0, 0, "", err
	}
	return bundleBytes, patchBytes, hex.EncodeToString(h.Sum(nil)), out.Close()
}

func newBundleToken() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}
