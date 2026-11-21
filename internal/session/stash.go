package session

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/errfmt"
)

const (
	stashMaxFileBytes    = 16 << 20
	stashMaxSessionBytes = 256 << 20
	stashDirPerm         = 0o700
	stashFilePerm        = 0o600
)

var (
	errStashNotAbsolute = errors.New("path must be absolute")
	errStashIsDirectory = errors.New("path is a directory")
	errStashNotRegular  = errors.New("path is not a regular file")
)

type stashEntry struct {
	Name      string `json:"name"`
	Path      string `json:"path"`
	Hash      string `json:"hash"`
	Bytes     int64  `json:"bytes"`
	MediaType string `json:"media_type"`
	Kind      string `json:"kind"`
	Source    string `json:"source"`
	StoredAt  int64  `json:"stored_at"`
}

type stashBox struct {
	dir     string
	entries []*stashEntry
	bytes   int64
	evicted uint64
}

type stashStore struct {
	mu         sync.Mutex
	socketPath func() string
	root       string
	boxes      map[string]*stashBox
}

func newStashStore(socketPath func() string) *stashStore {
	return &stashStore{socketPath: socketPath, boxes: map[string]*stashBox{}}
}

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

type stashResult struct {
	Entry     stashEntry
	Deduped   bool
	Evicted   int
	Evictions uint64
	Bytes     int64
	Entries   int
}

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
		return stashResult{}, errfmt.Errorf("file is %d bytes and the cap is %d bytes", info.Size(), stashMaxFileBytes)
	}

	f, err := os.Open(src)
	if err != nil {
		return stashResult{}, err
	}
	defer func() { _ = f.Close() }()

	return s.putFrom(sessionID, src, f, referenced)
}

func (s *stashStore) putFrom(sessionID, src string, r io.Reader, referenced func() map[string]bool) (stashResult, error) {
	if s == nil {
		return stashResult{}, errors.New("this daemon has no stash")
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	b, err := s.box(sessionID)
	if err != nil {
		return stashResult{}, err
	}

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
	written, err := io.Copy(io.MultiWriter(tmp, sum), io.LimitReader(r, stashMaxFileBytes+1))
	if err != nil {
		return stashResult{}, err
	}
	if written > stashMaxFileBytes {
		return stashResult{}, errfmt.Errorf("file is larger than %d bytes", stashMaxFileBytes)
	}
	if err := tmp.Close(); err != nil {
		return stashResult{}, err
	}
	if err := os.Chmod(tmpName, stashFilePerm); err != nil {
		return stashResult{}, err
	}

	hash := hex.EncodeToString(sum.Sum(nil))
	name := hash + stashExt(src)

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
			return stashResult{}, errfmt.Errorf(
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

func (b *stashBox) find(name string) (*stashEntry, int) {
	for i, e := range b.entries {
		if e.Name == name {
			return e, i
		}
	}
	return nil, -1
}

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

type stashListing struct {
	Dir       string
	Entries   []stashEntry
	Bytes     int64
	Evicted   uint64
	Reference map[string]bool
}

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

func (s *stashStore) open(sessionID, path string) (*os.File, *stashEntry, error) {
	if !s.owns(sessionID, path) {
		return nil, nil, os.ErrNotExist
	}
	s.mu.Lock()
	b := s.boxes[sessionID]
	var entry *stashEntry
	if b != nil {
		entry, _ = b.find(filepath.Base(filepath.Clean(path)))
	}
	s.mu.Unlock()
	if entry == nil {
		return nil, nil, os.ErrNotExist
	}
	f, err := os.Open(entry.Path)
	if err != nil {
		return nil, nil, err
	}
	copied := *entry
	return f, &copied, nil
}

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
