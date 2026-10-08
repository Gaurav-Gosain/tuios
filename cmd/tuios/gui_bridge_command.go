package main

import (
	"os"

	"github.com/spf13/cobra"

	"github.com/Gaurav-Gosain/tuios/internal/guibridge"
)

// newGUIBridgeCommand is `tuios gui-bridge`, the process a native renderer
// (tuios-gpui) starts to reach a session. It speaks the frame format in
// internal/guibridge on stdin and stdout. It is not for running by hand.
func newGUIBridgeCommand() *cobra.Command {
	var opts guibridge.Options
	var resume string
	cmd := &cobra.Command{
		Use:    "gui-bridge",
		Short:  "Serve a session to a native renderer over stdin and stdout",
		Hidden: true,
		Args:   cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			// The protocol owns stdout. Anything else that prints, such as the
			// note that the daemon is starting, goes to stderr instead.
			proto := os.Stdout
			os.Stdout = os.Stderr
			if err := ensureDaemon(); err != nil {
				return err
			}
			if resume != "" {
				r, err := guibridge.ParseResume(resume)
				if err != nil {
					return err
				}
				opts.Resume = r
			}
			opts.Version = version
			opts.In = os.Stdin
			opts.Out = proto
			return guibridge.Run(opts)
		},
	}
	f := cmd.Flags()
	f.StringVar(&opts.Session, "session", "", "Session to attach, created when missing")
	f.IntVar(&opts.Cols, "cols", 120, "Grid width in cells")
	f.IntVar(&opts.Rows, "rows", 40, "Grid height in cells")
	f.IntVar(&opts.CellWidth, "cell-width", 9, "Cell width in pixels")
	f.IntVar(&opts.CellHeight, "cell-height", 18, "Cell height in pixels")
	f.IntSliceVar(&opts.Insets, "insets", nil, "Room around each pane's text in pixels: top,left,right,bottom")
	f.StringVar(&opts.Theme, "theme", "", "Theme to use instead of the one the config names")
	f.StringVar(&opts.SessionID, "session-id", "", "Session to attach by id, before --session; with neither, the session last active")
	f.StringVar(&opts.Host, "host", "", "Attach a session on this host from the [hosts] table, through this machine's daemon")
	f.StringVar(&resume, "resume", "", "Stream position the renderer holds per pane: pty=seq,pty=seq")
	f.IntVar(&opts.ResumePID, "resume-pid", 0, "The daemon pid the --resume positions came from")
	f.IntVar(&opts.Scrollback, "scrollback", 0, "History rows each pane's snapshot carries (default: the daemon's, 1000)")
	return cmd
}
