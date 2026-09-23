//go:build js

package federation

import "context"

// TailnetMachines reports no tailnet in the browser build: there is no
// tailscaled in a browser tab to ask.
func TailnetMachines(_ context.Context, _ TailnetOptions) ([]TailnetMachine, error) {
	return nil, ErrNoTailnet
}
