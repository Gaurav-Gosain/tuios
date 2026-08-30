package session

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// This file holds the session stash: a daemon-owned file store an agent can put
// bytes into and then attach by path, with the session's lifetime rather than
// the producer's.
//
// It exists because the mailbox's attachments are sender-owned paths. That is
// the right default and it stays the fast path: the ring never copies, so a
// megabyte image costs the ring nothing. What it does not answer is who owns the
// file. The sender's temp file can vanish between the send and the read, which
// the reader learns as MISSING, and every agent invents its own /tmp convention
// to avoid it. The stash is the other half of the same split A2A's FilePart
// makes: bytes when the producer will not keep them, a uri when it will. The
// bytes still never enter the ring; they go on disk beside the socket, and the
// message carries the path as before.
//
// The one promise is lifetime. A stashed file lives as long as the session and
// no longer. That is what makes it safe to hand another agent a path and stop
// thinking about it, and it is why the store never falls back to a directory
// that outlives a boot: a stash that quietly became permanent would be a disk
// leak with a friendly name, which is exactly what this replaces.

// Caps. Same reasoning as the ring's: a store with no bound is a disk leak.
const (
	// stashMaxFileBytes bounds one stashed file. It is large enough for a
	// screenshot, a core log or a profile, and small enough that a copy holding
	// the store's lock is measured in milliseconds.
	stashMaxFileBytes = 16 << 20
	// stashMaxSessionBytes bounds everything one session holds at once.
	stashMaxSessionBytes = 256 << 20
	// stashDirPerm is the mode of the stash root and of each session directory.
	// It matches the socket directory's 0700: the store holds whatever agents
	// hand it, so it is readable by its owner and nobody else.
	stashDirPerm = 0o700
	// stashFilePerm is the mode of a stored file. Read and write for the owner,
	// never executable: nothing in the store is meant to be run.
	stashFilePerm = 0o600
)

var (
	errStashNotAbsolute = errors.New("path must be absolute")
	errStashIsDirectory = errors.New("path is a directory")
	errStashNotRegular  = errors.New("path is not a regular file")
)

// stashEntry is one stored file. It is content-addressed: the stored name is the
// sha256 of the bytes plus the source's extension, so two agents that stash the
// same file end up pointing at one copy.
type stashEntry struct {
	// Name is the stored file's base name, sha256 hex plus the extension.
	Name string `json:"name"`
	// Path is the absolute path a caller attaches. It is what the CLI prints.
	Path string `json:"path"`
	// Hash is the full sha256 of the content, hex.
	Hash string `json:"hash"`
	// Bytes is the stored size, which is the size actually copied rather than
	// the size the source claimed before the copy started.
	Bytes int64 `json:"bytes"`
	// MediaType and Kind classify the file the same way an attachment is
	// classified, from the extension and nothing else.
	MediaType string `json:"media_type"`
	Kind      string `json:"kind"`
	// Source is the path the file was copied from, kept so a human reading a
	// listing can tell two hashes apart. It is never resolved again.
	Source string `json:"source"`
	// StoredAt is unix-nano. StampedAt on a dedup hit as well, because a second
	// caller asking for the same bytes is a use, and eviction ranks by use.
	StoredAt int64 `json:"stored_at"`
}

// stashBox is one session's directory and index.
type stashBox struct {
	dir string
	// entries are ordered oldest use first, which is the order eviction walks.
	entries []*stashEntry
	bytes   int64
	// evicted counts the files this session's store has deleted to make room.
	// Counted rather than silent, the same discipline the ring follows: a caller
	// whose file is gone can see that something took it.
	evicted uint64
}

// stashStore is the daemon's whole stash: one root directory and one box per
// live session.
//
// Locking. Every operation holds mu for its whole duration, the copy included.
// Two agents stashing at once are serialised, which is the point: the per-
// session cap is only exact if the check and the write cannot interleave with
// another writer's, and an eviction that runs beside a put would otherwise be
// choosing victims from a set that is changing under it. The cost is bounded by
// the per-file cap, so the worst wait one agent imposes on another is one 16 MiB
// copy.
//
// Lock order. stashStore.mu is taken before agentBus.mu and never the other way
// round. The stash asks the bus which files its messages still name; the bus
// knows nothing about the stash.
type stashStore struct {
	mu sync.Mutex
	// socketPath is resolved lazily rather than at construction, because the
	// daemon may still be told a different socket path after it is built. Once
	// resolved it is memoised in root.
	socketPath func() string
	root       string
	boxes      map[string]*stashBox
}

func newStashStore(socketPath func() string) *stashStore {
	return &stashStore{socketPath: socketPath, boxes: map[string]*stashBox{}}
}

// rootDir returns the stash root, creating it on first use. Callers hold s.mu.
//
// The root sits beside the daemon socket: $XDG_RUNTIME_DIR/tuios/stash when that
// variable is set, and /tmp/tuios-$UID/stash when it is not, which is the same
// fallback the socket itself takes. There is deliberately no third choice. Both
// of these go away with the boot, so the honest worst case for a daemon that is
// killed and never restarted is that the files sit in a per-boot directory; a
// fallback into the user's home would make them permanent, and permanent is the
// one thing this store must never be.
func (s *stashStore) rootDir() (string, error) {
	if s.root == "" {
		sock := s.socketPath()
		if sock == "" {
			return "", errors.New("the daemon has no socket path, so it has nowhere to put a stash")
		}
		s.root = filepath.Join(filepath.Dir(sock), "stash")
	}
	if err := os.MkdirAll(s.root, stashDirPerm); err != nil {
		return "", err
	}
	return s.root, nil
}

// box returns a session's store, creating the directory on first use. Callers
// hold s.mu.
//
// The key is the session id, not its name. A name can be reused by a session
// created after this one is killed, and a stale directory from a daemon that was
// killed rather than stopped would then be adopted by a session that never wrote
// it. An id cannot be reused, so a box always holds only what its own session
// put there.
func (s *stashStore) box(sessionID string) (*stashBox, error) {
	if b := s.boxes[sessionID]; b != nil {
		if err := os.MkdirAll(b.dir, stashDirPerm); err != nil {
			return nil, err
		}
		return b, nil
	}
	root, err := s.rootDir()
	if err != nil {
		return nil, err
	}
	dir := filepath.Join(root, sessionID)
	if err := os.MkdirAll(dir, stashDirPerm); err != nil {
		return nil, err
	}
	b := &stashBox{dir: dir}
	s.boxes[sessionID] = b
	return b, nil
}

// stashResult is what one put reports back.
type stashResult struct {
	Entry stashEntry
	// Deduped is true when the bytes were already stored and the copy was
	// discarded. The caller gets the path that already existed.
	Deduped bool
	// Evicted is how many files this put deleted to make room, and Evictions is
	// the session's running total.
	Evicted   int
	Evictions uint64
	// Bytes and Entries are the session's totals after the put.
	Bytes   int64
	Entries int
}

// put copies src into the session's store and returns the stored path.
//
// referenced names the files this session's ring still points at, which is the
// only input eviction takes beyond age. It is a function rather than a set so it
// is evaluated inside the lock, once, and only when a put actually has to evict.
func (s *stashStore) put(sessionID, src string, referenced func() map[string]bool) (stashResult, error) {
	if s == nil {
		return stashResult{}, errors.New("this daemon has no stash")
	}
	if !filepath.IsAbs(src) {
		return stashResult{}, errStashNotAbsolute
	}
	src = filepath.Clean(src)

	info, err := os.Stat(src)
	if err != nil {
		return stashResult{}, err
	}
	if info.IsDir() {
		return stashResult{}, errStashIsDirectory
	}
	if !info.Mode().IsRegular() {
		return stashResult{}, errStashNotRegular
	}
	if info.Size() > stashMaxFileBytes {
		return stashResult{}, fmt.Errorf("file is %d bytes and the cap is %d bytes", info.Size(), stashMaxFileBytes)
	}

	f, err := os.Open(src)
	if err != nil {
		return stashResult{}, err
	}
	defer func() { _ = f.Close() }()

	s.mu.Lock()
	defer s.mu.Unlock()

	b, err := s.box(sessionID)
	if err != nil {
		return stashResult{}, err
	}

	// The copy goes to a temp file first, so a put that fails halfway leaves no
	// half-file under a name that says it is complete. The hash is taken from
	// the bytes that were actually written rather than from the source read a
	// second time, which would be a different file if the source changed.
	tmp, err := os.CreateTemp(b.dir, ".put-")
	if err != nil {
		return stashResult{}, err
	}
	tmpName := tmp.Name()
	committed := false
	defer func() {
		_ = tmp.Close()
		if !committed {
			_ = os.Remove(tmpName)
		}
	}()

	sum := sha256.New()
	// One byte past the cap, so a source that grew after the stat above is
	// refused rather than allowed to walk over the per-file bound.
	written, err := io.Copy(io.MultiWriter(tmp, sum), io.LimitReader(f, stashMaxFileBytes+1))
	if err != nil {
		return stashResult{}, err
	}
	if written > stashMaxFileBytes {
		return stashResult{}, fmt.Errorf("file is larger than %d bytes", stashMaxFileBytes)
	}
	if err := tmp.Close(); err != nil {
		return stashResult{}, err
	}
	if err := os.Chmod(tmpName, stashFilePerm); err != nil {
		return stashResult{}, err
	}

	hash := hex.EncodeToString(sum.Sum(nil))
	name := hash + stashExt(src)

	// Identical bytes with the same extension are one entry. Two agents that
	// stash the same screenshot get one file and one path, which is what makes
	// the per-session cap a bound on distinct content rather than on how many
	// times it was handed around.
	//
	// The entry moves to the newest end on a hit. A second caller asking for
	// these bytes is a use, and eviction ranks by use: the file two agents want
	// should not be the first one taken.
	if existing, at := b.find(name); existing != nil {
		existing.StoredAt = time.Now().UnixNano()
		b.entries = append(append(b.entries[:at:at], b.entries[at+1:]...), existing)
		return stashResult{
			Entry:     *existing,
			Deduped:   true,
			Evictions: b.evicted,
			Bytes:     b.bytes,
			Entries:   len(b.entries),
		}, nil
	}

	evicted := 0
	if b.bytes+written > stashMaxSessionBytes {
		var keep map[string]bool
		if referenced != nil {
			keep = referenced()
		}
		evicted = b.evict(written, keep)
		if b.bytes+written > stashMaxSessionBytes {
			return stashResult{}, fmt.Errorf(
				"this session's stash holds %d bytes of %d and nothing else can be dropped",
				b.bytes, stashMaxSessionBytes)
		}
	}

	final := filepath.Join(b.dir, name)
	if err := os.Rename(tmpName, final); err != nil {
		return stashResult{}, err
	}
	committed = true

	media := mediaTypeFor(final)
	kind := "file"
	if strings.HasPrefix(media, "image/") {
		kind = "image"
	}
	entry := &stashEntry{
		Name:      name,
		Path:      final,
		Hash:      hash,
		Bytes:     written,
		MediaType: media,
		Kind:      kind,
		Source:    src,
		StoredAt:  time.Now().UnixNano(),
	}
	b.entries = append(b.entries, entry)
	b.bytes += written

	return stashResult{
		Entry:     *entry,
		Evicted:   evicted,
		Evictions: b.evicted,
		Bytes:     b.bytes,
		Entries:   len(b.entries),
	}, nil
}

// find returns the entry with this stored name and its index. Callers hold
// s.mu.
func (b *stashBox) find(name string) (*stashEntry, int) {
	for i, e := range b.entries {
		if e.Name == name {
			return e, i
		}
	}
	return nil, -1
}

// evict deletes unreferenced entries, oldest use first, until there is room for
// need more bytes, and reports how many it took. Callers hold s.mu.
//
// A referenced entry is one a message still in the session's ring names as an
// attachment. Those are skipped, never deleted: deleting one would turn a
// message that reads as delivered into a reference to nothing, which is the
// failure the stash exists to remove. When only referenced entries are left this
// returns having freed less than asked, and the caller refuses the put. Refusing
// is the right end of that: the alternative is breaking a message that is still
// readable to make room for one that has not been sent.
func (b *stashBox) evict(need int64, referenced map[string]bool) int {
	took := 0
	kept := b.entries[:0]
	for _, e := range b.entries {
		if b.bytes+need <= stashMaxSessionBytes || referenced[e.Path] {
			kept = append(kept, e)
			continue
		}
		if err := os.Remove(e.Path); err != nil && !os.IsNotExist(err) {
			LogError("Failed to evict stashed file %s: %v", e.Path, err)
			kept = append(kept, e)
			continue
		}
		b.bytes -= e.Bytes
		b.evicted++
		took++
	}
	b.entries = kept
	return took
}

// stashListing is one session's store as a listing reads it.
type stashListing struct {
	Dir       string
	Entries   []stashEntry
	Bytes     int64
	Evicted   uint64
	Reference map[string]bool
}

// list returns a session's store, newest last, with the referenced set resolved
// so a caller can see which files are safe from eviction and which are next.
func (s *stashStore) list(sessionID string, referenced func() map[string]bool) (stashListing, error) {
	if s == nil {
		return stashListing{}, errors.New("this daemon has no stash")
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	out := stashListing{Reference: map[string]bool{}}
	b := s.boxes[sessionID]
	if b == nil {
		root, err := s.rootDir()
		if err != nil {
			return out, err
		}
		out.Dir = filepath.Join(root, sessionID)
		return out, nil
	}
	out.Dir = b.dir
	out.Bytes = b.bytes
	out.Evicted = b.evicted
	if referenced != nil {
		out.Reference = referenced()
	}
	out.Entries = make([]stashEntry, 0, len(b.entries))
	for _, e := range b.entries {
		out.Entries = append(out.Entries, *e)
	}
	return out, nil
}

// owns reports whether a path is a file this session's store put there. It is
// what marks an attachment as daemon-owned, so a reader can tell a file that
// will be there for the session from one the sender may delete.
//
// The comparison is on cleaned paths and nothing else. A caller that spells a
// stashed path through a symlinked directory is told the file is not stashed,
// which is the safe way to be wrong: it reads as a sender-owned path, which is
// what every attachment was before this existed.
func (s *stashStore) owns(sessionID, path string) bool {
	if s == nil || path == "" {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	b := s.boxes[sessionID]
	if b == nil {
		return false
	}
	e, _ := b.find(filepath.Base(filepath.Clean(path)))
	return e != nil && e.Path == filepath.Clean(path)
}

// forget deletes a session's whole store. It runs on session deletion, so the
// files go when the session does, which is the entire lifetime promise.
func (s *stashStore) forget(sessionID string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	b := s.boxes[sessionID]
	delete(s.boxes, sessionID)
	if b == nil {
		return
	}
	if err := os.RemoveAll(b.dir); err != nil {
		LogError("Failed to remove stash directory %s: %v", b.dir, err)
	}
}

// sweep deletes the whole stash root and forgets every box.
//
// It runs twice: at daemon start, before anything is served, and at daemon
// shutdown. The shutdown call is the guarantee for an ordinary stop, including
// SIGINT and SIGTERM, which both reach it. The start call is what covers the
// case the shutdown call cannot: a daemon that was SIGKILLed or lost with the
// machine leaves its files behind, and the next daemon removes them before it
// accepts a connection. Nothing is guaranteed in the window between an unclean
// death and the next start, and the files sit in a per-boot directory until
// then. That window is the honest limit of the promise.
func (s *stashStore) sweep() {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.boxes = map[string]*stashBox{}
	root, err := s.rootDir()
	if err != nil {
		return
	}
	if err := os.RemoveAll(root); err != nil {
		LogError("Failed to clear the stash root %s: %v", root, err)
	}
}

// stashExt returns the extension to store a file under.
//
// Only a short alphanumeric extension is kept, because the stored name is a path
// component the daemon builds and a source called "notes.tar gz backup" would
// otherwise put spaces in it. Anything else is dropped and the file is stored
// under its hash alone, which classifies as application/octet-stream: the
// content is intact, only the guess about its type is gone.
func stashExt(src string) string {
	ext := filepath.Ext(src)
	if len(ext) < 2 || len(ext) > 17 {
		return ""
	}
	for _, r := range ext[1:] {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		default:
			return ""
		}
	}
	return strings.ToLower(ext)
}

// referencedPaths returns every attachment path the messages still in a
// session's ring name.
//
// It lives here rather than beside the ring because it is the stash's question,
// not the mailbox's: the ring does not care what a path points at. The set is
// what "oldest unreferenced first" is resolved against, and the coupling is only
// this one read. A message aging out of the ring does not delete anything; it
// only makes the file it named eligible, and the file is not touched until a
// later put needs the room. Eviction is lazy on purpose: a file whose message
// scrolled away is very often the file its reader is still working with, and
// deleting it the moment the ring forgot the message would take it out from
// under that reader for no gain, since nothing is over its cap.
func (b *agentBus) referencedPaths(session string) map[string]bool {
	out := map[string]bool{}
	if b == nil {
		return out
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, m := range b.box(session).msgs {
		for _, a := range m.Attachments {
			out[filepath.Clean(a.Path)] = true
		}
	}
	return out
}
