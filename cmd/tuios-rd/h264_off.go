//go:build !h264 && (linux || darwin)

package main

import "errors"

// This build shows tiles only. The engine that tuios remote-desktop installs
// sends tiles, so the H.264 decoder (openh264 through purego) stays out of
// the binary. Build with -tags h264 to get it back.

const haveH264 = false

type openh264 struct{}

type picture struct {
	y, u, v []byte
	ys, uvs int
	w, h    int
}

type decoder struct{}

func loadOpenH264() (*openh264, error) { return nil, nil }

var errNoH264 = errors.New("this build of tuios-rd shows tiles only, and the engine sent H.264. Update the engine on the host with tuios remote-desktop")

func (l *openh264) newDecoder() (*decoder, error) { return nil, errNoH264 }

func (d *decoder) close() {}

func (d *decoder) decode([]byte) (picture, bool, error) { return picture{}, false, errNoH264 }
