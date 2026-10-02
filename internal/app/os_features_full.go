//go:build !slim

package app

import (
	"sync"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/federation"
)

// osFeatures is the part of OS that only the full build reads. tuios-slim
// leaves these features out and has an empty osFeatures (os_features_slim.go).
type osFeatures struct {
	// captureHits are the window rectangles capture mode drew a highlight
	// around this frame, so the click handler reads what was drawn instead of
	// recomputing a layout. Reused between frames, never reallocated.
	captureHits []captureHit

	// shotImagePlaced records that the preview's kitty placement is on the
	// host, so closing the panel takes it down again. shotImageSent records
	// that the picture itself is resident, so a panel that only moved costs a
	// placement and not another upload. shotPlacement is what was last drawn,
	// so an unchanged frame emits nothing at all.
	shotImagePlaced bool
	shotImageSent   bool
	shotPlacement   screenshotPlacementState

	// shotCaptures counts the captures this client has taken. It is what
	// names the picture the host holds, because the host holds one picture
	// under the preview's image id and the only question that matters is
	// whether that picture is this capture's. The file name cannot answer it:
	// two captures in one second share a name.
	shotCaptures int

	// shotDiscarded holds the serials of captures the user dismissed before
	// their file was written. The result that arrives afterwards removes its
	// own file and says nothing. Nil when nothing is pending, which is almost
	// always, so it costs an idle frame nothing.
	shotDiscarded     []int
	tailnetMu         sync.Mutex
	tailnetAskedAt    time.Time
	tailnetCandidates []string
	// tapeDetect holds the project-tape detection state (trust store, session
	// memory of handled directories, debounce bookkeeping, and the current
	// passive indicator). See tape_detect.go.
	tapeDetect tapeDetectState
	// inboxEvents carries what the Inbox watcher reads off the daemon, and
	// stopInbox ends the watcher. Both nil until the watcher starts.
	inboxEvents chan tea.Msg
	stopInbox   func()
	// hostTests are the results of the settings page's last link test, keyed by
	// host name. A row prefers its test result to the daemon's snapshot: the
	// test is newer, and it is what the user just asked for.
	hostTests map[string]federation.HostReport
	// hostTestRunning is true while a link test is in flight, so the row cannot
	// start a second one.
	hostTestRunning bool
	// hostsToApply are the hosts the person changed on the settings page
	// since the last save. The daemon applies a file change that dials a
	// new host only for the person, so the save asks it to (applyHostsCmd).
	hostsToApply []string
}
