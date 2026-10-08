package session

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"image"
	"image/gif"
	"image/jpeg"
	"image/png"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	_ "golang.org/x/image/bmp"
	"golang.org/x/image/draw"
	_ "golang.org/x/image/webp"
)

// file-preview: what Quick Look shows, made on the machine the file is on.
//
// A preview of a file on another machine should not cost the file: a 40 MB
// photo is shown at 1600 pixels, a PDF as its first page, a film as one frame.
// So the machine with the file does the heavy part and sends the small result,
// and the client decodes what comes back (tuios-gpui's tuios-preview crate). A
// text file is sent as its first 256 KiB; the client highlights it.
//
// PDF pages and film frames come from pdftoppm and ffmpeg when that machine
// has them. Without pdftoppm a PDF up to 8 MiB is sent as it is, for a client
// that can draw one. Without ffmpeg a film is described, not shown.

const (
	previewMaxPx       = 1600
	previewMaxPxLimit  = 4096
	previewTextMax     = 256 << 10
	previewImageMax    = 64 << 20
	previewImageDimMax = 10000
	previewPDFBytesMax = 8 << 20
	previewSVGMax      = 4 << 20
	previewToolTimeout = 20 * time.Second
)

var (
	previewImageExt = map[string]bool{".png": true, ".jpg": true, ".jpeg": true, ".gif": true, ".webp": true, ".bmp": true}
	previewVideoExt = map[string]bool{".mp4": true, ".mkv": true, ".webm": true, ".mov": true, ".avi": true, ".m4v": true}
)

// verbFilePreview makes the preview of one file on this machine.
func (d *Daemon) verbFilePreview(_ *connState, params json.RawMessage) (any, *verbError) {
	var p struct {
		Path  string `json:"path"`
		MaxPx int    `json:"max_px"`
		Page  int    `json:"page"`
	}
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	path, verr := expandPath(p.Path)
	if verr != nil {
		return nil, verr
	}
	maxPx := p.MaxPx
	if maxPx <= 0 {
		maxPx = previewMaxPx
	}
	maxPx = min(maxPx, previewMaxPxLimit)
	page := max(p.Page, 1)

	fi, err := os.Stat(path)
	if err != nil {
		return nil, fileError("preview", path, err)
	}
	out := map[string]any{
		"path":  path,
		"name":  filepath.Base(path),
		"size":  fi.Size(),
		"mtime": fi.ModTime().UnixMilli(),
	}
	if fi.IsDir() {
		out["kind"] = "dir"
		return out, nil
	}
	ext := strings.ToLower(filepath.Ext(path))
	ctx, cancel := context.WithTimeout(d.ctx, previewToolTimeout)
	defer cancel()

	switch {
	case previewImageExt[ext]:
		if err := previewImage(path, fi.Size(), maxPx, out); err != nil {
			describeBinary(path, out, err.Error())
		}
		return out, nil
	case ext == ".svg":
		if fi.Size() <= previewSVGMax {
			if b, err := os.ReadFile(path); err == nil {
				out["kind"] = "svg"
				out["text"] = string(b)
				return out, nil
			}
		}
		describeBinary(path, out, "The drawing is too large to show.")
		return out, nil
	case ext == ".pdf":
		previewPDF(ctx, path, fi.Size(), maxPx, page, out)
		return out, nil
	case previewVideoExt[ext]:
		previewVideo(ctx, path, maxPx, out)
		return out, nil
	}
	if previewText(path, fi.Size(), out) {
		return out, nil
	}
	describeBinary(path, out, "")
	return out, nil
}

// previewImage decodes an image and sends it at most maxPx on its long side.
func previewImage(path string, size int64, maxPx int, out map[string]any) error {
	if size > previewImageMax {
		return errors.New("The image is too large to show.")
	}
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	cfg, format, err := image.DecodeConfig(f)
	if err != nil {
		return errors.New("The image could not be read.")
	}
	if cfg.Width > previewImageDimMax || cfg.Height > previewImageDimMax {
		return errors.New("The image is too large to show.")
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return err
	}
	var img image.Image
	if format == "gif" {
		// The first frame. An animation is not played in a preview.
		img, err = gif.Decode(f)
	} else {
		img, _, err = image.Decode(f)
	}
	if err != nil {
		return errors.New("The image could not be read.")
	}
	scaled := fit(img, maxPx)
	data, mime, err := encodePreview(scaled, format == "jpeg")
	if err != nil {
		return err
	}
	b := scaled.Bounds()
	out["kind"] = "image"
	out["mime"] = mime
	out["width"] = cfg.Width
	out["height"] = cfg.Height
	out["image_width"] = b.Dx()
	out["image_height"] = b.Dy()
	out["image"] = base64.StdEncoding.EncodeToString(data)
	return nil
}

// fit scales img down to at most maxPx on its long side.
func fit(img image.Image, maxPx int) image.Image {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	if w <= maxPx && h <= maxPx {
		return img
	}
	if w >= h {
		h = max(h*maxPx/w, 1)
		w = maxPx
	} else {
		w = max(w*maxPx/h, 1)
		h = maxPx
	}
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	draw.CatmullRom.Scale(dst, dst.Bounds(), img, b, draw.Src, nil)
	return dst
}

// encodePreview is the image as JPEG for a photo and PNG for everything else,
// which may hold transparency or hard edges.
func encodePreview(img image.Image, photo bool) ([]byte, string, error) {
	var buf bytes.Buffer
	if photo {
		if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 88}); err != nil {
			return nil, "", err
		}
		return buf.Bytes(), "image/jpeg", nil
	}
	enc := png.Encoder{CompressionLevel: png.BestSpeed}
	if err := enc.Encode(&buf, img); err != nil {
		return nil, "", err
	}
	return buf.Bytes(), "image/png", nil
}

// previewPDF sends one page as an image, or the file for a client to draw.
func previewPDF(ctx context.Context, path string, size int64, maxPx, page int, out map[string]any) {
	out["kind"] = "pdf"
	if pages := pdfPages(ctx, path); pages > 0 {
		out["pages"] = pages
	}
	out["page"] = page
	if tool, err := exec.LookPath("pdftoppm"); err == nil {
		cmd := exec.CommandContext(ctx, tool, "-png", "-singlefile", "-f", strconv.Itoa(page), "-l", strconv.Itoa(page),
			"-scale-to", strconv.Itoa(maxPx), path)
		var stdout bytes.Buffer
		cmd.Stdout = &stdout
		if err := cmd.Run(); err == nil && stdout.Len() > 0 {
			if cfg, err := png.DecodeConfig(bytes.NewReader(stdout.Bytes())); err == nil {
				out["image_width"] = cfg.Width
				out["image_height"] = cfg.Height
			}
			out["mime"] = "image/png"
			out["image"] = base64.StdEncoding.EncodeToString(stdout.Bytes())
			return
		}
	}
	if size <= previewPDFBytesMax {
		if b, err := os.ReadFile(path); err == nil {
			out["pdf"] = base64.StdEncoding.EncodeToString(b)
			return
		}
	}
	out["note"] = "This machine has no pdftoppm, and the file is too large to send for a preview."
}

// pdfPages counts a PDF's pages with pdfinfo, 0 when it cannot.
func pdfPages(ctx context.Context, path string) int {
	tool, err := exec.LookPath("pdfinfo")
	if err != nil {
		return 0
	}
	b, err := exec.CommandContext(ctx, tool, path).Output()
	if err != nil {
		return 0
	}
	for line := range strings.SplitSeq(string(b), "\n") {
		if rest, ok := strings.CutPrefix(line, "Pages:"); ok {
			n, _ := strconv.Atoi(strings.TrimSpace(rest))
			return n
		}
	}
	return 0
}

// previewVideo sends one frame from 5 % into the film, the first key frame
// there, as yazi does.
func previewVideo(ctx context.Context, path string, maxPx int, out map[string]any) {
	out["kind"] = "video"
	durMs := videoDuration(ctx, path)
	if durMs > 0 {
		out["duration_ms"] = durMs
	}
	tool, err := exec.LookPath("ffmpeg")
	if err != nil {
		out["note"] = "This machine has no ffmpeg, so no frame can be shown."
		return
	}
	at := float64(durMs) * 0.05 / 1000
	args := []string{"-v", "error", "-nostdin"}
	if at > 0 {
		args = append(args, "-ss", strconv.FormatFloat(at, 'f', 3, 64))
	}
	scale := "scale='min(" + strconv.Itoa(maxPx) + ",iw)':'min(" + strconv.Itoa(maxPx) + ",ih)':force_original_aspect_ratio=decrease"
	args = append(args, "-skip_frame", "nokey", "-i", path, "-frames:v", "1", "-vf", scale, "-f", "image2pipe", "-vcodec", "mjpeg", "-q:v", "3", "-")
	cmd := exec.CommandContext(ctx, tool, args...)
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	if err := cmd.Run(); err != nil || stdout.Len() == 0 {
		out["note"] = "No frame could be read from the film."
		return
	}
	if cfg, err := jpeg.DecodeConfig(bytes.NewReader(stdout.Bytes())); err == nil {
		out["image_width"] = cfg.Width
		out["image_height"] = cfg.Height
	}
	out["mime"] = "image/jpeg"
	out["image"] = base64.StdEncoding.EncodeToString(stdout.Bytes())
}

// videoDuration is the film's length in ms from ffprobe, 0 when it cannot.
func videoDuration(ctx context.Context, path string) int64 {
	tool, err := exec.LookPath("ffprobe")
	if err != nil {
		return 0
	}
	b, err := exec.CommandContext(ctx, tool, "-v", "error", "-show_entries", "format=duration", "-of", "default=nw=1:nk=1", path).Output()
	if err != nil {
		return 0
	}
	sec, err := strconv.ParseFloat(strings.TrimSpace(string(b)), 64)
	if err != nil {
		return 0
	}
	return int64(sec * 1000)
}

// previewText sends the start of a file that reads as text. It reports false
// for a file that does not.
func previewText(path string, size int64, out map[string]any) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer func() { _ = f.Close() }()
	buf := make([]byte, min(size, previewTextMax))
	n, _ := io.ReadFull(f, buf)
	buf = buf[:n]
	head := buf[:min(len(buf), 8192)]
	if bytes.IndexByte(head, 0) >= 0 {
		return false
	}
	// A cut through the last character is not a reason to call it binary.
	trimmed := buf
	for i := 0; i < 4 && len(trimmed) > 0 && !utf8.Valid(trimmed); i++ {
		trimmed = trimmed[:len(trimmed)-1]
	}
	if !utf8.Valid(trimmed) {
		return false
	}
	out["kind"] = "text"
	out["text"] = string(trimmed)
	out["truncated"] = size > int64(len(trimmed))
	out["mime"] = http.DetectContentType(head)
	return true
}

// describeBinary is the preview of a file that has none: its type and size.
func describeBinary(path string, out map[string]any, note string) {
	out["kind"] = "binary"
	if f, err := os.Open(path); err == nil {
		head := make([]byte, 512)
		n, _ := f.Read(head)
		_ = f.Close()
		out["mime"] = http.DetectContentType(head[:n])
	}
	if note != "" {
		out["note"] = note
	}
}

func previewVerbs() map[string]verbEntry {
	return map[string]verbEntry{
		"file-preview": {
			description: "Make the preview of a file on this machine. An image comes scaled to max_px on its long side, a PDF page and a film frame as an image (with pdftoppm and ffmpeg there), text as its first 256 KiB, and anything else as its type and size.",
			params: []verbParam{
				{Name: "path", Type: "string", Required: true, Description: "The file. An absolute path, or one that starts with ~."},
				{Name: "max_px", Type: "int", Description: "The longest side of an image, at most 4096.", Default: "1600"},
				{Name: "page", Type: "int", Description: "The PDF page.", Default: "1"},
			},
			returns: []verbParam{
				{Name: "kind", Type: "string", Description: "image, svg, pdf, video, text, dir or binary.", Accepted: []string{"image", "svg", "pdf", "video", "text", "dir", "binary"}},
				{Name: "image", Type: "string", Description: "The picture, base64, as mime says: the image, the page or the frame."},
				{Name: "mime", Type: "string", Description: "The picture's type for image, pdf and video, and the file's type otherwise."},
				{Name: "width", Type: "int", Description: "An image's own width."},
				{Name: "height", Type: "int", Description: "An image's own height."},
				{Name: "text", Type: "string", Description: "text and svg: the content."},
				{Name: "truncated", Type: "bool", Description: "text: the file is longer than what was sent."},
				{Name: "pdf", Type: "string", Description: "pdf, when this machine cannot draw the page: the file, base64, at most 8 MiB."},
				{Name: "pages", Type: "int", Description: "pdf: the page count, when pdfinfo is there."},
				{Name: "duration_ms", Type: "int", Description: "video: the length."},
				{Name: "note", Type: "string", Description: "Why there is no picture, in words a person can act on."},
				{Name: "size", Type: "int", Description: "The file's size."},
				{Name: "mtime", Type: "int", Description: "The file's modification time, Unix ms."},
			},
			examples: []string{`{"id":1,"verb":"file-preview","params":{"path":"~/Pictures/cat.jpg","max_px":1200}}`},
			handler:  (*Daemon).verbFilePreview,
		},
	}
}
