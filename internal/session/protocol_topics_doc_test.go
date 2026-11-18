package session

import (
	"os"
	"strings"
	"testing"
)

// TestProtocolTopicsNameEveryVerb fails on a verb that the "Verbs by topic"
// part of docs/protocol.md does not link to. tuios.dev renders that page, and
// the topics are how a reader finds a verb by what it does.
func TestProtocolTopicsNameEveryVerb(t *testing.T) {
	data, err := os.ReadFile("../../docs/protocol.md")
	if err != nil {
		t.Fatal(err)
	}
	_, topics, ok := strings.Cut(string(data), "\n## Verbs by topic\n")
	if !ok {
		t.Fatal("protocol.md has no Verbs by topic section")
	}
	if end := strings.Index(topics, "\n## "); end >= 0 {
		topics = topics[:end]
	}
	for name := range verbRegistry() {
		if !strings.Contains(topics, "(protocol-verbs.md#"+name+")") {
			t.Errorf("Verbs by topic in protocol.md does not link to the verb %q. Add [`%s`](protocol-verbs.md#%s) to its topic", name, name, name)
		}
	}
}
