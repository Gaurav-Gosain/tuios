package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/shquote"

	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/errfmt"
	"github.com/Gaurav-Gosain/tuios/internal/jsonx"
	"github.com/Gaurav-Gosain/tuios/internal/layout"
	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

const xpanesMaxPanes = 64

const xpanesDefaultPlaceholder = "{}"

type xpanesOptions struct {
	session     string
	workspace   int
	command     string
	placeholder string
	layout      string
	perPane     int
	ssh         bool
	noSync      bool
	force       bool
	jsonOutput  bool
	speedy      int
	interval    float64
}

func newXpanesCommand() *cobra.Command {
	var o xpanesOptions
	cmd := &cobra.Command{
		Use:   "xpanes [flags] [items...]",
		Short: "Open one pane per item in a new workspace, with multifocus on",
		Long: `Open one pane per item in a new tiled workspace, and turn multifocus on for
all of them. Then the keys you type go to every pane. This is like
tmux-xpanes.

The items are the arguments. With no arguments, tuios reads one item from each
line of stdin, when stdin is not a terminal. tuios ignores empty lines.

Each pane starts a shell. With -c, tuios types the command into the shell and
replaces {} with the item, in shell quotes. The shell stays when the command
stops. In a session on another machine, the daemon there chooses the shell,
and the pane does not get TUIOS_XPANES_ITEM.

-s is speedy mode: the pane runs the command with sh -c, with no interactive
shell. When the command stops, the pane shows a message and stays until you
press Enter. -ss closes the pane when the command stops. --ssh turns on -s.

--interval waits that many seconds between the panes, for example 0.5. With
-s or -ss, tuios waits between opening the panes. Without them, it opens all
the panes and waits between typing the commands.

Each pane gets the item in TUIOS_XPANES_ITEM and its number, from 1, in
TUIOS_XPANES_INDEX. The item is the name of the pane.

tuios opens the panes on the first empty workspace of the session, and shows
that workspace. Inside a tuios pane, the session is the session of the pane.
Outside, it is the most recently active session. --session names a different
one.

The layout and multifocus need a client attached to the session. The layout
needs tiling on and the bsp layout. Otherwise tuios opens the panes and tells
you what it could not do.`,
		Example: `  # Three ssh sessions, and type into all of them at the same time
  tuios xpanes --ssh host1 host2 host3

  # Items from stdin, one command for each
  printf 'a\nb\nc\n' | tuios xpanes -c 'echo {}; exec $SHELL'

  # Tail logs side by side, without multifocus
  ls /var/log/*.log | tuios xpanes -l even-horizontal --no-sync -c 'tail -f {}'

  # Two items for each pane
  tuios xpanes -n 2 -c 'diff {}' a.txt b.txt c.txt d.txt

  # Speedy mode: no shell, and the pane closes when curl stops, one second apart
  tuios xpanes -ss --interval 1 -c 'curl -s https://{}/health' web1 web2 web3`,
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			items, err := xpanesItems(args, cmd.InOrStdin())
			if err != nil {
				return err
			}
			return runXpanes(o, items)
		},
	}
	f := cmd.Flags()
	f.StringVar(&o.session, "session", "", "Session to open the panes in (default: this pane's session, else the most recently active)")
	f.CountVarP(&o.speedy, "speedy", "s", "Speedy mode: run the command with no interactive shell, and hold the pane until Enter. -ss closes the pane when the command stops")
	f.Float64Var(&o.interval, "interval", 0, "Seconds to wait between the panes, for example 0.5")
	f.IntVar(&o.workspace, "workspace", 0, "Workspace to open the panes on. It must be empty (default: the first empty workspace)")
	f.StringVarP(&o.command, "command", "c", "", "Command to run in each pane, with {} replaced by the item")
	f.StringVarP(&o.placeholder, "replace", "I", xpanesDefaultPlaceholder, "Text in the command that tuios replaces with the item")
	f.StringVarP(&o.layout, "layout", "l", layout.ArrangeTiled, "Layout: tiled, even-horizontal or even-vertical (also t, eh, ev)")
	f.IntVarP(&o.perPane, "items-per-pane", "n", 1, "Number of items for each pane. tuios joins them with spaces")
	f.BoolVar(&o.ssh, "ssh", false, "Run ssh with the item in each pane. The same as -s -c 'ssh -- {}'")
	f.BoolVar(&o.noSync, "no-sync", false, "Do not turn multifocus on")
	f.BoolVar(&o.force, "force", false, fmt.Sprintf("Open more than %d panes", xpanesMaxPanes))
	addJSONFlag(cmd, &o.jsonOutput)
	cmd.MarkFlagsMutuallyExclusive("command", "ssh")
	_ = cmd.RegisterFlagCompletionFunc("session", completeSessionNames)
	_ = cmd.RegisterFlagCompletionFunc("layout", cobra.FixedCompletions(layout.Arrangements, cobra.ShellCompDirectiveNoFileComp))
	return cmd
}

func xpanesItems(args []string, stdin io.Reader) ([]string, error) {
	var items []string
	if len(args) > 0 {
		for _, a := range args {
			if strings.TrimSpace(a) != "" {
				items = append(items, a)
			}
		}
	} else if !readerIsTerminal(stdin) {
		sc := bufio.NewScanner(stdin)
		sc.Buffer(make([]byte, 64*1024), 1024*1024)
		for sc.Scan() {
			if line := strings.TrimRight(sc.Text(), "\r"); strings.TrimSpace(line) != "" {
				items = append(items, line)
			}
		}
		if err := sc.Err(); err != nil {
			return nil, errfmt.Errorf("could not read the items from stdin: %w", err)
		}
	}
	if len(items) == 0 {
		return nil, errors.New("there are no items. Give the items as arguments, or one on each line of stdin")
	}
	return items, nil
}

func readerIsTerminal(r io.Reader) bool {
	f, ok := r.(*os.File)
	return ok && term.IsTerminal(int(f.Fd()))
}

func xpanesLayout(name string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "", "t", layout.ArrangeTiled:
		return layout.ArrangeTiled, nil
	case "eh", layout.ArrangeEvenHorizontal:
		return layout.ArrangeEvenHorizontal, nil
	case "ev", layout.ArrangeEvenVertical:
		return layout.ArrangeEvenVertical, nil
	}
	return "", errfmt.Errorf("the layout %q is not known. Use tiled, even-horizontal or even-vertical", name)
}

type xpanesPane struct {
	Items       []string
	Title       string
	Argv        []string
	Line        string
	CloseOnExit bool
}

const (
	xpanesInteractive = 0
	xpanesSpeedy      = 1
	xpanesSpeedyClose = 2
)

const xpanesHoldMessage = "The command stopped. Press Enter to close the pane."

var xpanesHold = `printf '\n\033[7m %s \033[0m\n' '` + xpanesHoldMessage + `' >&2; read _`

func xpanesPanes(items []string, perPane int, command, placeholder, shell string, speedy int) []xpanesPane {
	perPane = max(perPane, 1)
	var panes []xpanesPane
	for start := 0; start < len(items); start += perPane {
		group := items[start:min(start+perPane, len(items))]
		joined := strings.Join(group, " ")
		env := []string{
			"env",
			"TUIOS_XPANES_ITEM=" + joined,
			fmt.Sprintf("TUIOS_XPANES_INDEX=%d", len(panes)+1),
		}
		line := ""
		if command != "" {
			quoted := make([]string, len(group))
			for i, it := range group {
				quoted[i] = shquote.Word(it)
			}
			line = command
			if placeholder != "" {
				line = strings.ReplaceAll(command, placeholder, strings.Join(quoted, " "))
			}
		}
		p := xpanesPane{Items: group, Title: xpanesTitle(joined)}
		switch {
		case line != "" && speedy == xpanesSpeedy:
			p.Argv = append(env, "sh", "-c", line+"\n"+xpanesHold)
		case line != "" && speedy == xpanesSpeedyClose:
			p.Argv = append(env, "sh", "-c", line)
			p.CloseOnExit = true
		default:
			if shell != "" {
				p.Argv = append(env, shell)
			}
			p.Line = line
		}
		panes = append(panes, p)
	}
	return panes
}

func xpanesTitle(item string) string {
	item = strings.Join(strings.Fields(item), " ")
	if r := []rune(item); len(r) > 60 {
		item = string(r[:59]) + "…"
	}
	return item
}

const xpanesReadyTimeout = 10 * time.Second

const xpanesReadyIdle = 300 * time.Millisecond

func xpanesWindowParams(t *verbTarget, window string, p map[string]any) map[string]any {
	p = t.params(p)
	p["window"] = window
	return p
}

type xpanesTypeError struct {
	what string
	err  error
}

func xpanesTypeCommands(c verbCaller, t *verbTarget, panes []xpanesPane, windows []string, interval, timeout time.Duration) []xpanesTypeError {
	var errs []xpanesTypeError
	typed := 0
	for i, p := range panes {
		if p.Line == "" || i >= len(windows) {
			continue
		}
		if typed > 0 {
			time.Sleep(interval)
		}
		typed++
		id := windows[i]
		ms := int(timeout / time.Millisecond)
		_, _ = c.Call("wait-for", xpanesWindowParams(t, id, map[string]any{
			"condition": "window-output", "pattern": `\S`, "timeout": ms,
		}))
		_, _ = c.Call("wait-for", xpanesWindowParams(t, id, map[string]any{
			"condition": "window-idle", "idle": int(xpanesReadyIdle / time.Millisecond), "timeout": ms,
		}))
		if _, err := c.Call("send-text", xpanesWindowParams(t, id, map[string]any{"text": p.Line + "\r"})); err != nil {
			errs = append(errs, xpanesTypeError{fmt.Sprintf("tuios could not type the command into pane %d", i+1), err})
		}
	}
	return errs
}

func xpanesSpeedyMode(o xpanesOptions, command string) (int, error) {
	speedy := o.speedy
	if o.ssh && speedy == xpanesInteractive {
		speedy = xpanesSpeedy
	}
	switch {
	case speedy > xpanesSpeedyClose:
		return 0, errors.New("give -s one or two times: -s holds the pane, -ss closes it")
	case speedy != xpanesInteractive && command == "":
		return 0, errors.New("-s and -ss run a command. Add -c or --ssh")
	}
	return speedy, nil
}

func xpanesCommandLine(o xpanesOptions) string {
	if o.ssh {
		return "ssh -- " + o.placeholder
	}
	return o.command
}

func xpanesShell(host, envShell string) string {
	switch {
	case host != "":
		return ""
	case envShell != "":
		return envShell
	}
	return "/bin/sh"
}

type xpanesResult struct {
	Session    string
	Workspace  int
	Windows    []string
	Layout     string
	Arranged   bool
	Multifocus bool
	Warnings   []string
}

func runXpanes(o xpanesOptions, items []string) error {
	if runtime.GOOS == "windows" {
		return errors.New("tuios xpanes needs sh, so it does not work on Windows")
	}
	kind, err := xpanesLayout(o.layout)
	if err != nil {
		return err
	}
	if o.perPane < 1 {
		return errors.New("-n must be 1 or more")
	}
	command := xpanesCommandLine(o)
	speedy, err := xpanesSpeedyMode(o, command)
	if err != nil {
		return err
	}
	if o.interval < 0 {
		return errors.New("--interval must be 0 or more seconds")
	}
	interval := time.Duration(o.interval * float64(time.Second))
	sessionName := paneSessionOr(o.session)
	host, _, _, err := resolveTarget(sessionName, "")
	if err != nil {
		return err
	}
	panes := xpanesPanes(items, o.perPane, command, o.placeholder, xpanesShell(host, os.Getenv("SHELL")), speedy)
	if len(panes) > xpanesMaxPanes && !o.force {
		return errfmt.Errorf("this opens %d panes, and the limit is %d. Add --force to open them all", len(panes), xpanesMaxPanes)
	}

	t, err := dialSessionTarget(sessionName)
	if err != nil {
		return err
	}
	defer t.Close()

	res := xpanesResult{Layout: kind}
	ws, name, current, err := 0, "", 0, error(nil)
	paneWS, inScratch := xpanesPaneWorkspace(t, os.Getenv("TUIOS_PANE_ID"))
	scratchWS := 0
	if o.workspace == 0 && inScratch {
		scratchWS = paneWS
	}
	if scratchWS != 0 {
		ws, name = scratchWS, t.session
	} else {
		ws, name, current, err = xpanesWorkspace(t, o.workspace)
		if err != nil {
			return reportVerbError(err, o.jsonOutput)
		}
	}
	res.Session, res.Workspace = name, ws
	if scratchWS == 0 {
		params := map[string]any{"workspace": ws}
		origin := current
		if paneWS != 0 && !inScratch {
			origin = paneWS
		}
		if origin != 0 && origin != ws {
			params["return_to"] = origin
		}
		if _, err := t.client.Call("select-workspace", t.params(params)); err != nil {
			return reportVerbError(t.explain("select-workspace", err), o.jsonOutput)
		}
	}

	cwd := ""
	if t.host == "" {
		cwd, _ = os.Getwd()
	}
	for i, p := range panes {
		if i > 0 && speedy != xpanesInteractive {
			time.Sleep(interval)
		}
		params := map[string]any{
			"name":      p.Title,
			"workspace": ws,
			"focus":     i == 0,
		}
		if len(p.Argv) > 0 {
			params["command"] = p.Argv
		}
		if p.CloseOnExit {
			params["close_on_exit"] = true
		}
		if cwd != "" {
			params["cwd"] = cwd
		}
		raw, err := t.client.Call("new-window", t.params(params))
		if err != nil {
			if len(res.Windows) > 0 {
				err = errfmt.Errorf("%w (%d of %d panes are open)", t.explain("new-window", err), len(res.Windows), len(panes))
			}
			return reportVerbError(err, o.jsonOutput)
		}
		var w struct {
			WindowID string `json:"window_id"`
		}
		if err := jsonx.Unmarshal(raw, &w); err != nil || w.WindowID == "" {
			return errfmt.Errorf("the daemon did not return the id of the new window")
		}
		res.Windows = append(res.Windows, w.WindowID)
	}

	arrange := append([]string{kind, strconv.Itoa(ws)}, res.Windows...)
	if err := xpanesClientCommand(t, "ArrangePanes", arrange); err != nil {
		res.Warnings = append(res.Warnings, xpanesWarning("tuios could not lay out the panes", err, name))
	} else {
		res.Arranged = true
	}
	if !o.noSync {
		if err := xpanesClientCommand(t, "SetMultifocus", res.Windows); err != nil {
			res.Warnings = append(res.Warnings, xpanesWarning("tuios could not turn multifocus on", err, name))
		} else {
			res.Multifocus = true
		}
	}
	_, _ = t.client.Call("focus-window", xpanesWindowParams(t, res.Windows[0], nil))

	for _, err := range xpanesTypeCommands(t.client, t, panes, res.Windows, interval, xpanesReadyTimeout) {
		res.Warnings = append(res.Warnings, xpanesWarning(err.what, err.err, name))
	}

	if o.jsonOutput {
		outputJSON(map[string]any{
			"success": true, "session": res.Session, "workspace": res.Workspace,
			"windows": res.Windows, "layout": res.Layout, "arranged": res.Arranged,
			"multifocus": res.Multifocus, "warnings": res.Warnings,
		})
		return nil
	}
	fmt.Println(xpanesSummary(res))
	for _, w := range res.Warnings {
		fmt.Fprintln(os.Stderr, w)
	}
	return nil
}

func xpanesWorkspace(t *verbTarget, asked int) (int, string, int, error) {
	raw, err := t.client.Call("list-workspaces", t.params(nil))
	if err != nil {
		return 0, "", 0, t.explain("list-workspaces", err)
	}
	var list struct {
		Workspaces []struct {
			Workspace   int  `json:"workspace"`
			WindowCount int  `json:"window_count"`
			Current     bool `json:"current"`
		} `json:"workspaces"`
		CurrentWorkspace int `json:"current_workspace"`
	}
	if err := jsonx.Unmarshal(raw, &list); err != nil {
		return 0, "", 0, errfmt.Errorf("could not read the workspaces: %w", err)
	}
	current := list.CurrentWorkspace
	name := t.session
	if info, err := t.client.Call("session-info", t.params(nil)); err == nil {
		var s struct {
			Name string `json:"session_name"`
		}
		if jsonx.Unmarshal(info, &s) == nil && s.Name != "" {
			name = s.Name
		}
	}
	if asked != 0 {
		for _, w := range list.Workspaces {
			if w.Workspace != asked {
				continue
			}
			if w.WindowCount > 0 {
				return 0, "", 0, errfmt.Errorf("workspace %d has %d windows. Use an empty workspace, or leave out --workspace", asked, w.WindowCount)
			}
			return asked, name, current, nil
		}
		return 0, "", 0, errfmt.Errorf("workspace %d does not exist. Use a number from 1 to %d", asked, len(list.Workspaces))
	}
	for _, w := range list.Workspaces {
		if w.WindowCount == 0 && !w.Current {
			return w.Workspace, name, current, nil
		}
	}
	return 0, "", 0, errors.New("every workspace has windows. Close the windows on one workspace, then try again")
}

func xpanesClientCommand(t *verbTarget, command string, args []string) error {
	deadline := time.Now().Add(5 * time.Second)
	for {
		_, err := t.client.Call("run-command", t.params(map[string]any{"command": command, "args": args}))
		if err == nil || !strings.Contains(err.Error(), app.ErrNotHereYet) || time.Now().After(deadline) {
			return err
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func xpanesWarning(what string, err error, sessionName string) string {
	var call *session.VerbCallError
	if errors.As(err, &call) && call.Code == session.ErrVerbNeedsClient {
		return fmt.Sprintf("%s, because no client is attached to session %s. Attach with: tuios attach %s", what, sessionName, sessionName)
	}
	msg := err.Error()
	if errors.As(err, &call) {
		msg = call.Message
	}
	return fmt.Sprintf("%s: %s", what, msg)
}

func xpanesSummary(r xpanesResult) string {
	n := len(r.Windows)
	noun := "panes"
	if n == 1 {
		noun = "pane"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Opened %d %s on workspace %d of session %s.", n, noun, r.Workspace, r.Session)
	if r.Arranged {
		fmt.Fprintf(&b, " The layout is %s.", r.Layout)
	}
	if r.Multifocus {
		b.WriteString(" Multifocus is on.")
	}
	return b.String()
}

func xpanesPaneWorkspace(t *verbTarget, paneID string) (int, bool) {
	if paneID == "" {
		return 0, false
	}
	raw, err := t.client.Call("list-windows", t.params(nil))
	if err != nil {
		return 0, false
	}
	var list struct {
		Windows []struct {
			ID        string `json:"window_id"`
			Workspace int    `json:"workspace"`
			Scratch   bool   `json:"scratch"`
		} `json:"windows"`
	}
	if jsonx.Unmarshal(raw, &list) != nil {
		return 0, false
	}
	for _, w := range list.Windows {
		if w.ID == paneID {
			return w.Workspace, w.Scratch && session.IsScratchWorkspace(w.Workspace)
		}
	}
	return 0, false
}
