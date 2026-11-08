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

// Images that are files, not transcript data.
//
// Two images a person expects to see in the thread are not in the
// transcript as data. A screenshot pasted through tuios is a file in this
// daemon's paste folder, and the prompt holds only its path. A read tool of
// some harnesses (view_image, read_file) names the image's path and gives no
// data back. The daemon adds an image entry for each, after the entry that
// names it, with the id "file.<entry id>.<k>".
//
// The rules that keep this to images the agent or the person put there:
//   - The caller never names a path. An image read finds the entry in the
//     newest page of the transcript again and takes the path from it.
//   - A prompt's path counts only when it is a file of this daemon's paste
//     folder, by its name prefix. A read tool's path counts only when it is
//     absolute, and only when no image follows the call.
//   - The file is opened without following a symbolic link, must be a
//     regular file of at most transcriptview.ImageMax bytes, and its first
//     bytes must be PNG, JPEG, GIF or WebP, whatever its name says.

// fileImagePrefix starts the id of an image that is a file.
const fileImagePrefix = "file."

// fileImagesMax bounds the images one entry gives.
const fileImagesMax = 4

// fileImageTypes are the media types by the sniffed extension.
var fileImageTypes = map[string]string{".png": "image/png", ".jpg": "image/jpeg", ".gif": "image/gif", ".webp": "image/webp"}

// fileImageReadTools are the read tools whose path may name an image.
var fileImageReadTools = map[string]bool{"view_image": true, "read_file": true, "read_many_files": true, "read": true, "view": true}

// fileImagePaths are the image files entry i names, at most fileImagesMax.
// pasteDir is this daemon's paste folder, "" for none.
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

// fileImageExt reports whether a path's name ends in an image extension.
func fileImageExt(p string) bool {
	switch strings.ToLower(filepath.Ext(p)) {
	case ".png", ".jpg", ".jpeg", ".gif", ".webp":
		return true
	}
	return false
}

// withFileImages adds an image entry after each entry that names an image
// file that is there now.
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

// readFileImage reads the image file that the entry named by id gives, from
// entries, the newest page of the transcript.
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

// openFileImage reads an image file by the rules above.
func openFileImage(p string) (string, []byte, error) {
	// OpenRegular does not follow a link where the platform can refuse
	// one at the open; Lstat refuses it on the others too.
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
