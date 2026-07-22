// Package trust implements the security foundation for .tuios.tape autorun:
// a per-machine trust store that records, for each project tape, its canonical
// path and the SHA-256 of its exact content, together with whether the user has
// trusted or denied it.
//
// The store is direnv's model. A tape is inert until the user explicitly trusts
// it; trust is bound to the (canonical path, content hash) pair, so any edit to
// the file reverts it to untrusted and re-prompts. Denial is keyed by path
// alone, so a hostile edit cannot nag the user back into a prompt.
//
// Stage 1 (this package plus its detection and indicator callers) never
// executes a tape. It only stats, reads, hashes, and reports trust status. The
// single-read Check API is deliberately shaped so a later stage can execute the
// exact bytes that were hashed, defeating a swap of the file (or its symlink
// target) between the trust check and the run.
package trust

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/adrg/xdg"
	"github.com/pelletier/go-toml/v2"
)

// MaxTapeSize bounds the bytes read from a tape. A layout script fits in a
// small fraction of this; the cap keeps a later review dialog honest and stops
// a pathologically large or unbounded file (a device, a fifo target) from being
// slurped into memory.
const MaxTapeSize = 64 * 1024

// TapeFileName is the fixed basename of a project tape.
const TapeFileName = ".tuios.tape"

// Status is the trust verdict for a tape encountered at a directory.
type Status int

const (
	// StatusIneligible means the tape fails the hygiene preconditions (not a
	// regular file, not owned by the current user, group- or world-writable, or
	// larger than MaxTapeSize). An ineligible tape can never be offered for
	// trust; the only response is an explanatory one-line notice.
	StatusIneligible Status = iota
	// StatusUntrusted means the tape is eligible but not trusted: either never
	// seen, or its content hash no longer matches the trusted hash for its path
	// (it was edited since it was trusted).
	StatusUntrusted
	// StatusTrusted means the tape's path is trusted and its current content
	// hash matches the stored hash.
	StatusTrusted
	// StatusDenied means the user chose "never for this path". Denial is by path
	// alone, so it survives edits to the file and produces no prompt or
	// indicator until explicitly cleared.
	StatusDenied
)

// String renders a Status as a lowercase word for logs and UI.
func (s Status) String() string {
	switch s {
	case StatusIneligible:
		return "ineligible"
	case StatusUntrusted:
		return "untrusted"
	case StatusTrusted:
		return "trusted"
	case StatusDenied:
		return "denied"
	default:
		return "unknown"
	}
}

// Result is the outcome of checking a tape file.
//
// Content holds the exact bytes that Hash was computed over, read in a single
// pass from the same file descriptor the hygiene checks were applied to. A
// later stage must execute Content, never re-read the path: that is what makes
// the check TOCTOU-safe. Content is nil for a denied or ineligible tape, which
// never runs.
type Result struct {
	Status  Status
	Path    string // canonical path (filepath.EvalSymlinks), the trust key
	Hash    string // hex SHA-256 of Content, empty when Content is nil
	Content []byte // exact hashed bytes; reused verbatim by a later run
	Size    int64
	Reason  string // why a tape is ineligible, for the notice; empty otherwise
}

// trustEntry is a trusted (path, hash) record. Only the latest hash per path is
// kept; re-trusting an edited tape replaces it.
type trustEntry struct {
	Path      string    `toml:"path"`
	SHA256    string    `toml:"sha256"`
	TrustedAt time.Time `toml:"trusted_at"`
}

// denyEntry is a "never for this path" record. It carries no hash by design.
type denyEntry struct {
	Path     string    `toml:"path"`
	DeniedAt time.Time `toml:"denied_at"`
}

// trustFile is the on-disk shape of the store.
type trustFile struct {
	Trusted []trustEntry `toml:"trusted"`
	Denied  []denyEntry  `toml:"denied"`
}

// Store is the in-memory trust store backed by a TOML file. It is the single
// writer of that file. All methods are safe for concurrent use.
type Store struct {
	path string

	mu      sync.Mutex
	trusted map[string]trustEntry
	denied  map[string]denyEntry

	// Warning is non-empty when the store file was present but failed its own
	// integrity checks (wrong permissions or owner). In that case its contents
	// are deliberately not loaded, so no stale or planted entry is honored and
	// every tape reads as untrusted until the user re-establishes trust.
	Warning string
}

// DefaultPath returns the trust store location,
// $XDG_DATA_HOME/tuios/tape-trust.toml, creating the parent directory. It is a
// state file, not config, so it does not travel with dotfile syncing: trust
// decisions are per-machine by design.
func DefaultPath() (string, error) {
	return xdg.DataFile("tuios/" + trustFileBaseName)
}

const trustFileBaseName = "tape-trust.toml"

// Load opens the trust store at its default XDG location, creating an empty
// 0600 file if none exists.
func Load() (*Store, error) {
	path, err := DefaultPath()
	if err != nil {
		return nil, fmt.Errorf("resolving tape trust store path: %w", err)
	}
	return LoadFromPath(path)
}

// LoadFromPath opens (or creates) the trust store at path. A missing store is
// created empty with 0600 permissions and a 0700 parent. A present store is
// validated: if it is group- or world-accessible, or not owned by the current
// user, its contents are ignored and Warning is set, so tampering downgrades
// trust to nothing rather than being honored.
func LoadFromPath(path string) (*Store, error) {
	s := &Store{
		path:    path,
		trusted: make(map[string]trustEntry),
		denied:  make(map[string]denyEntry),
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return s, fmt.Errorf("creating tape trust store directory: %w", err)
	}

	info, err := os.Stat(path)
	if os.IsNotExist(err) {
		// First run: materialize an empty, correctly-permissioned store so the
		// mode is right from the start rather than after the first write.
		if werr := s.save(); werr != nil {
			return s, werr
		}
		return s, nil
	}
	if err != nil {
		return s, fmt.Errorf("stat tape trust store: %w", err)
	}

	if !ownedByCurrentUser(info) || isGroupOrWorldAccessible(info) {
		s.Warning = fmt.Sprintf(
			"tape trust store %s has unsafe owner or permissions (want 0600, owned by you); ignoring its contents",
			path,
		)
		// Re-tighten what we can so the next legitimate write starts from a safe
		// mode, but do not trust anything the tampered file claimed.
		_ = os.Chmod(path, 0o600)
		return s, nil
	}

	// #nosec G304 - path is the trust store location, reading it is the point.
	data, err := os.ReadFile(path)
	if err != nil {
		return s, fmt.Errorf("reading tape trust store: %w", err)
	}

	var file trustFile
	if err := toml.Unmarshal(data, &file); err != nil {
		// A corrupt store is treated like a tampered one: ignore its contents
		// rather than crashing or silently honoring half of it.
		s.Warning = fmt.Sprintf("tape trust store %s is corrupt (%v); ignoring its contents", path, err)
		return s, nil
	}

	for _, e := range file.Trusted {
		if e.Path == "" || e.SHA256 == "" {
			continue
		}
		s.trusted[e.Path] = e
	}
	for _, e := range file.Denied {
		if e.Path == "" {
			continue
		}
		s.denied[e.Path] = e
	}
	return s, nil
}

// Check resolves, hygiene-checks, reads, and hashes the tape at tapePath in a
// single pass, and returns its trust status.
//
// The read is done once, from a file descriptor whose fstat drives the hygiene
// checks, so the bytes in Result.Content are exactly the bytes that were
// validated and hashed. A later stage executes Content directly; it must never
// re-open the path. This is what defeats a file swap or a symlink retarget
// between the check and the run.
//
// Denial is evaluated first and by canonical path alone, so a denied tape never
// causes a read and cannot be nagged back to life by editing its content.
func (s *Store) Check(tapePath string) (Result, error) {
	real, err := filepath.EvalSymlinks(tapePath)
	if err != nil {
		return Result{
			Status: StatusIneligible,
			Path:   tapePath,
			Reason: "cannot resolve path",
		}, fmt.Errorf("resolving tape path: %w", err)
	}
	if abs, aerr := filepath.Abs(real); aerr == nil {
		real = abs
	}

	s.mu.Lock()
	_, denied := s.denied[real]
	trusted, isTrusted := s.trusted[real]
	s.mu.Unlock()

	if denied {
		return Result{Status: StatusDenied, Path: real}, nil
	}

	// Open once and stat the descriptor. Doing hygiene on the fd (not a second
	// path stat) ties the checks to the same inode the bytes come from.
	// #nosec G304 - opening the resolved tape to hash it is the intended action.
	f, err := os.Open(real)
	if err != nil {
		return Result{
			Status: StatusIneligible,
			Path:   real,
			Reason: "cannot open file",
		}, fmt.Errorf("opening tape: %w", err)
	}
	defer func() { _ = f.Close() }()

	info, err := f.Stat()
	if err != nil {
		return Result{
			Status: StatusIneligible,
			Path:   real,
			Reason: "cannot stat file",
		}, fmt.Errorf("stat tape: %w", err)
	}

	if reason, ok := hygieneReason(info); !ok {
		return Result{Status: StatusIneligible, Path: real, Reason: reason, Size: info.Size()}, nil
	}

	// Read from the same descriptor, capped one byte past the limit so an
	// oversize file is detected without reading it all.
	content, err := io.ReadAll(io.LimitReader(f, MaxTapeSize+1))
	if err != nil {
		return Result{
			Status: StatusIneligible,
			Path:   real,
			Reason: "cannot read file",
		}, fmt.Errorf("reading tape: %w", err)
	}
	if int64(len(content)) > MaxTapeSize {
		return Result{
			Status: StatusIneligible,
			Path:   real,
			Reason: fmt.Sprintf("larger than %d KiB", MaxTapeSize/1024),
			Size:   int64(len(content)),
		}, nil
	}

	sum := sha256.Sum256(content)
	hash := hex.EncodeToString(sum[:])

	status := StatusUntrusted
	if isTrusted && trusted.SHA256 == hash {
		status = StatusTrusted
	}

	return Result{
		Status:  status,
		Path:    real,
		Hash:    hash,
		Content: content,
		Size:    int64(len(content)),
	}, nil
}

// Trust records trust for a (canonical path, hash) pair, replacing any earlier
// hash for that path and clearing any deny entry, then persists the store. The
// hash must be the one from a Result the caller has already displayed; there is
// no "trust without seeing it" path.
func (s *Store) Trust(path, hash string) error {
	if path == "" || hash == "" {
		return fmt.Errorf("trust requires a path and a hash")
	}
	s.mu.Lock()
	s.trusted[path] = trustEntry{Path: path, SHA256: hash, TrustedAt: time.Now().UTC()}
	delete(s.denied, path)
	err := s.saveLocked()
	s.mu.Unlock()
	return err
}

// Deny records "never for this path", clearing any trust entry, then persists.
func (s *Store) Deny(path string) error {
	if path == "" {
		return fmt.Errorf("deny requires a path")
	}
	s.mu.Lock()
	s.denied[path] = denyEntry{Path: path, DeniedAt: time.Now().UTC()}
	delete(s.trusted, path)
	err := s.saveLocked()
	s.mu.Unlock()
	return err
}

// Forget removes both trust and deny entries for a path, returning it to the
// default untrusted-but-promptable state, then persists.
func (s *Store) Forget(path string) error {
	s.mu.Lock()
	delete(s.trusted, path)
	delete(s.denied, path)
	err := s.saveLocked()
	s.mu.Unlock()
	return err
}

// TrustedHash returns the stored trusted hash for a canonical path and whether
// one exists. It lets a caller tell a never-seen tape apart from one that was
// trusted and has since been edited (a stored hash that no longer matches),
// which the review dialog flags as "changed since you trusted it".
func (s *Store) TrustedHash(path string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.trusted[path]
	if !ok {
		return "", false
	}
	return e.SHA256, true
}

// save persists the store, taking the lock.
func (s *Store) save() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.saveLocked()
}

// saveLocked writes the store atomically (temp file plus rename) with 0600
// permissions. The caller holds s.mu.
func (s *Store) saveLocked() error {
	file := trustFile{}
	for _, e := range s.trusted {
		file.Trusted = append(file.Trusted, e)
	}
	for _, e := range s.denied {
		file.Denied = append(file.Denied, e)
	}

	data, err := toml.Marshal(file)
	if err != nil {
		return fmt.Errorf("encoding tape trust store: %w", err)
	}

	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("creating tape trust store directory: %w", err)
	}

	tmp, err := os.CreateTemp(dir, ".tape-trust-*.tmp")
	if err != nil {
		return fmt.Errorf("creating tape trust store temp file: %w", err)
	}
	tmpName := tmp.Name()
	cleanup := func() { _ = os.Remove(tmpName) }

	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		cleanup()
		return fmt.Errorf("setting tape trust store permissions: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		cleanup()
		return fmt.Errorf("writing tape trust store: %w", err)
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return fmt.Errorf("closing tape trust store temp file: %w", err)
	}
	if err := os.Rename(tmpName, s.path); err != nil {
		cleanup()
		return fmt.Errorf("replacing tape trust store: %w", err)
	}
	return nil
}
