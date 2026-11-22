//go:build linux || darwin

package main

import (
	"context"
	"encoding/binary"
	"errors"
	"net/url"
	"strings"
	"time"

	"github.com/coder/websocket"
)

// firstTilesFrame opens a watching stream with the tiles codec and returns
// the size of the first tiles frame: proof that the engine captures and the
// whole path carries frames. It sends no input.
func firstTilesFrame(ctx context.Context, base, token, output string) (int, int, error) {
	u := strings.Replace(base, "http", "ws", 1) + "/stream?view=1"
	if output != "" {
		u += "&output=" + url.QueryEscape(output)
	}
	u += "&codec=tiles"
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	c, _, err := websocket.Dial(ctx, u, &websocket.DialOptions{Subprotocols: []string{"nsl1", "bearer." + token}})
	if err != nil {
		return 0, 0, err
	}
	defer c.Close(websocket.StatusNormalClosure, "")
	c.SetReadLimit(64 << 20)
	for {
		_, m, err := c.Read(ctx)
		if err != nil {
			return 0, 0, err
		}
		// NSL1 header (20 bytes), then a tiles payload: version, flags, width
		// and height.
		if len(m) >= 26 && string(m[:4]) == "NSL1" && m[5] == 8 {
			w, h := int(binary.BigEndian.Uint16(m[22:])), int(binary.BigEndian.Uint16(m[24:]))
			if w == 0 || h == 0 {
				return 0, 0, errors.New("the engine sent an empty frame")
			}
			return w, h, nil
		}
	}
}
