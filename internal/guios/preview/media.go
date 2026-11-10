package preview

import (
	"bytes"
	"context"
	"errors"
	"image"
	_ "image/jpeg" // ffmpeg's frames
	"math"
	"strconv"
	"strings"

	"github.com/Gaurav-Gosain/tuios/internal/jsonx"
)

// Films and sound: ffprobe says what a file holds, and ffmpeg reads one
// frame at a time, which is all a preview shows; nothing plays. A film's
// frame is read at a key frame first, which needs no decode of the frames
// before it, so a seek costs one decode wherever it lands.

// Facts are what a film or a sound file holds.
type Facts struct {
	DurationMs int64
	W, H       int
	FPS        float64
	VideoCodec string
	AudioCodec string
	SampleRate int
	Channels   int
	BitRate    int64
	// Cover tells that a sound file holds a picture, read with Frame.
	Cover bool
	// Tags are the file's tags, by lower case name: title, artist,
	// album, date, track, genre.
	Tags map[string]string
	// Engine names what read them.
	Engine string
}

// nativeFacts and nativeFrame read a film with the platform's framework:
// AVFoundation on macOS, set there when it is built in.
var (
	nativeFacts func(ctx context.Context, path string) (Facts, error)
	nativeFrame func(ctx context.Context, path string, atMs int64, maxPx int) (image.Image, error)
)

// The film containers and the protocol ffmpeg may read: the open file and
// no other, so a playlist named .mp4 cannot make it read other files.
const (
	mediaProtocols = "file"
	mediaFormats   = "mov,mp4,m4a,3gp,3g2,mj2,matroska,webm,avi,mp3,flac,ogg,wav,aiff,aac,asf"
)

// MediaFacts reads what a film or sound file holds: with the platform's
// framework where there is one (it reads in process, in milliseconds), and
// ffprobe for what it does not read, as a Matroska film.
func MediaFacts(ctx context.Context, path string) (Facts, error) {
	if nativeFacts != nil {
		if f, err := nativeFacts(ctx, path); err == nil {
			// AVFoundation leaves out tags an ID3 or FLAC tag holds, as
			// an MP3's year: the file's own tags fill them in.
			if ByName(path) == Audio {
				if t, ok := tagFacts(path); ok {
					for k, v := range t.Tags {
						if f.Tags[k] == "" {
							f.Tags[k] = v
						}
					}
				}
			}
			return f, nil
		}
	}
	out, err := run(ctx, 4<<20, "ffprobe", "-v", "error", "-protocol_whitelist", mediaProtocols,
		"-show_entries", "format=duration,bit_rate:format_tags:stream=codec_type,codec_name,width,height,avg_frame_rate,sample_rate,channels:stream_disposition=attached_pic",
		"-of", "json", path)
	if err != nil {
		var nt ErrNoTool
		if errors.As(err, &nt) {
			if f, ok := tagFacts(path); ok {
				return f, nil
			}
		}
		return Facts{}, err
	}
	var probe struct {
		Format struct {
			Duration string            `json:"duration"`
			BitRate  string            `json:"bit_rate"`
			Tags     map[string]string `json:"tags"`
		} `json:"format"`
		Streams []struct {
			Type        string `json:"codec_type"`
			Name        string `json:"codec_name"`
			Width       int    `json:"width"`
			Height      int    `json:"height"`
			Rate        string `json:"avg_frame_rate"`
			SampleRate  string `json:"sample_rate"`
			Channels    int    `json:"channels"`
			Disposition struct {
				AttachedPic int `json:"attached_pic"`
			} `json:"disposition"`
		} `json:"streams"`
	}
	if err := jsonx.Unmarshal(out, &probe); err != nil {
		return Facts{}, ErrUnreadable
	}
	f := Facts{Engine: "ffprobe", Tags: map[string]string{}}
	for k, v := range probe.Format.Tags {
		f.Tags[strings.ToLower(k)] = v
	}
	for _, s := range probe.Streams {
		switch {
		case s.Type == "video" && s.Disposition.AttachedPic == 1:
			f.Cover = true
		case s.Type == "video" && f.VideoCodec == "":
			f.VideoCodec, f.W, f.H = s.Name, s.Width, s.Height
			if num, den, ok := strings.Cut(s.Rate, "/"); ok {
				n, _ := strconv.ParseFloat(num, 64)
				d, _ := strconv.ParseFloat(den, 64)
				if d > 0 && n > 0 {
					f.FPS = math.Round(n/d*100) / 100
				}
			}
		case s.Type == "audio" && f.AudioCodec == "":
			f.AudioCodec, f.Channels = s.Name, s.Channels
			f.SampleRate, _ = strconv.Atoi(s.SampleRate)
		}
	}
	if sec, err := strconv.ParseFloat(probe.Format.Duration, 64); err == nil {
		f.DurationMs = int64(sec * 1000)
	}
	f.BitRate, _ = strconv.ParseInt(probe.Format.BitRate, 10, 64)
	return f, nil
}

// Frame reads the film's frame at atMs, at most maxPx on its long side, or a
// sound file's cover.
func Frame(ctx context.Context, path string, atMs int64, maxPx int) (image.Image, error) {
	if maxPx <= 0 || maxPx > MaxPx {
		maxPx = 1600
	}
	if nativeFrame != nil {
		if img, err := nativeFrame(ctx, path, atMs, maxPx); err == nil {
			return img, nil
		}
	}
	if Tool("ffmpeg") == "" {
		if img, ok := tagCover(path, maxPx); ok {
			return img, nil
		}
		return nil, ErrNoTool("ffmpeg")
	}
	// Key frames first. A film with few has none past this point, so the
	// next try decodes. The last frame of a film starts before its end,
	// so the last try looks a second back.
	for _, try := range []struct {
		keys bool
		back int64
	}{{true, 0}, {false, 0}, {false, 1000}} {
		img, err := ffmpegFrame(ctx, path, max(atMs-try.back, 0), maxPx, try.keys)
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if err == nil {
			return img, nil
		}
	}
	return nil, ErrNoFrame
}

func ffmpegFrame(ctx context.Context, path string, atMs int64, maxPx int, keys bool) (image.Image, error) {
	args := []string{"-v", "error", "-nostdin", "-protocol_whitelist", mediaProtocols}
	if atMs > 0 {
		args = append(args, "-ss", strconv.FormatFloat(float64(atMs)/1000, 'f', 3, 64))
	}
	if keys {
		args = append(args, "-skip_frame", "nokey")
	}
	scale := "scale='min(" + itoa(maxPx) + ",iw)':'min(" + itoa(maxPx) + ",ih)':force_original_aspect_ratio=decrease"
	args = append(args, "-i", path, "-map", "0:v:0", "-frames:v", "1", "-vf", scale, "-f", "image2pipe", "-vcodec", "mjpeg", "-q:v", "3", "-")
	out, err := run(ctx, 64<<20, "ffmpeg", args...)
	if err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return nil, errors.New("no frame")
	}
	img, _, err := image.Decode(bytes.NewReader(out))
	return img, err
}
