// Package openedcopy keeps the copies tuios makes of files on other machines
// so a program on this desktop can open them: the rail's o on a row of a far
// listing.
//
// Each copy has a place of its own under the user cache:
//
//	$XDG_CACHE_HOME/tuios/opened/<host>/<hash>/<name>
//	$XDG_CACHE_HOME/tuios/opened/<host>/<hash>.json
//
// The hash is of the host and the far path, so the same far file always lands
// in the same place, and opening it again can reuse the copy. The record
// beside it says what the far file and the copy looked like when the copy
// ended. Its modification time is when the copy was last opened, which is what
// Sweep reads.
//
// The far name is whatever the program that made the file chose, so it goes
// through SafeName before it is part of a path here. The hash folder is a
// fixed-width hex word, so nothing the far machine says can name a folder
// outside the cache.
package openedcopy

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Gaurav-Gosain/tuios/internal/atomicfile"
	"github.com/Gaurav-Gosain/tuios/internal/errfmt"
	"github.com/Gaurav-Gosain/tuios/internal/jsonx"
	"github.com/adrg/xdg"
)

// recordExt is the suffix of a copy's record.
const recordExt = ".json"

// nameMax bounds a name in bytes. Most file systems take 255, and the cut
// leaves room for the part file the copy writes first (.NAME.tuios-part-ID).
const nameMax = 180

// Root is the folder every copy is under: tuios/opened in the cache home,
// read as the rest of tuios reads its folders (adrg/xdg). os.UserCacheDir
// would ignore XDG_CACHE_HOME on macOS, and put the copies where the person
// did not say tuios keeps its cache.
func Root() (string, error) {
	if xdg.CacheHome == "" {
		return "", errors.New("no cache folder: neither XDG_CACHE_HOME nor HOME is set")
	}
	return filepath.Join(xdg.CacheHome, "tuios", "opened"), nil
}

// Place is where the copy of one far path goes.
type Place struct {
	// Dir is the copy's own folder, and Path the copy in it.
	Dir  string
	Path string
	// Record is the file that describes the copy.
	Record string
}

// PlaceFor is the place of host's path under root. name is the name to give
// the copy, as the listing showed it.
func PlaceFor(root, host, farPath, name string) (Place, error) {
	sum := sha256.Sum256([]byte(host + "\x00" + farPath))
	hash := hex.EncodeToString(sum[:8])
	hostDir := filepath.Join(root, SafeName(host))
	p := Place{
		Dir:    filepath.Join(hostDir, hash),
		Record: filepath.Join(hostDir, hash+recordExt),
	}
	p.Path = filepath.Join(p.Dir, SafeName(name))
	// SafeName leaves no separator and no dot name, so this holds by
	// construction. It is checked anyway: the path is handed to the copy and
	// then to a program, and a slip here would write outside the cache.
	if !Inside(root, p.Path) || filepath.Dir(p.Path) != p.Dir {
		return Place{}, errfmt.Errorf("the copy of %q would land outside %s", name, root)
	}
	return p, nil
}

// Inside reports whether p is root or under it, by the cleaned paths.
func Inside(root, p string) bool {
	rel, err := filepath.Rel(filepath.Clean(root), filepath.Clean(p))
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

// windowsReserved are the device names Windows will not make as a file, with
// or without an extension.
var windowsReserved = []string{
	"CON", "PRN", "AUX", "NUL",
	"COM1", "COM2", "COM3", "COM4", "COM5", "COM6", "COM7", "COM8", "COM9",
	"LPT1", "LPT2", "LPT3", "LPT4", "LPT5", "LPT6", "LPT7", "LPT8", "LPT9",
}

// SafeName makes a far name into one path element here. It puts _ in place
// of a separator of either kind, of every control character and of every
// character a file name cannot hold on some system, and never answers "", "."
// or "..". So ..\..\x is .._.._x, in the copy's own folder. The extension stays, so a
// [[files.open]] rule still matches the copy.
func SafeName(name string) string {
	name = strings.ToValidUTF8(name, "_")
	var b strings.Builder
	for _, r := range name {
		switch {
		case r < 0x20 || r == 0x7f || (r >= 0x80 && r < 0xa0):
			b.WriteByte('_')
		case strings.ContainsRune(`/\:<>"|?*`, r):
			b.WriteByte('_')
		default:
			b.WriteRune(r)
		}
	}
	name = strings.TrimLeft(strings.TrimRight(b.String(), " ."), " ")
	if strings.Trim(name, ".") == "" {
		return "file"
	}
	stem, _, _ := strings.Cut(name, ".")
	if slices.Contains(windowsReserved, strings.ToUpper(stem)) {
		name = "_" + name
	}
	if len(name) > nameMax {
		ext := filepath.Ext(name)
		if len(ext) > 16 {
			ext = ""
		}
		cut := nameMax - len(ext)
		for cut > 0 && !utf8.RuneStart(name[cut]) {
			cut--
		}
		name = name[:cut] + ext
	}
	return name
}

// Record describes a copy as it was when the copy ended.
type Record struct {
	Host string `json:"host"`
	Path string `json:"path"`
	Dir  bool   `json:"dir,omitempty"`
	// Far is the far file's fingerprint when it was copied, and Local the
	// copy's when the copy ended. A copy whose fingerprint is not Local any
	// more was changed here.
	Far   string `json:"far"`
	Local string `json:"local"`
}

// ReadRecord reads a copy's record. A record that is not there is ok false.
func ReadRecord(path string) (Record, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Record{}, false
	}
	var r Record
	if jsonx.Unmarshal(data, &r) != nil {
		return Record{}, false
	}
	return r, true
}

// WriteRecord writes a copy's record, owner only.
func WriteRecord(path string, r Record) error {
	data, err := jsonx.Marshal(r)
	if err != nil {
		return err
	}
	return atomicfile.Write(path, data, 0o600, false)
}

// Touch marks a copy as opened now, so Sweep keeps it.
func Touch(record string) {
	now := time.Now()
	_ = os.Chtimes(record, now, now)
}

// Entry is one entry of a folder, as a fingerprint reads it.
type Entry struct {
	Rel   string
	Size  int64
	MTime int64
	Dir   bool
}

// FileFingerprint is a file's fingerprint: its size and modification time in
// Unix ms.
func FileFingerprint(size, mtime int64) string {
	return fmt.Sprintf("file:%d:%d", size, mtime)
}

// TreeFingerprint is a folder's fingerprint: every entry's path, and a file's
// size and modification time. A folder's own time is left out. A program that
// opens the folder can change it and nothing in it.
func TreeFingerprint(entries []Entry) string {
	lines := make([]string, len(entries))
	for i, e := range entries {
		if e.Dir {
			lines[i] = "d " + e.Rel
		} else {
			lines[i] = fmt.Sprintf("f %s %d %d", e.Rel, e.Size, e.MTime)
		}
	}
	slices.Sort(lines)
	sum := sha256.Sum256([]byte(strings.Join(lines, "\n")))
	return "tree:" + hex.EncodeToString(sum[:])
}

// LocalFingerprint is the fingerprint of the copy at path. It reads links as
// links and follows none.
func LocalFingerprint(path string, dir bool) (string, error) {
	fi, err := os.Lstat(path)
	if err != nil {
		return "", err
	}
	if !dir {
		return FileFingerprint(fi.Size(), fi.ModTime().UnixMilli()), nil
	}
	var entries []Entry
	err = filepath.WalkDir(path, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if p == path {
			return nil
		}
		rel, err := filepath.Rel(path, p)
		if err != nil {
			return err
		}
		e := Entry{Rel: filepath.ToSlash(rel), Dir: d.IsDir()}
		if !e.Dir {
			info, err := d.Info()
			if err != nil {
				return err
			}
			e.Size, e.MTime = info.Size(), info.ModTime().UnixMilli()
		}
		entries = append(entries, e)
		return nil
	})
	if err != nil {
		return "", err
	}
	return TreeFingerprint(entries), nil
}

// Sweep removes every copy under root that was last opened more than keep
// ago, and a copy that never got a record (one whose copy did not end) by its
// folder's time. keep of zero or less keeps everything. It answers how many
// copies it removed.
func Sweep(root string, keep time.Duration, now time.Time) (int, error) {
	if keep <= 0 {
		return 0, nil
	}
	hosts, err := os.ReadDir(root)
	if errors.Is(err, fs.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	removed := 0
	for _, h := range hosts {
		if !h.IsDir() {
			continue
		}
		hostDir := filepath.Join(root, h.Name())
		items, err := os.ReadDir(hostDir)
		if err != nil {
			continue
		}
		for _, it := range items {
			info, err := it.Info()
			if err != nil {
				continue
			}
			hash, isRecord := strings.CutSuffix(it.Name(), recordExt)
			if !isRecord && !it.IsDir() {
				continue
			}
			if !isRecord {
				// A folder with a record is judged by its record.
				if _, err := os.Lstat(filepath.Join(hostDir, hash+recordExt)); err == nil {
					continue
				}
			}
			if now.Sub(info.ModTime()) < keep {
				continue
			}
			if os.RemoveAll(filepath.Join(hostDir, hash)) == nil {
				_ = os.Remove(filepath.Join(hostDir, hash+recordExt))
				removed++
			}
		}
		// Gone only when empty.
		_ = os.Remove(hostDir)
	}
	return removed, nil
}

// Clear removes every copy under root. It answers how many copies there were
// and their size.
func Clear(root string) (copies int, size int64, err error) {
	err = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return nil
			}
			return err
		}
		if d.Type().IsRegular() {
			if strings.HasSuffix(p, recordExt) && filepath.Dir(filepath.Dir(p)) == filepath.Clean(root) {
				copies++
			} else if info, err := d.Info(); err == nil {
				size += info.Size()
			}
		}
		return nil
	})
	if err != nil {
		return 0, 0, err
	}
	return copies, size, os.RemoveAll(root)
}
