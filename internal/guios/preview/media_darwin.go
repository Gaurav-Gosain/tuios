//go:build darwin

package preview

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"math"
	"runtime"
	"strings"
	"sync"
	"unsafe"

	"github.com/ebitengine/purego"
	"github.com/ebitengine/purego/objc"
)

// AVFoundation reads films and sound on macOS when this Mac has no ffmpeg:
// the facts of an asset, its frame at a time, a sound file's cover. The
// package's ffprobe and ffmpeg paths go first where they are installed,
// since they read more containers; most Macs have neither.

func init() {
	nativeFacts = avFacts
	nativeFrame = avFrame
}

// cmTime is a CMTime: 24 bytes, which purego passes and returns through
// memory as the C ABI does for a struct of its size.
type cmTime struct {
	Value     int64
	Timescale int32
	Flags     uint32
	Epoch     int64
}

// cgSize is a CGSize: two doubles in float registers.
type cgSize struct{ W, H float64 }

var av struct {
	once sync.Once
	err  error

	poolPush func() uintptr
	poolPop  func(pool uintptr)

	CMTimeGetSeconds                                  func(t cmTime) float64
	CMTimeMakeWithSeconds                             func(sec float64, scale int32) cmTime
	CMFormatDescriptionGetMediaSubType                func(desc uintptr) uint32
	CMAudioFormatDescriptionGetStreamBasicDescription func(desc uintptr) uintptr

	mediaVideo, mediaAudio objc.ID
	urlAsset, generator    objc.Class

	selURLAssetWithURL, selDuration, selTracksWithMediaType, selCount, selObjectAtIndex,
	selNaturalSize, selPreferredTransform, selNominalFrameRate, selFormatDescriptions,
	selCommonMetadata, selCommonKey, selStringValue, selDataValue, selUTF8String, selBytes,
	selLength, selGeneratorWithAsset, selSetAppliesTransform, selSetMaximumSize,
	selCopyCGImage, selEstimatedDataRate objc.SEL
}

// loadAV opens AVFoundation and CoreMedia once.
func loadAV() error {
	av.once.Do(func() {
		defer func() {
			if r := recover(); r != nil {
				av.err = fmt.Errorf("AVFoundation: %v", r)
			}
		}()
		if err := loadCG(); err != nil {
			panic(err)
		}
		open := func(path string) uintptr {
			h, err := purego.Dlopen(path, purego.RTLD_NOW|purego.RTLD_GLOBAL)
			if err != nil {
				panic(err)
			}
			return h
		}
		lobjc := open("/usr/lib/libobjc.A.dylib")
		cm := open("/System/Library/Frameworks/CoreMedia.framework/CoreMedia")
		avf := open("/System/Library/Frameworks/AVFoundation.framework/AVFoundation")
		purego.RegisterLibFunc(&av.poolPush, lobjc, "objc_autoreleasePoolPush")
		purego.RegisterLibFunc(&av.poolPop, lobjc, "objc_autoreleasePoolPop")
		purego.RegisterLibFunc(&av.CMTimeGetSeconds, cm, "CMTimeGetSeconds")
		purego.RegisterLibFunc(&av.CMTimeMakeWithSeconds, cm, "CMTimeMakeWithSeconds")
		purego.RegisterLibFunc(&av.CMFormatDescriptionGetMediaSubType, cm, "CMFormatDescriptionGetMediaSubType")
		purego.RegisterLibFunc(&av.CMAudioFormatDescriptionGetStreamBasicDescription, cm, "CMAudioFormatDescriptionGetStreamBasicDescription")
		global := func(name string) objc.ID {
			p, err := purego.Dlsym(avf, name)
			if err != nil {
				panic(err)
			}
			return objc.ID(**(**uintptr)(unsafe.Pointer(&p)))
		}
		av.mediaVideo = global("AVMediaTypeVideo")
		av.mediaAudio = global("AVMediaTypeAudio")
		av.urlAsset = objc.GetClass("AVURLAsset")
		av.generator = objc.GetClass("AVAssetImageGenerator")
		if av.urlAsset == 0 || av.generator == 0 {
			panic("no AVURLAsset or AVAssetImageGenerator class")
		}
		s := objc.RegisterName
		av.selURLAssetWithURL = s("URLAssetWithURL:options:")
		av.selDuration = s("duration")
		av.selTracksWithMediaType = s("tracksWithMediaType:")
		av.selCount = s("count")
		av.selObjectAtIndex = s("objectAtIndex:")
		av.selNaturalSize = s("naturalSize")
		av.selPreferredTransform = s("preferredTransform")
		av.selNominalFrameRate = s("nominalFrameRate")
		av.selEstimatedDataRate = s("estimatedDataRate")
		av.selFormatDescriptions = s("formatDescriptions")
		av.selCommonMetadata = s("commonMetadata")
		av.selCommonKey = s("commonKey")
		av.selStringValue = s("stringValue")
		av.selDataValue = s("dataValue")
		av.selUTF8String = s("UTF8String")
		av.selBytes = s("bytes")
		av.selLength = s("length")
		av.selGeneratorWithAsset = s("assetImageGeneratorWithAsset:")
		av.selSetAppliesTransform = s("setAppliesPreferredTrackTransform:")
		av.selSetMaximumSize = s("setMaximumSize:")
		av.selCopyCGImage = s("copyCGImageAtTime:actualTime:error:")
	})
	return av.err
}

// withPool runs fn on one thread inside an autorelease pool: the objects
// AVFoundation hands back autoreleased go when fn ends, not never, as they
// would on a goroutine's thread with no pool.
func withPool(fn func()) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	pool := av.poolPush()
	defer av.poolPop(pool)
	fn()
}

// asset opens a file as an AVURLAsset, autoreleased.
func asset(path string) (objc.ID, error) {
	u, err := cfURL(path)
	if err != nil {
		return 0, err
	}
	defer cg.CFRelease(u)
	a := objc.ID(av.urlAsset).Send(av.selURLAssetWithURL, objc.ID(u), objc.ID(0))
	if a == 0 {
		return 0, ErrUnreadable
	}
	return a, nil
}

// firstTrack is an asset's first track of a media type, 0 for none.
func firstTrack(a objc.ID, media objc.ID) objc.ID {
	tracks := a.Send(av.selTracksWithMediaType, media)
	if tracks == 0 || objc.Send[uint](tracks, av.selCount) == 0 {
		return 0
	}
	return tracks.Send(av.selObjectAtIndex, uint(0))
}

// firstFormat is a track's first format description, 0 for none.
func firstFormat(track objc.ID) uintptr {
	descs := track.Send(av.selFormatDescriptions)
	if descs == 0 || objc.Send[uint](descs, av.selCount) == 0 {
		return 0
	}
	return uintptr(descs.Send(av.selObjectAtIndex, uint(0)))
}

// goString reads an NSString.
func goString(s objc.ID) string {
	if s == 0 {
		return ""
	}
	p := uintptr(s.Send(av.selUTF8String))
	if p == 0 {
		return ""
	}
	ptr := *(*unsafe.Pointer)(unsafe.Pointer(&p))
	n := 0
	for *(*byte)(unsafe.Add(ptr, n)) != 0 && n < 1<<16 {
		n++
	}
	return strings.ToValidUTF8(string(unsafe.Slice((*byte)(ptr), n)), "")
}

// goBytes copies an NSData's bytes.
func goBytes(d objc.ID) []byte {
	if d == 0 {
		return nil
	}
	n := objc.Send[uint](d, av.selLength)
	p := uintptr(d.Send(av.selBytes))
	if p == 0 || n == 0 || n > 64<<20 {
		return nil
	}
	ptr := *(*unsafe.Pointer)(unsafe.Pointer(&p))
	return bytes.Clone(unsafe.Slice((*byte)(ptr), n))
}

// fourCC names a codec's four-character code as ffprobe names it.
func fourCC(code uint32) string {
	b := []byte{byte(code >> 24), byte(code >> 16), byte(code >> 8), byte(code)}
	s := strings.TrimSpace(string(b))
	switch s {
	case "avc1", "avc3":
		return "h264"
	case "hvc1", "hev1":
		return "hevc"
	case "av01":
		return "av1"
	case "vp09":
		return "vp9"
	case "aac", "mp4a":
		return "aac"
	case ".mp3":
		return "mp3"
	case "alac":
		return "alac"
	case "flac":
		return "flac"
	case "lpcm":
		return "pcm"
	case "ac-3":
		return "ac3"
	case "opus":
		return "opus"
	}
	return s
}

// commonKeys maps AVFoundation's common metadata keys to the tags' names.
var commonKeys = map[string]string{"title": "title", "artist": "artist", "albumName": "album", "creationDate": "date", "type": "genre", "creator": "artist"}

func avFacts(ctx context.Context, path string) (Facts, error) {
	if err := loadAV(); err != nil {
		return Facts{}, err
	}
	if ctx.Err() != nil {
		return Facts{}, ctx.Err()
	}
	var f Facts
	var err error
	withPool(func() {
		var a objc.ID
		if a, err = asset(path); err != nil {
			return
		}
		f = Facts{Engine: "AVFoundation", Tags: map[string]string{}}
		if sec := av.CMTimeGetSeconds(objc.Send[cmTime](a, av.selDuration)); sec > 0 && !math.IsNaN(sec) && !math.IsInf(sec, 0) {
			f.DurationMs = int64(sec * 1000)
		}
		if v := firstTrack(a, av.mediaVideo); v != 0 {
			size := objc.Send[cgSize](v, av.selNaturalSize)
			t := objc.Send[cgAffine](v, av.selPreferredTransform)
			f.W, f.H = int(math.Round(size.W)), int(math.Round(size.H))
			if math.Abs(t.A) < 0.5 && math.Abs(t.B) > 0.5 {
				f.W, f.H = f.H, f.W // a phone's film, turned
			}
			f.FPS = math.Round(float64(objc.Send[float32](v, av.selNominalFrameRate))*100) / 100
			if d := firstFormat(v); d != 0 {
				f.VideoCodec = fourCC(av.CMFormatDescriptionGetMediaSubType(d))
			}
		}
		if s := firstTrack(a, av.mediaAudio); s != 0 {
			if d := firstFormat(s); d != 0 {
				f.AudioCodec = fourCC(av.CMFormatDescriptionGetMediaSubType(d))
				if p := av.CMAudioFormatDescriptionGetStreamBasicDescription(d); p != 0 {
					// AudioStreamBasicDescription: mSampleRate first, a
					// double; mChannelsPerFrame at byte 28.
					ptr := *(*unsafe.Pointer)(unsafe.Pointer(&p))
					f.SampleRate = int(*(*float64)(ptr))
					f.Channels = int(*(*uint32)(unsafe.Add(ptr, 28)))
				}
			}
			f.BitRate = int64(objc.Send[float32](s, av.selEstimatedDataRate))
		}
		if items := a.Send(av.selCommonMetadata); items != 0 {
			n := objc.Send[uint](items, av.selCount)
			for i := uint(0); i < n && i < 256; i++ {
				item := items.Send(av.selObjectAtIndex, i)
				key := goString(item.Send(av.selCommonKey))
				if key == "artwork" {
					f.Cover = true
					continue
				}
				if name, ok := commonKeys[key]; ok {
					if v := goString(item.Send(av.selStringValue)); v != "" {
						f.Tags[name] = v
					}
				}
			}
		}
	})
	if err != nil {
		return Facts{}, err
	}
	if f.DurationMs == 0 && f.VideoCodec == "" && f.AudioCodec == "" {
		return Facts{}, ErrUnreadable
	}
	return f, nil
}

func avFrame(ctx context.Context, path string, atMs int64, maxPx int) (image.Image, error) {
	if err := loadAV(); err != nil {
		return nil, err
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	var out image.Image
	var err error
	withPool(func() {
		var a objc.ID
		if a, err = asset(path); err != nil {
			return
		}
		if firstTrack(a, av.mediaVideo) == 0 {
			out, err = avCover(a, maxPx)
			return
		}
		gen := objc.ID(av.generator).Send(av.selGeneratorWithAsset, a)
		if gen == 0 {
			err = ErrNoFrame
			return
		}
		gen.Send(av.selSetAppliesTransform, true)
		gen.Send(av.selSetMaximumSize, cgSize{float64(maxPx), float64(maxPx)})
		// The default tolerance takes the nearest frame that decodes
		// fast, a key frame, as the ffmpeg path does. The last frame of
		// a film starts before its end, so a miss there looks a second
		// back.
		for _, back := range []int64{0, 1000} {
			at := av.CMTimeMakeWithSeconds(float64(max(atMs-back, 0))/1000, 600)
			var nsErr objc.ID
			img := uintptr(gen.Send(av.selCopyCGImage, at, uintptr(0), unsafe.Pointer(&nsErr)))
			if img == 0 {
				continue
			}
			out, err = drawCGImage(img)
			cg.CGImageRelease(img)
			return
		}
		err = ErrNoFrame
	})
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	return out, err
}

// avCover is a sound file's artwork, decoded and fitted.
func avCover(a objc.ID, maxPx int) (image.Image, error) {
	items := a.Send(av.selCommonMetadata)
	if items == 0 {
		return nil, ErrNoFrame
	}
	n := objc.Send[uint](items, av.selCount)
	for i := uint(0); i < n && i < 256; i++ {
		item := items.Send(av.selObjectAtIndex, i)
		if goString(item.Send(av.selCommonKey)) != "artwork" {
			continue
		}
		data := goBytes(item.Send(av.selDataValue))
		if data == nil {
			continue
		}
		img, _, err := image.Decode(bytes.NewReader(data))
		if err != nil {
			continue
		}
		return Fit(img, maxPx), nil
	}
	return nil, ErrNoFrame
}

// drawCGImage copies a CGImage into an image.RGBA.
func drawCGImage(img uintptr) (image.Image, error) {
	w, h := int(cg.CGImageGetWidth(img)), int(cg.CGImageGetHeight(img))
	if w <= 0 || h <= 0 {
		return nil, ErrNoFrame
	}
	bm, err := newBitmap(w, h)
	if err != nil {
		return nil, err
	}
	defer bm.release()
	cg.CGContextDrawImage(bm.ctx, cgRect{0, 0, float64(w), float64(h)}, img)
	return bm.image(), nil
}
