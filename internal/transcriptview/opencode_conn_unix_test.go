//go:build linux || darwin

package transcriptview

import (
	"os"
	"testing"
)

const wantsServicePID = false

func asOtherUser(t *testing.T) {
	old := myUID
	myUID = func() int { return os.Getuid() + 1 }
	t.Cleanup(func() { myUID = old })
}
