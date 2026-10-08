package stopevent

import (
	"strings"
	"testing"
)

// TestName holds the name kill-server and the daemon both derive to the
// rules Windows puts on it: one name for one socket in any case, another
// name for another socket, and a name Windows accepts.
func TestName(t *testing.T) {
	a := Name(`C:\Users\me\AppData\Local\tuios\tuios.sock`)
	if b := Name(`c:\users\ME\appdata\local\TUIOS\tuios.sock`); a != b {
		t.Errorf("one path in two cases gives two names: %s, %s", a, b)
	}
	if b := Name(`C:\Users\other\AppData\Local\tuios\tuios.sock`); a == b {
		t.Errorf("two sockets share the name %s", a)
	}
	if !strings.HasPrefix(a, `Local\`) || strings.Count(a, `\`) != 1 || len(a) > 260 {
		t.Errorf("name %q is not a valid Local\\ object name", a)
	}
}
