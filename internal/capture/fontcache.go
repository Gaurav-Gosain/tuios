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

const fontCacheVersion = 1

const fontCacheMax = 32 << 20

type fontIndexFile struct {
	Version     int
	Fingerprint string
	Faces       []indexedFace
}

func fontCachePath() string {
	if xdg.CacheHome == "" {
		return ""
	}
	return filepath.Join(xdg.CacheHome, "tuios", "fonts-index.gob")
}

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

type fontFileStamp struct {
	path  string
	size  int64
	mtime int64
}

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
