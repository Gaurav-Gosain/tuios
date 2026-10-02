package app

import "time"

// tapeFinishRefreshDelay is how long after a tape finishes the client waits
// before re-fetching every pane's content from the daemon. It lets the last
// commands' output land on the daemon first.
const tapeFinishRefreshDelay = 1200 * time.Millisecond

// tapeLayoutRefreshMsg asks the Update loop to refresh all panes from the daemon
// after a project tape has built its layout. See refreshAllPanesAfterTape.
type tapeLayoutRefreshMsg struct{}
