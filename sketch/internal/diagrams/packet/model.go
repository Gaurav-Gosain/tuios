// Ported from pele src/diagrams/packet/model.ts at 8aff183be57021b533f319a29341a529be934b08.

package packet

import (
	"math"
	"strings"
	"unicode/utf8"

	"tuios.dev/sketch/internal/js"
	"tuios.dev/sketch/internal/types"
)

type PacketBlock struct {
	start float64
	end   float64
	// As Mermaid counts it: one less than the width for the pieces of a field split across rows.
	bits  float64
	label string
}

type PacketModel struct {
	title      string
	accTitle   string
	accDescr   string
	bitsPerRow float64
	rows       [][]PacketBlock
}

const MAX_ROWS = 10_000

// packetOption is config.packet[key] when it is a number of at least min, capped at max.
func packetOption(config types.Config, key string, fallback, min, max float64) float64 {
	section, _ := config["packet"].(map[string]any)
	if value, ok := section[key].(float64); ok && value >= min {
		return math.Min(value, max)
	}
	return fallback
}

func invalid(message string) {
	types.Throw(message, types.CodeSemantic, types.ErrorInfo{Type: "packet"})
}

// joinLines is .replace(/\n\s+/g, '\n').
func joinLines(text string) string {
	var out strings.Builder
	for i := 0; i < len(text); {
		if text[i] != '\n' {
			out.WriteByte(text[i])
			i++
			continue
		}
		j := i + 1
		for j < len(text) {
			c, size := utf8.DecodeRuneInString(text[j:])
			if !js.IsSpace(c) {
				break
			}
			j += size
		}
		out.WriteByte('\n')
		i = j
	}
	return out.String()
}

func buildPacket(ast *PacketAst, config types.Config, title string) *PacketModel {
	bitsPerRow := math.Floor(packetOption(config, "bitsPerRow", 32, 1, 4096))
	var rows [][]PacketBlock
	lastBit := -1.0
	var word []PacketBlock
	row := 1.0

	for _, block := range ast.blocks {
		start, end, bits := block.start, block.end, block.bits
		hasEnd, hasBits := block.hasEnd, block.hasBits
		if block.hasStart && hasEnd && end < start {
			invalid("Packet block " + js.Num(start) + " - " + js.Num(end) + " is invalid. End must be greater than start.")
		}
		if !block.hasStart {
			start = lastBit + 1
		}
		if start != lastBit+1 {
			shown := start
			if hasEnd {
				shown = end
			}
			invalid("Packet block " + js.Num(start) + " - " + js.Num(shown) + " is not contiguous. It should start from " + js.Num(lastBit+1) + ".")
		}
		if hasBits && bits == 0 {
			invalid("Packet block " + js.Num(start) + " is invalid. Cannot have a zero bit field.")
		}
		if !hasEnd {
			b := 1.0
			if hasBits {
				b = bits
			}
			end = start + b - 1
		}
		if !hasBits {
			bits = end - start + 1
		}
		lastBit = end

		for float64(len(word)) <= bitsPerRow+1 && len(rows) < MAX_ROWS {
			rowEnd := float64(row*bitsPerRow) - 1
			if end <= rowEnd {
				word = append(word, PacketBlock{start, end, bits, block.label})
				if end == rowEnd {
					rows = append(rows, word)
					word = nil
					row++
				}
				break
			}
			word = append(word, PacketBlock{start, rowEnd, rowEnd - start, block.label})
			rows = append(rows, word)
			word = nil
			row++
			start = rowEnd + 1
			bits = end - start
		}
	}
	if len(word) > 0 {
		rows = append(rows, word)
	}

	model := &PacketModel{title: ast.title, bitsPerRow: bitsPerRow, rows: rows}
	if model.title == "" {
		model.title = title
	}
	if ast.accTitle != "" {
		model.accTitle = js.TrimStart(ast.accTitle)
	}
	if ast.accDescr != "" {
		model.accDescr = joinLines(ast.accDescr)
	}
	return model
}
