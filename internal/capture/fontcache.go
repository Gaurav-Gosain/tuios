package capture

import (
	"bytes"
	"crypto/sha256"
	"encoding/gob"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"

	"github.com/adrg/xdg"

	"github.com/Gaurav-Gosain/tuios/internal/atomicfile"
)

// The font index kept on disk.
//
// Every process that takes a screenshot used to scan the system's font files
// for the family it draws with: every font on the machine opened, and its name
// table read. That is 110 ms warm and about 580 ms cold on a machine with 1,090
// font files, and seconds on a slower disk. The answer only changes when a font
// is added, removed or replaced, so the faces found are kept in a file in the
// cache folder, tuios/fonts-index.gob, and read back in under a millisecond.
//
// The file says what it was made from. Its fingerprint is the list of font
// folders and, for every font file the walk finds, its path, size and
// modification time. The walk runs every time (it lists folders and stats
// files, 2 ms for 1,033 files, no font opened), so a font added, removed,
// renamed, or replaced by a package manager (a new file moved into place) or by
// a copy (a new time) is seen, and the index is made again and the file
// rewritten.
//
// What it does not see: a font file rewritten in place with the same size and
// the same modification time, such as cp -p of a different font of exactly the
// same size over it. The faces the file names are then the old ones until
// another font changes. Delete tuios/fonts-index.gob in the cache folder to scan
// again.
//
// The file is only ever a shortcut. One that is missing, short, damaged, from
// another format number or made from other fonts is a miss: the scan runs and
// the file is written again. A cache folder that cannot be written costs the
// shortcut and nothing else. The write is an atomic rename, so two processes
// that both scan leave one whole file.

// fontCacheVersion is the format of the file. Bump it when indexedFace or
// fontIndexFile changes shape.
const fontCacheVersion = 1

// fontCacheMax bounds the file read back, in bytes. A real one is 130 KiB for
// 1,100 faces; a file past this is not one.
const fontCacheMax = 32 << 20

// fontIndexFile is the file's content.
type fontIndexFile struct {
	Version     int
	Fingerprint string
	// Faces are the faces of every font file, in the order the walk met the
	// files, as a scan files them.
	Faces []indexedFace
}

// fontCachePath is the file, in the folder tuios keeps its cache in
// (adrg/xdg, as the rest of tuios reads its folders), or "" when there is no
// cache folder.
func fontCachePath() string {
	if xdg.CacheHome == "" {
		return ""
	}
	return filepath.Join(xdg.CacheHome, "tuios", "fonts-index.gob")
}

// fontFingerprint says which fonts a scan was made from: the folders, and each
// font file the walk found with its size and modification time.
func fontFingerprint(dirs []string, files []fontFileStamp) string {
	h := sha256.New()
	fmt.Fprintf(h, "v%d\n", fontCacheVersion)
	for _, d := range dirs {
		fmt.Fprintf(h, "d %s\n", d)
	}
	for _, f := range files {
		fmt.Fprintf(h, "f %s %d %d\n", f.path, f.size, f.mtime)
	}
	return hex.EncodeToString(h.Sum(nil))
}

// fontFileStamp is what the fingerprint holds of one font file.
type fontFileStamp struct {
	path  string
	size  int64
	mtime int64
}

// loadFontCache returns the faces the file holds when it was made from the
// fonts the fingerprint names, and false for any other answer.
func loadFontCache(path, fingerprint string) ([]indexedFace, bool) {
	if path == "" {
		return nil, false
	}
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() > fontCacheMax {
		return nil, false
	}
	data, err := os.ReadFile(path) // #nosec G304 - tuios's own cache file
	if err != nil {
		return nil, false
	}
	var c fontIndexFile
	if err := gob.NewDecoder(bytes.NewReader(data)).Decode(&c); err != nil {
		return nil, false
	}
	if c.Version != fontCacheVersion || c.Fingerprint != fingerprint {
		return nil, false
	}
	return c.Faces, true
}

// saveFontCache writes the faces and what they were made from. It is best
// effort: a failure is a missing shortcut.
func saveFontCache(path, fingerprint string, faces []indexedFace) {
	if path == "" {
		return
	}
	var buf bytes.Buffer
	if err := gob.NewEncoder(&buf).Encode(fontIndexFile{Version: fontCacheVersion, Fingerprint: fingerprint, Faces: faces}); err != nil {
		return
	}
	_ = atomicfile.WritePrivate(path, buf.Bytes(), false)
}
