package session

import (
	"context"
	"encoding/json"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/federation"
)

const routeLANLimit = 3

const routeTailnetWait = 2 * time.Second

func (d *Daemon) verbListRoutes(_ *connState, _ json.RawMessage) (any, *verbError) {
	ctx, cancel := context.WithTimeout(context.Background(), routeTailnetWait)
	defer cancel()
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
