package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"slices"
	"strings"

	"github.com/Gaurav-Gosain/tuios/internal/childproc"
	"github.com/Gaurav-Gosain/tuios/internal/integration"
)

// takeOmarchy splits "omarchy" off the harness arguments.
func takeOmarchy(args []string) ([]string, bool) {
	if !slices.Contains(args, integration.OmarchyID) {
		return args, false
	}
	return slices.DeleteFunc(slices.Clone(args), func(a string) bool { return a == integration.OmarchyID }), true
}

// omarchyRunner runs omarchy-shell and omarchy from PATH. It is nil when
// omarchy-shell is not there, which means the Omarchy shell is not installed.
func omarchyRunner() integration.OmarchyRunner {
	if _, err := exec.LookPath("omarchy-shell"); err != nil {
		return nil
	}
	return func(name string, args ...string) (string, error) {
		out, err := childproc.Command(name, args...).CombinedOutput()
		if msg := strings.TrimSpace(string(out)); err != nil && msg != "" {
			err = errors.New(msg)
		}
		return string(out), err
	}
}

// selfPath is the path of this tuios binary, for the commands the widget and
// the menu row run.
func selfPath() string {
	if p, err := os.Executable(); err == nil {
		return p
	}
	return "tuios"
}

// runOmarchyInstall is integration install omarchy.
func runOmarchyInstall(force bool) error {
	res, err := integration.OmarchyInstall(integration.SystemEnv(), selfPath(), omarchyRunner(), force)
	for _, n := range res.Notes {
		fmt.Println(n)
	}
	return err
}

// runOmarchyUninstall is integration uninstall omarchy.
func runOmarchyUninstall() error {
	res, err := integration.OmarchyUninstall(integration.SystemEnv(), selfPath(), omarchyRunner())
	for _, n := range res.Notes {
		fmt.Println(n)
	}
	if err == nil && len(res.Notes) == 0 {
		fmt.Println("The Omarchy integration was not installed.")
	}
	return err
}

// runOmarchyStatus is integration status omarchy.
func runOmarchyStatus(w io.Writer) error {
	st, err := integration.OmarchyStatusOf(integration.SystemEnv(), selfPath())
	if err != nil {
		return err
	}
	if !st.Installed {
		fmt.Fprintln(w, "Omarchy: not set up here ("+st.Dir+" is missing).")
	}
	fmt.Fprintln(w, "Bar widget: "+map[string]string{
		"missing": "not there. Run tuios integration install omarchy.",
		"current": "current, in " + st.Plugin + ".",
		"older":   "from another tuios, in " + st.Plugin + ". Run tuios integration install omarchy to update it.",
		"foreign": st.Plugin + " holds a plugin tuios did not write.",
	}[st.PluginState])
	fmt.Fprintln(w, "Menu: "+map[string]string{
		"marked":  "the tuios row is in " + st.Menu + ".",
		"by_hand": st.Menu + " has a tuios row of your own.",
		"missing": "no tuios row in " + st.Menu + ".",
	}[st.MenuRow])
	return nil
}
