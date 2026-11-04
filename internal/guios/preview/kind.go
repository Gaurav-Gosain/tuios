// Package preview reads files for guios's file viewer: what kind a file is,
// and the work each kind needs before it shows, done off the UI thread. A
// picture comes back decoded and scaled, a PDF as a document whose pages
// render on request, a film as its facts and the frame at a time, a table,
// a JSON tree or an archive's listing parsed, a large text through a line
// index that holds a few offsets, not the file.
//
// It draws nothing. cmd/guios turns what it returns into bitmaps and views.
// On macOS, Quartz draws PDF pages and ImageIO decodes pictures (the darwin
// files); elsewhere poppler's pdftoppm and ffmpeg do the work tools can, and
// Go's decoders the rest.
package preview

import (
	"bytes"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

// Kind is how a file previews.
type Kind int

const (
	// Unknown is a file no view reads: its facts and a hex dump.
	Unknown Kind = iota
	// Text is code and plain text, highlighted by its name.
	Text
	// Markdown is a document drawn as Markdown, with its source a view away.
	Markdown
	// Image is a picture: PNG, JPEG, WebP, BMP, TIFF, HEIC and the rest.
	Image
	// Animation is a picture with frames: a GIF of more than one.
	Animation
	// SVG is a drawing, shown as one, with its source a view away.
	SVG
	// PDF is a document of pages.
	PDF
	// Video is a film: frames at a time and a bar to move through it.
	Video
	// Audio is sound: its facts, its tags and its cover.
	Audio
	// Table is CSV or TSV.
	Table
	// JSON is a tree of JSON values.
	JSON
	// Archive is a zip or tar file's listing.
	Archive
	// Font is a font file, shown as a specimen.
	Font
	// Binary is a file that is not text: a hex dump.
	Binary
)

// String names a kind as the viewer's header says it.
func (k Kind) String() string {
	switch k {
	case Text:
		return "Text"
	case Markdown:
		return "Markdown"
	case Image:
		return "Image"
	case Animation:
		return "Animation"
	case SVG:
		return "SVG drawing"
	case PDF:
		return "PDF document"
	case Video:
		return "Video"
	case Audio:
		return "Audio"
	case Table:
		return "Table"
	case JSON:
		return "JSON"
	case Archive:
		return "Archive"
	case Font:
		return "Font"
	case Binary:
		return "Binary"
	}
	return "File"
}

var byExt = map[string]Kind{
	".md": Markdown, ".markdown": Markdown, ".mdown": Markdown, ".mkd": Markdown, ".mdx": Markdown,
	".png": Image, ".jpg": Image, ".jpeg": Image, ".webp": Image, ".bmp": Image, ".tif": Image, ".tiff": Image,
	".heic": Image, ".heif": Image, ".avif": Image, ".ico": Image, ".jxl": Image,
	".gif": Animation,
	".svg": SVG,
	".pdf": PDF,
	".mp4": Video, ".m4v": Video, ".mov": Video, ".mkv": Video, ".webm": Video, ".avi": Video,
	".mp3": Audio, ".m4a": Audio, ".aac": Audio, ".flac": Audio, ".wav": Audio, ".ogg": Audio, ".oga": Audio,
	".opus": Audio, ".aiff": Audio, ".aif": Audio, ".wma": Audio,
	".csv": Table, ".tsv": Table,
	".json": JSON, ".geojson": JSON, ".jsonc": JSON,
	".zip": Archive, ".tar": Archive, ".tgz": Archive, ".gz": Archive, ".jar": Archive, ".7z": Archive,
	".rar": Archive, ".xz": Archive, ".bz2": Archive, ".zst": Archive, ".tbz2": Archive, ".txz": Archive,
	".ttf": Font, ".otf": Font, ".ttc": Font, ".otc": Font,
}

// archiveSuffixes are the names of archives with two extensions.
var archiveSuffixes = []string{".tar.gz", ".tar.bz2", ".tar.xz", ".tar.zst"}

// ByName is the kind a file's name says, Unknown when it says none.
func ByName(name string) Kind {
	lower := strings.ToLower(name)
	for _, s := range archiveSuffixes {
		if strings.HasSuffix(lower, s) {
			return Archive
		}
	}
	return byExt[filepath.Ext(lower)]
}

// Detect is the kind of a file by its name, then by its first bytes: a
// picture or a PDF named as something else still shows as what it is, and a
// file of no known name shows as text when its start reads as text.
func Detect(name string, head []byte) Kind {
	if k := Sniff(head); k != Unknown {
		// A name that says text over bytes that say a picture: the
		// bytes win. A name that says a kind the sniff agrees with, or
		// a finer one (an MP4 named .m4a is audio), keeps the name's.
		if nk := ByName(name); nk != Unknown && compatible(nk, k) {
			return nk
		}
		return k
	}
	if k := ByName(name); k != Unknown {
		return k
	}
	if IsText(head) {
		return Text
	}
	return Binary
}

// compatible reports whether a kind by name fits the kind the bytes say.
func compatible(byName, sniffed Kind) bool {
	switch {
	case byName == sniffed:
		return true
	case sniffed == Video && byName == Audio, sniffed == Audio && byName == Video:
		return true // MP4 and Ogg hold either
	case sniffed == Archive && (byName == Archive):
		return true
	case sniffed == Image && byName == Animation, sniffed == Animation && byName == Image:
		return true
	}
	return false
}

// Sniff is the kind a file's first bytes say, Unknown when they say none.
func Sniff(b []byte) Kind {
	has := func(p string) bool { return bytes.HasPrefix(b, []byte(p)) }
	switch {
	case has("%PDF-"):
		return PDF
	case has("\x89PNG\r\n\x1a\n"), has("\xff\xd8\xff"), has("BM") && len(b) > 14 && b[14] >= 12 && b[14] <= 124,
		has("II*\x00"), has("MM\x00*"), has("\x00\x00\x01\x00"):
		return Image
	case has("GIF87a"), has("GIF89a"):
		return Animation
	case len(b) >= 12 && has("RIFF") && string(b[8:12]) == "WEBP":
		return Image
	case len(b) >= 12 && has("RIFF") && string(b[8:12]) == "WAVE":
		return Audio
	case len(b) >= 12 && has("RIFF") && string(b[8:12]) == "AVI ":
		return Video
	case len(b) >= 12 && string(b[4:8]) == "ftyp":
		switch string(b[8:12]) {
		case "heic", "heix", "hevc", "heim", "heis", "mif1", "msf1", "avif", "avis":
			return Image
		case "M4A ", "M4B ", "M4P ":
			return Audio
		}
		return Video
	case has("\x1aE\xdf\xa3"):
		return Video // Matroska and WebM
	case has("ID3"), has("fLaC"), has("OggS"), has("FORM") && len(b) >= 12 && string(b[8:12]) == "AIFF":
		return Audio
	case len(b) >= 2 && b[0] == 0xff && b[1]&0xe0 == 0xe0 && b[1]&0x06 != 0:
		return Audio // an MPEG audio frame with no tag before it
	case has("PK\x03\x04"), has("PK\x05\x06"), has("\x1f\x8b"), has("7z\xbc\xaf\x27\x1c"), has("Rar!\x1a\x07"),
		has("\xfd7zXZ\x00"), has("BZh"), has("\x28\xb5\x2f\xfd"):
		return Archive
	case len(b) > 262 && string(b[257:262]) == "ustar":
		return Archive
	case has("\x00\x01\x00\x00\x00"), has("OTTO\x00"), has("true\x00"), has("ttcf\x00"):
		// The sfnt version, then a count of tables under 256.
		if len(b) >= 12 {
			return Font
		}
	case has("<?xml") || has("<svg"):
		if bytes.Contains(b[:min(len(b), 1024)], []byte("<svg")) {
			return SVG
		}
	}
	return Unknown
}

// IsText reports whether a file's start reads as text: no NUL, and UTF-8
// but for a character cut at the end.
func IsText(b []byte) bool {
	if bytes.IndexByte(b, 0) >= 0 {
		return false
	}
	for i := 0; i < 4 && len(b) > 0 && !utf8.Valid(b); i++ {
		b = b[:len(b)-1]
	}
	return utf8.Valid(b)
}

// IsTSV reports whether a table's name says tabs part its cells.
func IsTSV(name string) bool { return strings.EqualFold(filepath.Ext(name), ".tsv") }
