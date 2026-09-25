package federation

import (
	"encoding/json"
	"testing"

	"tailscale.com/ipn/ipnstate"
)

// tailnetMachines runs discovery on a status built with tailscale's own types,
// by way of the JSON `tailscale status --json` prints. The tests in
// tailnet_test.go build their statuses this way, so each of them also checks
// that tailnetStatus reads every field discovery uses under the name
// tailscale gives it.
func tailnetMachines(st *ipnstate.Status, opt TailnetOptions) []TailnetMachine {
	if st == nil {
		return tailnetMachinesFrom(nil, opt)
	}
	data, err := json.Marshal(st)
	if err != nil {
		panic(err)
	}
	var got tailnetStatus
	if err := json.Unmarshal(data, &got); err != nil {
		panic(err)
	}
	return tailnetMachinesFrom(&got, opt)
}

// TestTailnetStatusReadsTheCommandOutput decodes a status as the tailscale
// command prints it, with the fields discovery reads and some it does not.
func TestTailnetStatusReadsTheCommandOutput(t *testing.T) {
	const out = `{
  "Version": "1.90.0",
  "BackendState": "Running",
  "Self": {"ID": "n1", "HostName": "laptop", "DNSName": "laptop.tail1.ts.net.", "OS": "macOS", "UserID": 42, "TailscaleIPs": ["100.64.0.1", "fd7a::1"], "Online": false},
  "Peer": {
    "nodekey:bb": {"HostName": "build", "DNSName": "build.tail1.ts.net.", "OS": "linux", "UserID": 42, "TailscaleIPs": ["fd7a::2", "100.64.0.2"], "Tags": ["tag:ci"], "Online": true},
    "nodekey:aa": {"HostName": "Pixel", "DNSName": "pixel.tail1.ts.net.", "OS": "android", "UserID": 7, "TailscaleIPs": ["100.64.0.3"], "Online": true, "ShareeNode": true}
  },
  "User": {"42": {"ID": 42, "LoginName": "me@example.com"}, "7": {"ID": 7, "LoginName": "friend@example.com"}}
}`
	var st tailnetStatus
	if err := json.Unmarshal([]byte(out), &st); err != nil {
		t.Fatal(err)
	}
	got := tailnetMachinesFrom(&st, DefaultTailnetOptions())
	if len(got) != 3 {
		t.Fatalf("got %d machines, want 3: %+v", len(got), got)
	}
	byName := map[string]TailnetMachine{}
	for _, m := range got {
		byName[m.Name] = m
	}
	if m := byName["laptop"]; !m.Self || !m.Online || m.IP != "100.64.0.1" || m.User != "me@example.com" || m.DNSName != "laptop.tail1.ts.net" {
		t.Errorf("self = %+v", m)
	}
	if m := byName["build"]; m.IP != "100.64.0.2" || len(m.Tags) != 1 || m.Tags[0] != "tag:ci" || m.OS != "linux" {
		t.Errorf("build = %+v", m)
	}
	if m := byName["pixel"]; !m.Shared || m.User != "friend@example.com" || m.Offered {
		t.Errorf("pixel = %+v", m)
	}
}
