package vt

import (
	"bytes"
	"encoding/base64"
	"errors"
	"strconv"
	"strings"
)

// ParseKittyCommand parses the body of a kitty graphics APC, the bytes after
// the leading 'G'. A payload that is not valid base64 is not an error here:
// the command is returned with PayloadErr set and no Data or FilePath, so the
// caller can still answer the guest and drop the transmission it was part of.
func ParseKittyCommand(data []byte) (*KittyCommand, error) {
	if len(data) == 0 {
		return nil, nil
	}

	cmd := &KittyCommand{
		Action: KittyActionTransmit,
		Medium: KittyMediumDirect,
		Format: KittyFormatRGBA,
	}

	controlPart, dataPart, _ := bytes.Cut(data, []byte{';'})

	if len(controlPart) > 0 {
		parseKittyControlParams(string(controlPart), cmd)
	}

	if len(dataPart) > 0 {
		// Always preserve raw payload for passthrough (avoids decode→re-encode cycle)
		cmd.RawPayload = string(dataPart)

		decoded, err := DecodeKittyPayload(dataPart)
		if err != nil {
			// Undecodable text is never handed on as image data: a guest that
			// sent it gets EINVAL (see KittyPayloadErrorResponse) and the
			// passthrough drops the transmission it belonged to.
			cmd.PayloadErr = err
		} else {
			switch cmd.Medium {
			case KittyMediumFile, KittyMediumTempFile, KittyMediumSharedMemory:
				cmd.FilePath = string(decoded)
			default:
				cmd.Data = decoded
			}
		}
	}

	return cmd, nil
}

// DecodeKittyPayload decodes a kitty graphics payload, padded or not.
//
// The protocol says the payload is base64 and leaves padding to the sender.
// kitten icat sends none: its Go encoder is RawStdEncoding, so the final chunk
// of a stream, and any file path whose length is not a multiple of three, has
// no '=' at the end. Only a chunk that is not the last has to be a multiple of
// four characters. Other senders (chafa, timg, mpv) pad. Both are accepted, and
// so is a payload built by joining padded groups, which is what a sender that
// pads every chunk produces if two chunks are ever sent as one.
//
// Each run of '=' ends one group. The text before it is decoded without
// padding, which accepts a group with its padding stripped and rejects one
// that was cut short (a single character left over), and the rest of the
// payload is decoded the same way.
func DecodeKittyPayload(payload []byte) ([]byte, error) {
	out := make([]byte, 0, base64.RawStdEncoding.DecodedLen(len(payload)))
	rest := payload
	for len(rest) > 0 {
		end := bytes.IndexByte(rest, '=')
		if end < 0 {
			end = len(rest)
		}
		group := rest[:end]
		rest = rest[end:]
		// The padding run, and any line break inside it, which a wrapped
		// encoding can put between two '='.
		pad, skip := 0, 0
		for skip < len(rest) && (rest[skip] == '=' || rest[skip] == '\n' || rest[skip] == '\r') {
			if rest[skip] == '=' {
				pad++
			}
			skip++
		}
		rest = rest[skip:]
		// Padding only ever completes a group of four, with one or two '='.
		// Anything else is not base64. Line breaks are not counted: the
		// decoder below skips them, as base64 does everywhere, and a shell
		// sender piping an image through base64 without -w 0 gets one every
		// 76 characters. Counted, they made a wrapped payload's padding look
		// misplaced whenever the breaks were not a multiple of four, and the
		// image was refused (FuzzKittyPayloadDecode).
		chars := len(group) - bytes.Count(group, []byte{'\n'}) - bytes.Count(group, []byte{'\r'})
		if pad > 0 && (pad > 2 || (chars+pad)%4 != 0) {
			return nil, errKittyPadding
		}
		// Decoded in place: out was sized for the whole payload, padding
		// included, so the groups always fit and a frame is copied once.
		n, err := base64.RawStdEncoding.Decode(out[len(out):cap(out)], group)
		if err != nil {
			return nil, err
		}
		out = out[:len(out)+n]
	}
	return out, nil
}

// errKittyPadding is a payload whose '=' padding cannot end a base64 group.
var errKittyPadding = errors.New("misplaced base64 padding")

// KittyPayloadErrorResponse is the reply owed to a guest whose command carried
// a payload that could not be decoded, or nil when none is owed.
//
// kitty answers an error unless the guest asked for silence with q=2, and only
// when the command names its image with i= or I=, because a reply without an
// id cannot be matched to anything. A query is the exception: it is answered
// with or without an id, the way tuios has always answered it, because a
// probing guest waits for that reply.
//
// A payload shaped like a reply ("EINVAL:...") is a guest echoing one back,
// usually a shell that received it as input. Answering that would echo again,
// forever, so it gets nothing.
func KittyPayloadErrorResponse(cmd *KittyCommand) []byte {
	if cmd == nil || cmd.PayloadErr == nil || cmd.Quiet >= 2 {
		return nil
	}
	if IsKittyEchoedResponse(cmd) {
		return nil
	}
	if cmd.Action != KittyActionQuery && cmd.ImageID == 0 && cmd.ImageNumber == 0 {
		return nil
	}
	return BuildKittyResponse(false, cmd.ImageID, "EINVAL:payload is not valid base64")
}

// IsKittyEchoedResponse reports whether a parsed graphics command is a
// terminal's reply coming back as input rather than a command from a guest.
//
// The payload shape alone is not enough. A chunk of real image data can be
// all capital letters behind a leading E: a run of dark pixels encodes to
// "EAAAAAAA", and the last chunk of a chunked transmission is often short
// enough to pass the length cap. Dropping it as an echo left the transmission
// without its m=0, so the image never appeared. A reply carries no key but
// the ids (i=, I=, p=), and every transmission carries at least one other key
// (a=, f=, s=, v=, t= on the first chunk, m= on every later one), so a command
// is an echo only when it has the reply's keys and the reply's payload.
func IsKittyEchoedResponse(cmd *KittyCommand) bool {
	return cmd != nil && !cmd.otherKeys && IsKittyResponsePayload(cmd.RawPayload)
}

// IsKittyResponsePayload reports whether a graphics payload looks like an
// echoed kitty protocol response rather than image data.
//
// It is matched against the raw wire payload (the base64 text between ';' and
// the APC terminator), not the decoded bytes. A real transmit payload is a
// base64 string; an echoed response is a short status token: "OK", or a POSIX
// error name optionally followed by a ":message" (e.g. "ENOENT", "EINVAL:bad
// params"). Matching the decoded bytes instead let arbitrary binary chunks (a
// chafa or mpv direct stream) collide with the 'E'+A-Z shape about 0.04% of
// the time and silently drop a chunk, corrupting the image.
//
// The shape required is ^(OK|E[A-Z]+(:.*)?)$ with a hard length cap so that a
// legitimate (necessarily longer, mixed-case) base64 payload cannot match.
func IsKittyResponsePayload(payload string) bool {
	if len(payload) == 0 || len(payload) > 256 {
		return false
	}
	if payload == "OK" {
		return true
	}
	// POSIX error name: 'E' followed by one or more uppercase letters, then an
	// optional ":<message>". A base64 image payload is not all-uppercase.
	if payload[0] != 'E' {
		return false
	}
	i := 1
	for i < len(payload) && payload[i] >= 'A' && payload[i] <= 'Z' {
		i++
	}
	if i < 2 {
		// Need at least one uppercase letter after the leading 'E'.
		return false
	}
	if i == len(payload) {
		return true
	}
	return payload[i] == ':'
}

func parseKittyControlParams(control string, cmd *KittyCommand) {
	for pair := range strings.SplitSeq(control, ",") {
		if pair == "" {
			continue
		}
		key, value, ok := strings.Cut(pair, "=")
		if !ok {
			continue
		}
		if key != "i" && key != "I" && key != "p" {
			cmd.otherKeys = true
		}

		switch key {
		case "a":
			if len(value) > 0 {
				cmd.Action = KittyGraphicsAction(value[0])
			}
		case "q":
			cmd.Quiet, _ = strconv.Atoi(value)
		case "i":
			if v, err := strconv.ParseUint(value, 10, 32); err == nil {
				cmd.ImageID = uint32(v)
			}
		case "I":
			if v, err := strconv.ParseUint(value, 10, 32); err == nil {
				cmd.ImageNumber = uint32(v)
			}
		case "p":
			if v, err := strconv.ParseUint(value, 10, 32); err == nil {
				cmd.PlacementID = uint32(v)
			}
		case "f":
			if v, err := strconv.Atoi(value); err == nil {
				cmd.Format = KittyGraphicsFormat(v)
			}
		case "t":
			if len(value) > 0 {
				cmd.Medium = KittyGraphicsMedium(value[0])
			}
		case "o":
			if len(value) > 0 {
				if value[0] == 'z' {
					cmd.Compression = KittyCompressionZlib
				}
			}
		case "s":
			cmd.Width, _ = strconv.Atoi(value)
		case "v":
			cmd.Height, _ = strconv.Atoi(value)
		case "S":
			cmd.Size, _ = strconv.Atoi(value)
		case "O":
			cmd.Offset, _ = strconv.Atoi(value)
		case "m":
			cmd.More = value == "1"
		case "d":
			if len(value) > 0 {
				cmd.Delete = KittyDeleteTarget(value[0])
			}
		case "x":
			cmd.SourceX, _ = strconv.Atoi(value)
		case "y":
			cmd.SourceY, _ = strconv.Atoi(value)
		case "w":
			cmd.SourceWidth, _ = strconv.Atoi(value)
		case "h":
			cmd.SourceHeight, _ = strconv.Atoi(value)
		case "X":
			cmd.XOffset, _ = strconv.Atoi(value)
		case "Y":
			cmd.YOffset, _ = strconv.Atoi(value)
			if v, err := strconv.ParseUint(value, 10, 32); err == nil {
				cmd.BackgroundColor = uint32(v)
			}
		case "c":
			cmd.Columns, _ = strconv.Atoi(value)
		case "r":
			cmd.Rows, _ = strconv.Atoi(value)
		case "z":
			if v, err := strconv.ParseInt(value, 10, 32); err == nil {
				cmd.ZIndex = int32(v)
			}
		case "C":
			cmd.CursorMove, _ = strconv.Atoi(value)
		case "U":
			cmd.Virtual = value == "1"
		}
	}
}

func BuildKittyResponse(ok bool, imageID uint32, message string) []byte {
	var buf bytes.Buffer
	buf.WriteString("\x1b_G")
	if imageID > 0 {
		buf.WriteString("i=")
		buf.WriteString(strconv.FormatUint(uint64(imageID), 10))
		buf.WriteByte(';')
	}
	if ok {
		buf.WriteString("OK")
	} else if message != "" {
		buf.WriteString(message)
	} else {
		buf.WriteString("ENOENT:file not found")
	}
	buf.WriteString("\x1b\\")
	return buf.Bytes()
}
