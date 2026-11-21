package tmuxcompat

import (
	"strings"

	"github.com/Gaurav-Gosain/tuios/internal/errfmt"
)

type tmuxCommand struct {
	name, alias string
}

var tmuxCommands = []tmuxCommand{
	{"attach-session", "attach"},
	{"bind-key", "bind"},
	{"break-pane", "breakp"},
	{"capture-pane", "capturep"},
	{"choose-buffer", ""},
	{"choose-client", ""},
	{"choose-tree", ""},
	{"clear-history", "clearhist"},
	{"clear-prompt-history", "clearphist"},
	{"clock-mode", ""},
	{"command-prompt", ""},
	{"confirm-before", "confirm"},
	{"copy-mode", ""},
	{"customize-mode", ""},
	{"delete-buffer", "deleteb"},
	{"detach-client", "detach"},
	{"display-menu", "menu"},
	{"display-message", "display"},
	{"display-popup", "popup"},
	{"display-panes", "displayp"},
	{"find-window", "findw"},
	{"has-session", "has"},
	{"if-shell", "if"},
	{"join-pane", "joinp"},
	{"kill-pane", "killp"},
	{"kill-server", ""},
	{"kill-session", ""},
	{"kill-window", "killw"},
	{"last-pane", "lastp"},
	{"last-window", "last"},
	{"link-window", "linkw"},
	{"list-buffers", "lsb"},
	{"list-clients", "lsc"},
	{"list-commands", "lscm"},
	{"list-keys", "lsk"},
	{"list-panes", "lsp"},
	{"list-sessions", "ls"},
	{"list-windows", "lsw"},
	{"load-buffer", "loadb"},
	{"lock-client", "lockc"},
	{"lock-server", "lock"},
	{"lock-session", "locks"},
	{"move-pane", "movep"},
	{"move-window", "movew"},
	{"new-session", "new"},
	{"new-window", "neww"},
	{"next-layout", "nextl"},
	{"next-window", "next"},
	{"paste-buffer", "pasteb"},
	{"pipe-pane", "pipep"},
	{"previous-layout", "prevl"},
	{"previous-window", "prev"},
	{"refresh-client", "refresh"},
	{"rename-session", "rename"},
	{"rename-window", "renamew"},
	{"resize-pane", "resizep"},
	{"resize-window", "resizew"},
	{"respawn-pane", "respawnp"},
	{"respawn-window", "respawnw"},
	{"rotate-window", "rotatew"},
	{"run-shell", "run"},
	{"save-buffer", "saveb"},
	{"select-layout", "selectl"},
	{"select-pane", "selectp"},
	{"select-window", "selectw"},
	{"send-keys", "send"},
	{"send-prefix", ""},
	{"server-access", ""},
	{"set-buffer", "setb"},
	{"set-environment", "setenv"},
	{"set-hook", ""},
	{"set-option", "set"},
	{"set-window-option", "setw"},
	{"show-buffer", "showb"},
	{"show-environment", "showenv"},
	{"show-hooks", ""},
	{"show-messages", "showmsgs"},
	{"show-options", "show"},
	{"show-prompt-history", "showphist"},
	{"show-window-options", "showw"},
	{"source-file", "source"},
	{"split-window", "splitw"},
	{"start-server", "start"},
	{"suspend-client", "suspendc"},
	{"swap-pane", "swapp"},
	{"swap-window", "swapw"},
	{"switch-client", "switchc"},
	{"unbind-key", "unbind"},
	{"unlink-window", "unlinkw"},
	{"wait-for", "wait"},
}

func lookupCommand(word string) (string, error) {
	found := ""
	ambiguous := false
	for _, c := range tmuxCommands {
		if c.alias != "" && c.alias == word {
			return c.name, nil
		}
		if !strings.HasPrefix(c.name, word) {
			continue
		}
		if found != "" {
			ambiguous = true
		}
		found = c.name
		if c.name == word {
			break
		}
	}
	if found == "" || word == "" {
		return "", errfmt.Errorf("unknown command: %s", word)
	}
	if !ambiguous {
		return found, nil
	}
	var names []string
	for _, c := range tmuxCommands {
		if strings.HasPrefix(c.name, word) {
			names = append(names, c.name)
		}
	}
	return "", errfmt.Errorf("ambiguous command: %s, could be: %s", word, strings.Join(names, ", "))
}
