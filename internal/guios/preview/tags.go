package preview

import (
	"bytes"
	"encoding/binary"
	"image"
	"io"
	"os"
	"strings"
	"unicode/utf16"
)

// Tags without ffprobe: what an MP3's ID3v2 tag, a FLAC's metadata blocks
// and a WAV's header say, read in Go. ffprobe reads more, so it goes first
// where it is.

// tagReadMax bounds what is read of a file's start for its tags.
const tagReadMax = 16 << 20

// tagFacts reads the facts of a sound file without a tool.
func tagFacts(path string) (Facts, bool) {
	b, size, ok := readHead(path, tagReadMax)
	if !ok {
		return Facts{}, false
	}
	f := Facts{Engine: "tags", Tags: map[string]string{}}
	switch {
	case bytes.HasPrefix(b, []byte("ID3")):
		end := id3Tags(b, &f, nil)
		mp3Duration(b[min(end, len(b)):], size-int64(end), &f)
	case bytes.HasPrefix(b, []byte("fLaC")):
		flacTags(b, &f, nil)
	case len(b) >= 12 && string(b[:4]) == "RIFF" && string(b[8:12]) == "WAVE":
		wavFacts(b, &f)
	default:
		if len(b) >= 2 && b[0] == 0xff && b[1]&0xe0 == 0xe0 {
			mp3Duration(b, size, &f)
			break
		}
		return Facts{}, false
	}
	return f, true
}

// tagCover reads a sound file's cover picture without a tool.
func tagCover(path string, maxPx int) (image.Image, bool) {
	b, _, ok := readHead(path, tagReadMax)
	if !ok {
		return nil, false
	}
	var pic []byte
	switch {
	case bytes.HasPrefix(b, []byte("ID3")):
		id3Tags(b, &Facts{Tags: map[string]string{}}, &pic)
	case bytes.HasPrefix(b, []byte("fLaC")):
		flacTags(b, &Facts{Tags: map[string]string{}}, &pic)
	}
	if pic == nil {
		return nil, false
	}
	img, _, err := image.Decode(bytes.NewReader(pic))
	if err != nil {
		return nil, false
	}
	return Fit(img, maxPx), true
}

func readHead(path string, limit int) ([]byte, int64, bool) {
	f, err := os.Open(path)
	if err != nil {
		return nil, 0, false
	}
	defer func() { _ = f.Close() }()
	fi, err := f.Stat()
	if err != nil {
		return nil, 0, false
	}
	b, err := io.ReadAll(io.LimitReader(f, int64(limit)))
	if err != nil {
		return nil, 0, false
	}
	return b, fi.Size(), true
}

// id3Tags reads an ID3v2.3 or 2.4 tag's text frames and its first picture,
// and returns where the tag ends.
func id3Tags(b []byte, f *Facts, pic *[]byte) int {
	if len(b) < 10 {
		return len(b)
	}
	ver := b[3]
	size := syncsafe(b[6:10])
	end := min(10+size, len(b))
	names := map[string]string{"TIT2": "title", "TPE1": "artist", "TALB": "album", "TYER": "date", "TDRC": "date", "TRCK": "track", "TCON": "genre", "TPE2": "album_artist"}
	for p := 10; p+10 <= end; {
		id := string(b[p : p+4])
		if id[0] == 0 {
			break
		}
		n := int(binary.BigEndian.Uint32(b[p+4 : p+8]))
		if ver >= 4 {
			n = syncsafe(b[p+4 : p+8])
		}
		body := b[p+10 : min(p+10+n, end)]
		p += 10 + n
		if name, ok := names[id]; ok && len(body) > 1 {
			if v := id3Text(body[0], body[1:]); v != "" {
				f.Tags[name] = v
			}
		}
		if id == "APIC" && pic != nil && *pic == nil && len(body) > 4 {
			// encoding, MIME\0, picture type, description\0, data
			enc := body[0]
			rest := body[1:]
			i := bytes.IndexByte(rest, 0)
			if i < 0 || i+2 > len(rest) {
				continue
			}
			rest = rest[i+2:]
			rest = skipID3String(enc, rest)
			*pic = rest
			f.Cover = true
		}
	}
	return end
}

func syncsafe(b []byte) int {
	return int(b[0]&0x7f)<<21 | int(b[1]&0x7f)<<14 | int(b[2]&0x7f)<<7 | int(b[3]&0x7f)
}

// id3Text decodes a text frame's body in its encoding.
func id3Text(enc byte, b []byte) string {
	var s string
	switch enc {
	case 1, 2: // UTF-16 with a byte order mark, or big endian
		be := enc == 2
		if len(b) >= 2 && b[0] == 0xfe && b[1] == 0xff {
			be, b = true, b[2:]
		} else if len(b) >= 2 && b[0] == 0xff && b[1] == 0xfe {
			be, b = false, b[2:]
		}
		u := make([]uint16, 0, len(b)/2)
		for i := 0; i+1 < len(b); i += 2 {
			if be {
				u = append(u, uint16(b[i])<<8|uint16(b[i+1]))
			} else {
				u = append(u, uint16(b[i+1])<<8|uint16(b[i]))
			}
		}
		s = string(utf16.Decode(u))
	case 0: // Latin-1
		r := make([]rune, len(b))
		for i, c := range b {
			r[i] = rune(c)
		}
		s = string(r)
	default:
		s = string(b)
	}
	return strings.TrimSpace(strings.TrimRight(strings.ToValidUTF8(s, ""), "\x00"))
}

// skipID3String skips a NUL-ended string in its encoding.
func skipID3String(enc byte, b []byte) []byte {
	if enc == 1 || enc == 2 {
		for i := 0; i+1 < len(b); i += 2 {
			if b[i] == 0 && b[i+1] == 0 {
				return b[i+2:]
			}
		}
		return nil
	}
	if i := bytes.IndexByte(b, 0); i >= 0 {
		return b[i+1:]
	}
	return nil
}

// mp3Duration guesses an MP3's length from its first frame's bit rate, which
// holds for a file of one rate.
func mp3Duration(b []byte, size int64, f *Facts) {
	rates := [16]int{0, 32, 40, 48, 56, 64, 80, 96, 112, 128, 160, 192, 224, 256, 320, 0}
	srates := [4]int{44100, 48000, 32000, 0}
	for i := 0; i+4 <= len(b) && i < 64<<10; i++ {
		if b[i] != 0xff || b[i+1]&0xfe != 0xfa { // MPEG-1 Layer III
			continue
		}
		br := rates[b[i+2]>>4] * 1000
		sr := srates[(b[i+2]>>2)&3]
		if br == 0 || sr == 0 {
			continue
		}
		f.AudioCodec, f.BitRate, f.SampleRate = "mp3", int64(br), sr
		f.Channels = 2
		if b[i+3]>>6 == 3 {
			f.Channels = 1
		}
		f.DurationMs = (size - int64(i)) * 8 * 1000 / int64(br)
		return
	}
}

// flacTags reads a FLAC's STREAMINFO, VORBIS_COMMENT and PICTURE blocks.
func flacTags(b []byte, f *Facts, pic *[]byte) {
	f.AudioCodec = "flac"
	for p := 4; p+4 <= len(b); {
		last := b[p]&0x80 != 0
		typ := b[p] & 0x7f
		n := int(b[p+1])<<16 | int(b[p+2])<<8 | int(b[p+3])
		body := b[p+4 : min(p+4+n, len(b))]
		p += 4 + n
		switch typ {
		case 0:
			if len(body) >= 18 {
				sr := int(body[10])<<12 | int(body[11])<<4 | int(body[12])>>4
				ch := int(body[12]>>1&7) + 1
				total := int64(body[13]&0x0f)<<32 | int64(binary.BigEndian.Uint32(body[14:18]))
				f.SampleRate, f.Channels = sr, ch
				if sr > 0 {
					f.DurationMs = total * 1000 / int64(sr)
				}
			}
		case 4:
			vorbisComments(body, f)
		case 6:
			if pic != nil && *pic == nil && len(body) > 32 {
				q := 4
				ml := int(binary.BigEndian.Uint32(body[q:]))
				q += 4 + ml
				if q+4 > len(body) {
					break
				}
				dl := int(binary.BigEndian.Uint32(body[q:]))
				q += 4 + dl + 16
				if q+4 > len(body) {
					break
				}
				l := int(binary.BigEndian.Uint32(body[q:]))
				q += 4
				*pic = body[q:min(q+l, len(body))]
				f.Cover = true
			} else if len(body) > 0 {
				f.Cover = true
			}
		}
		if last {
			break
		}
	}
}

// vorbisComments reads Vorbis comments: NAME=value, little endian lengths.
func vorbisComments(b []byte, f *Facts) {
	if len(b) < 8 {
		return
	}
	vl := int(binary.LittleEndian.Uint32(b))
	p := 4 + vl
	if p+4 > len(b) {
		return
	}
	n := int(binary.LittleEndian.Uint32(b[p:]))
	p += 4
	for k := 0; k < n && p+4 <= len(b); k++ {
		l := int(binary.LittleEndian.Uint32(b[p:]))
		p += 4
		if p+l > len(b) {
			return
		}
		if name, val, ok := strings.Cut(string(b[p:p+l]), "="); ok {
			name = strings.ToLower(name)
			if name == "tracknumber" {
				name = "track"
			}
			f.Tags[name] = strings.ToValidUTF8(val, "")
		}
		p += l
	}
}

// wavFacts reads a WAV's fmt chunk and the length of its data.
func wavFacts(b []byte, f *Facts) {
	var byteRate int64
	for p := 12; p+8 <= len(b); {
		id := string(b[p : p+4])
		n := int(binary.LittleEndian.Uint32(b[p+4:]))
		body := b[p+8 : min(p+8+n, len(b))]
		switch id {
		case "fmt ":
			if len(body) >= 16 {
				f.AudioCodec = "pcm"
				f.Channels = int(binary.LittleEndian.Uint16(body[2:]))
				f.SampleRate = int(binary.LittleEndian.Uint32(body[4:]))
				byteRate = int64(binary.LittleEndian.Uint32(body[8:]))
				f.BitRate = byteRate * 8
			}
		case "data":
			if byteRate > 0 {
				f.DurationMs = int64(n) * 1000 / byteRate
			}
			return
		}
		p += 8 + n + n%2
	}
}
