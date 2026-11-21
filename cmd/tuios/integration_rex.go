package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"slices"
	"strings"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/integration"
)

func takeRex(args []string) ([]string, bool) {
	if !slices.Contains(args, integration.RexID) {
		return args, false
	}
	return slices.DeleteFunc(slices.Clone(args), func(a string) bool { return a == integration.RexID }), true
}

func rexRunner() (integration.RexRunner, error) {
	path, err := exec.LookPath("rex")
	if err != nil {
		return nil, errors.New("rex is not on PATH. Install Rex, or put its rex command on PATH, then run this again")
	}
	return func(args ...string) (string, error) {
		cmd := exec.Command(path, args...)
		var out, errOut bytes.Buffer
		cmd.Stdout, cmd.Stderr = &out, &errOut
		if err := cmd.Run(); err != nil {
			msg := strings.TrimSpace(errOut.String())
			if msg == "" {
				msg = strings.TrimSpace(out.String())
			}
			if msg == "" {
				msg = err.Error()
			}
			return out.String(), errors.New(msg)
		}
		return out.String(), nil
	}, nil
}

func runRexInstall(force bool) error {
	rex, err := rexRunner()
	if err != nil {
		return err
	}
	res, err := integration.RexInstall(integration.SystemEnv(), rex, force)
	for _, n := range res.Notes {
		fmt.Println(n)
	}
	if err != nil {
		return err
	}
	printRexCheck(os.Stdout, res.Check)
	return nil
}

func runRexUninstall() error {
	rex, err := rexRunner()
	if err != nil {
		rex = func(...string) (string, error) { return "", errors.New("rex is not on PATH") }
	}
	res, err := integration.RexUninstall(integration.SystemEnv(), rex)
	for _, n := range res.Notes {
		fmt.Println(n)
	}
	if err == nil && len(res.Notes) == 0 {
		fmt.Println("The Rex integration was not installed.")
	}
	return err
}

func runRexStatus(w io.Writer) error {
	rex, err := rexRunner()
	if err != nil {
		rex = nil
	}
	st, err := integration.RexStatusOf(integration.SystemEnv(), rex)
	if err != nil {
		return err
	}
	fmt.Fprintf(w, "Rex config: %s\n", st.Config)
	switch st.LuaState {
	case "missing":
		fmt.Fprintf(w, "tuios.lua: not there. Run tuios integration install rex.\n")
	case "current":
		fmt.Fprintf(w, "tuios.lua: current (version %d).\n", st.LuaVersion)
	case "older":
		fmt.Fprintf(w, "tuios.lua: version %d, older than this tuios ships. Run tuios integration install rex to update it.\n", st.LuaVersion)
	case "edited":
		fmt.Fprintf(w, "tuios.lua: has changes of your own. tuios integration install rex --force replaces it.\n")
	default:
		fmt.Fprintf(w, "tuios.lua: a file tuios did not write.\n")
	}
	switch st.Line {
	case "marked":
		fmt.Fprintf(w, "init.lua: requires tuios (the line install added).\n")
	case "by_hand":
		fmt.Fprintf(w, "init.lua: requires tuios on a line of your own.\n")
	default:
		fmt.Fprintf(w, "init.lua: does not require tuios, so Rex has no tuios actions.\n")
	}
	switch {
	case st.LoadedNote != "":
		fmt.Fprintf(w, "Rex: could not ask which actions it has: %s.\n", st.LoadedNote)
	case !st.Loaded:
		fmt.Fprintf(w, "Rex: the tuios actions are not loaded. Run rex config reload.\n")
	default:
		fmt.Fprintf(w, "Rex: the tuios actions are loaded.\n")
		if st.Check == nil {
			fmt.Fprintf(w, "%s.\n", st.CheckNote)
		}
		printRexCheck(w, st.Check)
	}
	fmt.Fprintln(w, rexStatusRecords())
	return nil
}

func rexStatusRecords() string {
	mode := config.HostProgramStatusSession
	cfg, err := config.LoadExistingUserConfig()
	switch {
	case err != nil:
		return "Status in Rex: config.toml could not be read, so it is the default, one record for each tuios session."
	case cfg != nil:
		mode = config.NormalizeHostProgramStatus(cfg.Agents.HostProgramStatus)
	}
	switch mode {
	case config.HostProgramStatusPanes:
		return "Status in Rex: one record for each tuios pane with an agent (host_program_status = \"panes\")."
	case config.HostProgramStatusOff:
		return "Status in Rex: none (host_program_status = \"off\")."
	}
	return "Status in Rex: one record for each tuios session. For one per agent, set host_program_status = \"panes\" under [agents] (docs/REX.md, \"Per-pane status\")."
}

func printRexCheck(w io.Writer, c *integration.RexCheck) {
	if c == nil {
		return
	}
	version, _, _ := strings.Cut(strings.TrimSpace(c.Version), "\n")
	fmt.Fprintf(w, "The actions run %s (%s).\n", c.Tuios, version)
	switch {
	case c.ActionsNote != "":
		fmt.Fprintf(w, "Rex has only the built-in tuios actions: %s\n", c.ActionsNote)
	case c.ActionsCurrent != nil && !*c.ActionsCurrent:
		fmt.Fprintf(w, "Rex has %d tuios actions from an older tuios. Run rex config reload to load the actions of this one.\n", c.Actions)
	case c.Actions > 0:
		fmt.Fprintf(w, "Rex has %d tuios actions, the ones tuios actions lists.\n", c.Actions)
	}
	if c.KeysNote != "" {
		fmt.Fprintf(w, "Keys: %s\n", c.KeysNote)
	}
	if c.Agent != "" {
		switch {
		case c.AgentInstalled == nil:
			fmt.Fprintf(w, "Could not check %s: %s\n", c.Agent, c.AgentNote)
		case *c.AgentInstalled:
			fmt.Fprintf(w, "Start an Agent starts %s: tuios finds it.\n", c.Agent)
		default:
			fmt.Fprintf(w, "Start an Agent starts %s, and tuios does not find it on the daemon's PATH. Install it, or start the daemon from a shell that has it on PATH.\n", c.Agent)
		}
	}
}
