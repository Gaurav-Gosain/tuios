package session

import (
	"context"
	"encoding/json"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/federation"
)

// routeLANLimit is how many LAN addresses list-routes reports.
const routeLANLimit = 3

// routeTailnetWait bounds the question to tailscaled.
const routeTailnetWait = 2 * time.Second

// verbListRoutes answers list-routes: the addresses this machine has now, so
// a phone paired on one network can reach it from another. The tailnet name
// comes first, since it works from anywhere the tailnet does.
func (d *Daemon) verbListRoutes(_ *connState, _ json.RawMessage) (any, *verbError) {
	ctx, cancel := context.WithTimeout(context.Background(), routeTailnetWait)
	defer cancel()
	// An error means no tailscaled: LAN addresses are still an answer.
	machines, _ := federation.TailnetMachines(ctx, federation.TailnetOptions{})
	routes := []map[string]any{}
	for _, a := range federation.LocalAddrs(machines, routeLANLimit) {
		kind := "lan"
		switch {
		case a.Tailnet:
			kind = "tailnet"
		case a.Public:
			kind = "public"
		}
		routes = append(routes, map[string]any{"host": a.Host, "kind": kind})
	}
	return map[string]any{"type": "route_list", "routes": routes}, nil
}
