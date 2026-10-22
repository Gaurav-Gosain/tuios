package app

import (
	"strconv"
	"testing"
)

// The agents view holds a host to remoteAgentRowsMax rows, whatever its
// daemon sent. Security boundary test.
func TestRemoteAgentRowsAreBounded(t *testing.T) {
	m := &OS{}
	h := FederationHost{Name: "build", Status: "up"}
	for i := range 20 {
		s := FederationSession{Name: "s" + strconv.Itoa(i)}
		for j := range 40 {
			s.Agents = append(s.Agents, FederationAgent{ID: "w" + strconv.Itoa(j), State: "working"})
		}
		h.Sessions = append(h.Sessions, s)
	}
	m.FederationHosts = []FederationHost{h}
	if n := len(m.remoteAgentEntries()); n != remoteAgentRowsMax {
		t.Fatalf("the view took %d rows of one host, want %d", n, remoteAgentRowsMax)
	}
}
