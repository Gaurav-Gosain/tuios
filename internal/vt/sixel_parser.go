package vt

import (
	"bytes"
	"strconv"
)

// SixelCommand represents a parsed Sixel graphics command.
// Sixel DCS format: ESC P <p1>;<p2>;<p3> q <sixel-data> ST
type SixelCommand struct {
	// AspectRatio specifies the pixel aspect ratio
	// 0,1 = 2:1 (default), 2 = 5:1, 3,4 = 3:1, 5,6 = 2:1, 7,8,9 = 1:1
	AspectRatio int

	// BackgroundMode specifies how the background is handled
	// 0 = device default, 1 = no change (transparent), 2 = set to color 0
	BackgroundMode int

	// HorizontalGrid is deprecated but may be present (ignored)
	HorizontalGrid int

	// Width is the calculated width of the image in pixels
	Width int

	// Height is the calculated height of the image in pixels
	Height int

	// Data contains the raw sixel raster data (after the 'q' introducer)
	Data []byte

	// RawSequence contains the complete DCS sequence for passthrough
	RawSequence []byte
}

// ParseSixelCommand parses a DCS sixel sequence.
// The data parameter should contain everything after the DCS introducer,
// including parameters, the 'q' introducer, and sixel data.
func ParseSixelCommand(data []byte) *SixelCommand {
	if len(data) == 0 {
		return nil
	}

	cmd := &SixelCommand{
		AspectRatio:    2, // Default 2:1
		BackgroundMode: 0, // Device default
		RawSequence:    data,
	}

	// Find the 'q' introducer that marks the start of sixel data
	qIdx := bytes.IndexByte(data, 'q')
	if qIdx == -1 {
		// No sixel introducer found
		return nil
	}

	// Parse parameters before 'q'
	if qIdx > 0 {
		paramStr := string(data[:qIdx])
		params := bytes.Split([]byte(paramStr), []byte{';'})
		for i, p := range params {
			val, err := strconv.Atoi(string(bytes.TrimSpace(p)))
			if err != nil {
				continue
			}
			switch i {
			case 0:
				cmd.AspectRatio = val
			case 1:
				cmd.BackgroundMode = val
			case 2:
				cmd.HorizontalGrid = val
			}
		}
	}

	// Extract sixel data (everything after 'q')
	if qIdx+1 < len(data) {
		cmd.Data = data[qIdx+1:]
	}

	// Calculate dimensions from sixel data
	cmd.Width, cmd.Height = calculateSixelDimensions(cmd.Data)

	return cmd
}

// calculateSixelDimensions parses sixel data to determine image dimensions.
// Returns width and height in pixels.
func calculateSixelDimensions(data []byte) (width, height int) {
	if len(data) == 0 {
		return 0, 0
	}

	// Track position
	x := 0
	maxX := 0
	sixelLines := 1 // At least one sixel line

	i := 0
	for i < len(data) {
		c := data[i]

		switch {
		case c == '#':
			// Color introducer: #Pc;Pu;Px;Py;Pz or #Pc
			// Skip until we hit a non-parameter character
			i++
			for i < len(data) && (data[i] >= '0' && data[i] <= '9' || data[i] == ';') {
				i++
			}
			continue

		case c == '$':
			// Carriage return - go to left edge
			if x > maxX {
				maxX = x
			}
			x = 0
			i++
			continue

		case c == '-':
			// New sixel line (move down 6 pixels)
			if x > maxX {
				maxX = x
			}
			x = 0
			sixelLines++
			i++
			continue

		case c == '!':
			// Repeat introducer: !<count><char>
			i++
			// Parse repeat count
			countStart := i
			for i < len(data) && data[i] >= '0' && data[i] <= '9' {
				i++
			}
			count := 1
			if i > countStart {
				if n, err := strconv.Atoi(string(data[countStart:i])); err == nil {
					count = n
				}
			}
			// The character being repeated
			if i < len(data) && data[i] >= '?' && data[i] <= '~' {
				x += count
				i++
			}
			continue

		case c >= '?' && c <= '~':
			// Sixel data character (represents 6 vertical pixels)
			x++
			i++
			continue

		case c == '"':
			// Raster attributes: "Pan;Pad;Ph;Pv
			// Pan = pixel aspect numerator
			// Pad = pixel aspect denominator
			// Ph = horizontal extent in pixels
			// Pv = vertical extent in pixels
			i++
			params := make([]int, 0, 4)
			for i < len(data) && len(params) < 4 {
				numStart := i
				for i < len(data) && data[i] >= '0' && data[i] <= '9' {
					i++
				}
				if i > numStart {
					if n, err := strconv.Atoi(string(data[numStart:i])); err == nil {
						params = append(params, n)
					}
				}
				if i < len(data) && data[i] == ';' {
					i++
				} else {
					break
				}
			}
			// If Ph and Pv are specified, use them directly
			if len(params) >= 4 && params[2] > 0 && params[3] > 0 {
				return params[2], params[3]
			}
			continue

		default:
			// Unknown character, skip
			i++
		}
	}

	// Final position check
	if x > maxX {
		maxX = x
	}

	// Height is sixel lines * 6 pixels per line
	height = sixelLines * 6

	return maxX, height
}

// SixelAction represents the type of sixel operation.
type SixelAction int

const (
	SixelActionDisplay SixelAction = iota // Display sixel image at cursor
	SixelActionQuery                      // Query sixel support (rarely used)
)

// RowsForHeight returns the number of terminal rows needed for a sixel image
// given its pixel height and the terminal's cell height.
func (cmd *SixelCommand) RowsForHeight(cellHeight int) int {
	if cellHeight <= 0 || cmd.Height <= 0 {
		return 0
	}
	return (cmd.Height + cellHeight - 1) / cellHeight
}

// ColsForWidth returns the number of terminal columns needed for a sixel image
// given its pixel width and the terminal's cell width.
func (cmd *SixelCommand) ColsForWidth(cellWidth int) int {
	if cellWidth <= 0 || cmd.Width <= 0 {
		return 0
	}
	return (cmd.Width + cellWidth - 1) / cellWidth
}
