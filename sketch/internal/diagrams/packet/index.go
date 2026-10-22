// Ported from pele src/diagrams/packet/index.ts at 8fad23bd51783e4df1204e177a7fb0d2a30dcd13.

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
