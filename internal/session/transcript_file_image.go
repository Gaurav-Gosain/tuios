package session

import (
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/Gaurav-Gosain/tuios/internal/transcript"
	"github.com/Gaurav-Gosain/tuios/internal/transcriptview"
)

const fileImagePrefix = "file."

const fileImagesMax = 4

var fileImageTypes = map[string]string{".png": "image/png", ".jpg": "image/jpeg", ".gif": "image/gif", ".webp": "image/webp"}

var fileImageReadTools = map[string]bool{"view_image": true, "read_file": true, "read_many_files": true, "read": true, "view": true}

func fileImagePaths(entries []transcriptview.Entry, i int, pasteDir string) []string {
	en := entries[i]
	var out []string
	switch {
	case en.Role == "user" && en.Kind == "text" && en.Image == nil && pasteDir != "":
		for _, f := range strings.Fields(en.Text) {
			p := strings.Trim(f, "\"'`()[]<>,;")
			if filepath.Dir(p) == pasteDir && strings.HasPrefix(filepath.Base(p), pasteFilePrefix) && fileImageExt(p) {
				out = append(out, p)
			}
		}
	case en.Kind == "tool_call" && fileImageReadTools[strings.ToLower(en.Tool)] && filepath.IsAbs(en.Target) && fileImageExt(en.Target):
		for j := i + 1; j < len(entries) && entries[j].Kind != "tool_call"; j++ {
			if entries[j].Image != nil {
				return nil
			}
		}
		out = append(out, filepath.Clean(en.Target))
	}
	return out[:min(len(out), fileImagesMax)]
}

func fileImageExt(p string) bool {
	switch strings.ToLower(filepath.Ext(p)) {
	case ".png", ".jpg", ".jpeg", ".gif", ".webp":
		return true
	}
	return false
}

func withFileImages(entries []transcriptview.Entry, pasteDir string) []transcriptview.Entry {
	var out []transcriptview.Entry
	for i := range entries {
		paths := fileImagePaths(entries, i, pasteDir)
		if out == nil && len(paths) > 0 {
			out = append(make([]transcriptview.Entry, 0, len(entries)+len(paths)), entries[:i]...)
		}
		if out != nil {
			out = append(out, entries[i])
		}
		role := "user"
		if entries[i].Kind == "tool_call" {
			role = "tool"
		}
		for k, p := range paths {
			info, err := os.Lstat(p)
			if err != nil || !info.Mode().IsRegular() || info.Size() > transcriptview.ImageMax {
				continue
			}
			mt := fileImageTypes[strings.ToLower(filepath.Ext(p))]
			if mt == "" {
				mt = "image/jpeg"
			}
			out = append(out, transcriptview.Entry{ID: fileImagePrefix + entries[i].ID + "." + strconv.Itoa(k), Role: role, Kind: "text",
				Text: "[image]", At: entries[i].At, Image: &transcriptview.Image{MediaType: mt}})
		}
	}
	if out == nil {
		return entries
	}
	return out
}

func readFileImage(entries []transcriptview.Entry, pasteDir, id string) (string, []byte, error) {
	rest, ok := strings.CutPrefix(id, fileImagePrefix)
	cut := strings.LastIndexByte(rest, '.')
	if !ok || cut < 0 {
		return "", nil, transcriptview.ErrNoImage
	}
	k, err := strconv.Atoi(rest[cut+1:])
	if err != nil || k < 0 {
		return "", nil, transcriptview.ErrNoImage
	}
	for i := range entries {
		if entries[i].ID != rest[:cut] {
			continue
		}
		paths := fileImagePaths(entries, i, pasteDir)
		if k >= len(paths) {
			return "", nil, transcriptview.ErrNoImage
		}
		return openFileImage(paths[k])
	}
	return "", nil, transcriptview.ErrNoImage
}

func openFileImage(p string) (string, []byte, error) {
	if info, err := os.Lstat(p); err != nil || !info.Mode().IsRegular() {
		return "", nil, transcriptview.ErrNoImage
	}
	f, err := transcript.OpenRegular(p)
	if err != nil {
		return "", nil, transcriptview.ErrNoImage
	}
	defer func() { _ = f.Close() }()
	if info, err := f.Stat(); err != nil || info.Size() > transcriptview.ImageMax {
		return "", nil, transcriptview.ErrNoImage
	}
	data, err := io.ReadAll(io.LimitReader(f, transcriptview.ImageMax+1))
	if err != nil || len(data) > transcriptview.ImageMax {
		return "", nil, transcriptview.ErrNoImage
	}
	mt := fileImageTypes[sniffImage(data)]
	if mt == "" {
		return "", nil, transcriptview.ErrNoImage
	}
	return mt, data, nil
}
