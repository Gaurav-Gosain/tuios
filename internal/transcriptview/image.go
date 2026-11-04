package transcriptview

import (
	"bufio"
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/Gaurav-Gosain/tuios/internal/transcript"
)

// An image the person pasted into a prompt is in the transcript as base64
// data. A page names it with an Image on its "[image]" entry, and a client
// that draws it asks for the data by the entry's ID. The ID holds the byte
// offset of the record and the place of the block in it, so the read is one
// record, not the file.

// ImageMax bounds the data of one image, decoded.
const ImageMax = 10 << 20

// ErrNoImage reports that the ID names no image this package can read.
var ErrNoImage = errors.New("transcriptview: no image with that id")

// imageType reports whether a media type is one a client may draw.
func imageType(t string) bool {
	switch t {
	case "image/png", "image/jpeg", "image/gif", "image/webp":
		return true
	}
	return false
}

// ReadImage reads the image of the entry with the given ID: its media type
// and its data. Only Claude Code transcripts carry images it reads.
func ReadImage(path, format, id string) (string, []byte, error) {
	if format != "" && format != FormatClaude {
		return "", nil, ErrNoImage
	}
	offs, idxs, ok := strings.Cut(id, "-")
	if !ok {
		return "", nil, ErrNoImage
	}
	off, err := strconv.ParseInt(offs, 36, 64)
	if err != nil || off < 0 {
		return "", nil, ErrNoImage
	}
	idx, err := strconv.Atoi(idxs)
	if err != nil || idx < 0 {
		return "", nil, ErrNoImage
	}
	f, err := transcript.OpenRegular(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil, ErrNoFile
		}
		return "", nil, err
	}
	defer func() { _ = f.Close() }()
	// The offset must start a record: the byte before it ends a line.
	if off > 0 {
		var nl [1]byte
		if _, err := f.ReadAt(nl[:], off-1); err != nil || nl[0] != '\n' {
			return "", nil, ErrNoImage
		}
	}
	if _, err := f.Seek(off, io.SeekStart); err != nil {
		return "", nil, ErrNoImage
	}
	line, err := bufio.NewReaderSize(io.LimitReader(f, lineMax+1), 64<<10).ReadBytes('\n')
	defer clear(line)
	if len(line) > lineMax || err != nil && !errors.Is(err, io.EOF) {
		return "", nil, ErrNoImage
	}
	var rec struct {
		Type        string `json:"type"`
		IsSidechain bool   `json:"isSidechain"`
		IsMeta      bool   `json:"isMeta"`
		Message     struct {
			Content json.RawMessage `json:"content"`
		} `json:"message"`
	}
	if json.Unmarshal(bytes.TrimSpace(line), &rec) != nil || rec.IsSidechain || rec.IsMeta ||
		rec.Type != "user" && rec.Type != "assistant" || len(rec.Message.Content) == 0 || rec.Message.Content[0] != '[' {
		return "", nil, ErrNoImage
	}
	var blocks []struct {
		Type   string `json:"type"`
		Source struct {
			Type      string `json:"type"`
			MediaType string `json:"media_type"`
			Data      string `json:"data"`
		} `json:"source"`
	}
	if json.Unmarshal(rec.Message.Content, &blocks) != nil || idx >= len(blocks) {
		return "", nil, ErrNoImage
	}
	b := blocks[idx]
	if b.Type != "image" || b.Source.Type != "base64" || !imageType(b.Source.MediaType) ||
		base64.StdEncoding.DecodedLen(len(b.Source.Data)) > ImageMax+2 {
		return "", nil, ErrNoImage
	}
	data, err := base64.StdEncoding.DecodeString(b.Source.Data)
	if err != nil || len(data) > ImageMax {
		return "", nil, ErrNoImage
	}
	return b.Source.MediaType, data, nil
}
