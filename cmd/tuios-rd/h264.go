//go:build h264 && (linux || darwin)

package main

// The H.264 decoder: the system openh264, loaded at run time with purego.
// No cgo. The ISVCDecoder is a C++ object whose first word points at a
// table of function pointers; we call through that table.

import (
	"errors"
	"fmt"
	"os"
	"runtime"
	"unsafe"

	"github.com/ebitengine/purego"
)

var errNoOpenH264 = errors.New(`tuios-rd cannot load openh264 (libopenh264).
Install the openh264 package, then start tuios-rd again:
  Arch Linux:       pacman -S openh264
  Debian or Ubuntu: apt install libopenh264-8
  Fedora:           dnf install openh264
  macOS:            brew install openh264
Set SRTERM_OPENH264 to the library path when it is in another folder.`)

const haveH264 = true

func getenv(k string) string { return os.Getenv(k) }

type openh264 struct {
	create, destroy uintptr
}

func libCandidates() []string {
	var c []string
	if p := getenv("SRTERM_OPENH264"); p != "" {
		c = append(c, p)
	}
	switch runtime.GOOS {
	case "darwin":
		for _, dir := range []string{"", "/opt/homebrew/lib/", "/usr/local/lib/"} {
			c = append(c, dir+"libopenh264.dylib", dir+"libopenh264.8.dylib", dir+"libopenh264.7.dylib")
		}
	default:
		c = append(c, "libopenh264.so.8", "libopenh264.so.7", "libopenh264.so.6", "libopenh264.so")
	}
	return c
}

func loadOpenH264() (*openh264, error) {
	for _, name := range libCandidates() {
		h, err := purego.Dlopen(name, purego.RTLD_NOW|purego.RTLD_LOCAL)
		if err != nil {
			continue
		}
		cr, err1 := purego.Dlsym(h, "WelsCreateDecoder")
		de, err2 := purego.Dlsym(h, "WelsDestroyDecoder")
		if err1 != nil || err2 != nil {
			continue
		}
		return &openh264{create: cr, destroy: de}, nil
	}
	return nil, errNoOpenH264
}

// vtable slots of ISVCDecoderVtbl (wels/codec_api.h).
const (
	vtInitialize         = 0
	vtUninitialize       = 1
	vtDecodeFrameNoDelay = 3
	vtSetOption          = 8
)

// sBufferInfo mirrors SBufferInfo on 64-bit targets (72 bytes).
type sBufferInfo struct {
	status    int32
	_         int32
	inTS      uint64
	outTS     uint64
	w, h, fmt int32
	stride    [2]int32
	_         int32
	dst       [3]uintptr
}

type decoder struct {
	lib *openh264
	obj uintptr // ISVCDecoder*
	vt  *[10]uintptr
}

type picture struct {
	y, u, v []byte
	ys, uvs int
	w, h    int
}

func (l *openh264) newDecoder() (*decoder, error) {
	var obj uintptr
	r, _, _ := purego.SyscallN(l.create, uintptr(unsafe.Pointer(&obj)))
	if int32(r) != 0 || obj == 0 {
		return nil, errors.New("openh264: WelsCreateDecoder failed")
	}
	d := &decoder{lib: l, obj: obj, vt: *(**[10]uintptr)(unsafe.Pointer(obj))}
	// SDecodingParam is 32 bytes. All zero means: AVC bitstream, error
	// concealment off, no parse-only mode.
	var param [32]byte
	r, _, _ = purego.SyscallN(d.vt[vtInitialize], obj, uintptr(unsafe.Pointer(&param[0])))
	if int32(r) != 0 {
		d.close()
		return nil, fmt.Errorf("openh264: Initialize returned %d", int32(r))
	}
	return d, nil
}

func (d *decoder) close() {
	if d.obj == 0 {
		return
	}
	purego.SyscallN(d.vt[vtUninitialize], d.obj)
	purego.SyscallN(d.lib.destroy, d.obj)
	d.obj = 0
}

// decode feeds one Annex B access unit. The picture points into the
// decoder's memory and stays valid until the next call.
func (d *decoder) decode(au []byte) (picture, bool, error) {
	if len(au) == 0 {
		return picture{}, false, nil
	}
	var planes [3]uintptr
	var info sBufferInfo
	r, _, _ := purego.SyscallN(d.vt[vtDecodeFrameNoDelay], d.obj,
		uintptr(unsafe.Pointer(&au[0])), uintptr(len(au)),
		uintptr(unsafe.Pointer(&planes[0])), uintptr(unsafe.Pointer(&info)))
	runtime.KeepAlive(au)
	if int32(r) != 0 {
		return picture{}, false, fmt.Errorf("openh264: decode state %#x", int32(r))
	}
	if info.status != 1 || planes[0] == 0 {
		return picture{}, false, nil
	}
	w, h := int(info.w), int(info.h)
	ys, uvs := int(info.stride[0]), int(info.stride[1])
	ch := (h + 1) / 2
	p := picture{
		y:  unsafe.Slice((*byte)(unsafe.Pointer(planes[0])), ys*h),
		u:  unsafe.Slice((*byte)(unsafe.Pointer(planes[1])), uvs*ch),
		v:  unsafe.Slice((*byte)(unsafe.Pointer(planes[2])), uvs*ch),
		ys: ys, uvs: uvs, w: w, h: h,
	}
	return p, true, nil
}
