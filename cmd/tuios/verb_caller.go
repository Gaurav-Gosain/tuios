package main

import "encoding/json"

// verbCaller is the one method the hook needs from a verb client, so a test
// can stand in for the daemon.
type verbCaller interface {
	Call(verb string, params any) (json.RawMessage, error)
}
