package terminal

import "tuios.dev/vt"

func init() {
	vt.SetTerminalName("tuios")
	vt.AddTermcapBoolean("kitty-graphics-patches")
}
