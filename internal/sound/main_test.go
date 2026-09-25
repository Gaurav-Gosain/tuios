// This TestMain sits in the external test package rather than beside the tests
// it governs. testutil imports sound, to keep a test run from reaching the
// developer's speakers, so a TestMain in package sound importing testutil would
// close the loop. An external test package imports both and is imported by
// neither.
package sound_test

import (
	"os"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/testutil"
)

func TestMain(m *testing.M) { os.Exit(testutil.RunIsolated(m)) }
