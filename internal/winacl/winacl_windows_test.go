//go:build windows

package winacl

import (
	"testing"

	"golang.org/x/sys/windows"
)

// TestGrantsOther runs the DACL check over hand-written security
// descriptors. Each case is a way a file could let another user change it,
// or a way a safe file could look unsafe. WD is Everyone, SY SYSTEM, BA
// Administrators and OW OWNER RIGHTS.
func TestGrantsOther(t *testing.T) {
	me, _, err := CurrentUser()
	if err != nil {
		t.Fatal(err)
	}
	trusted, err := Trusted(me)
	if err != nil {
		t.Fatal(err)
	}
	mine := "(A;;FA;;;SY)(A;;FA;;;BA)(A;;FA;;;" + me.String() + ")"
	for _, c := range []struct {
		name, sddl string
		refuse     bool
	}{
		{"only trusted accounts", "D:P" + mine, false},
		{"OWNER RIGHTS", "D:P" + mine + "(A;;FA;;;OW)", false},
		{"Everyone may write", "D:P" + mine + "(A;;FW;;;WD)", true},
		{"Everyone may append", "D:P" + mine + "(A;;0x4;;;WD)", true},
		{"Everyone may change the DACL", "D:P" + mine + "(A;;WD;;;WD)", true},
		{"Everyone may take ownership", "D:P" + mine + "(A;;WO;;;WD)", true},
		{"Everyone may delete", "D:P" + mine + "(A;;SD;;;WD)", true},
		{"Everyone has generic write", "D:P" + mine + "(A;;GW;;;WD)", true},
		{"Everyone has generic all", "D:P" + mine + "(A;;GA;;;WD)", true},
		{"Everyone may only read", "D:P" + mine + "(A;;FR;;;WD)", false},
		{"a deny entry", "D:P" + mine + "(D;;FA;;;WD)", false},
		{"an inherit-only entry", "D:P" + mine + "(A;OICIIO;FA;;;WD)", false},
		{"a callback allow entry", "D:P" + mine + "(XA;;FW;;;WD;(Member_of {SID(BA)}))", true},
		{"an object allow entry", "D:P" + mine + "(OA;;CC;bf967aba-0de6-11d0-a285-00aa003049e2;;WD)", true},
		{"a NULL DACL", "D:NO_ACCESS_CONTROL", true},
		{"no DACL", "O:SY", true},
	} {
		t.Run(c.name, func(t *testing.T) {
			sd, err := windows.SecurityDescriptorFromString(c.sddl)
			if err != nil {
				t.Fatalf("%s: %v", c.sddl, err)
			}
			other, err := GrantsOther(sd, WriteAccess, trusted)
			if err != nil {
				t.Fatal(err)
			}
			if (other != "") != c.refuse {
				t.Fatalf("%s: other %q, want refused %v", c.sddl, other, c.refuse)
			}
		})
	}
}
