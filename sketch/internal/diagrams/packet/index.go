// Ported from pele src/diagrams/packet/index.ts at 8603bf3313add9143cd417015d4a6bd6c522dada.

// Package packet draws Mermaid's packet diagram: the fields of a network packet as boxes
// in rows of bits, with the bit numbers above them.
package packet

import "tuios.dev/sketch/internal/types"

// Packet is the packet diagram type.
var Packet = &types.Diagram{
	Type: "packet",
	Parse: func(source string, config types.Config, title *string, _ int, _ types.ParseLimits) any {
		return buildPacket(parsePacket(source), config, types.Or(title, ""))
	},
	Render: func(model any, config types.Config, options types.RenderOptions) types.Rendered {
		return renderPacket(model.(*PacketModel), config, options)
	},
}
