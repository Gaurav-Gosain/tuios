package main

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/errfmt"
	"github.com/Gaurav-Gosain/tuios/internal/jsonx"
	"github.com/Gaurav-Gosain/tuios/internal/session"
)

// tuios's askpass program.
//
// A pane is started with SSH_ASKPASS naming the tuios-askpass link, and
// SSH_ASKPASS_REQUIRE=force, so ssh runs this binary by that name for every
// passphrase, password and host key question, with the prompt as its
// argument (session.Manager.AskpassEnv). git runs it the same way for an
// https password when nothing else is set. It prints the answer on stdout,
// or exits 1 to say there is none, which ssh takes as a refusal.
//
// It asks where the person can answer:
//
//   - When the program that asks is the foreground job of a terminal, a
//     person typed it there, so it asks on that terminal, as ssh would: the
//     prompt, then the answer with echo off. A person who types ssh in a
//     shell pane sees no change.
//   - Otherwise nobody types into that terminal for this program, which is
//     the case of an agent's command, so it asks the daemon (the askpass
//     verb). The daemon puts the prompt in the Inbox, and the person answers
//     in a dialog of their client.

// isAskpassName reports whether the binary was run by the askpass link's name.
func isAskpassName(arg0 string) bool {
	return strings.TrimSuffix(filepath.Base(arg0), ".exe") == session.AskpassLinkName
}

// askpassWait is how long the program waits for the person: the daemon's
// own wait, and a little for the call itself.
const askpassWait = 5*time.Minute + 15*time.Second

// runAsAskpass answers one prompt and returns the exit status.
func runAsAskpass(args []string) int {
	prompt := strings.Join(args, " ")
	// OpenSSH says what kind of prompt it is in SSH_ASKPASS_PROMPT. "none"
	// is a notice, such as "Confirm user presence for key ...", which ssh
	// stops on its own once the key is touched, and "confirm" is a yes or no
	// question answered by the exit status.
	kind := os.Getenv("SSH_ASKPASS_PROMPT")
	// A tool that sets GIT_TERMINAL_PROMPT=0, as go mod download does, wants
	// a prompt with nobody at the terminal to fail at once. git runs
	// SSH_ASKPASS before it looks at the variable, so this program does. A
	// person at the terminal of a plain shell pane is still asked there, as
	// ssh would.
	noInbox := os.Getenv("GIT_TERMINAL_PROMPT") == "0"
	tty, onTerminal := foregroundTerminal()
	if onTerminal {
		defer func() { _ = tty.Close() }()
	}
	why := ""
	switch {
	case onTerminal && (kind == "none" || kind == "confirm"):
		// Neither goes to the Inbox, so there is nothing to ask the daemon.
		return askpassOnTerminal(tty, prompt, kind)
	case kind == "none":
		return 0
	case kind == "confirm":
		why = "the Inbox cannot ask a yes or no question yet"
	case noInbox && !onTerminal:
		why = "GIT_TERMINAL_PROMPT=0 is set"
	default:
		// The daemon knows the pane. In a pane that runs no agent, a
		// foreground job is the person's, whatever wraps ssh, so it says to
		// ask on the terminal. In an agent's pane the terminal is the
		// agent's, and the prompt goes to the Inbox.
		secret, status, err := askpassDaemon(prompt, onTerminal, noInbox)
		switch {
		case err == nil && status == "answered":
			return askpassPrint(secret)
		case onTerminal && (err != nil || status == "terminal"):
			return askpassOnTerminal(tty, prompt, kind)
		case err != nil:
			why = err.Error()
		case status == "no_inbox":
			why = "GIT_TERMINAL_PROMPT=0 is set"
		default:
			why = "the prompt ended with no answer: " + strings.ReplaceAll(status, "_", " ")
		}
	}
	fmt.Fprintln(os.Stderr, "tuios: nothing was asked or sent, because "+why+". Run the command in a terminal to answer it there.")
	return 1
}

// askpassDaemon asks the daemon for the answer to prompt: it goes in the
// Inbox, and the person answers in the secret dialog. It returns the
// answer and the daemon's status: answered, terminal (ask on the terminal
// instead), no_inbox, or how the prompt ended.
func askpassDaemon(prompt string, terminal, noInbox bool) ([]byte, string, error) {
	sock := os.Getenv("TUIOS_SOCKET")
	if sock == "" {
		return nil, "", errors.New("no tuios daemon runs this pane")
	}
	client, err := session.DialVerbClientAt(sock, "")
	if err != nil {
		return nil, "", errfmt.Errorf("the tuios daemon does not answer (%w)", err)
	}
	defer func() { _ = client.Close() }()
	raw, err := client.CallWithTimeout("askpass", map[string]any{"prompt": prompt, "terminal": terminal, "no_inbox": noInbox}, askpassWait)
	if err != nil {
		return nil, "", errfmt.Errorf("the prompt could not be shown (%w)", err)
	}
	var res struct {
		Status string `json:"status"`
		Secret []byte `json:"secret"`
	}
	err = jsonx.Unmarshal(raw, &res)
	clear(raw)
	if err != nil || res.Status != "answered" {
		clear(res.Secret)
		return nil, res.Status, err
	}
	return res.Secret, res.Status, nil
}

// askpassPrint writes the answer for the program that asks, and zeroes it.
func askpassPrint(answer []byte) int {
	out := append(answer, '\n')
	_, err := os.Stdout.Write(out)
	clear(out)
	clear(answer)
	if err != nil {
		return 1
	}
	return 0
}

// askpassOnTerminal asks on tty as ssh does. A notice is shown and asks
// nothing. A yes or no question and a user name are read with echo, and a
// confirm answers by the exit status. Anything else is read with echo off.
func askpassOnTerminal(tty *os.File, prompt, kind string) int {
	lower := strings.ToLower(prompt)
	switch {
	case kind == "none":
		_, _ = tty.WriteString(prompt + "\n")
		return 0
	case kind == "confirm" && !strings.Contains(lower, "yes/no"):
		prompt += " (yes/no) "
	}
	if _, err := tty.WriteString(prompt); err != nil {
		return 1
	}
	if kind == "confirm" || strings.Contains(lower, "yes/no") || strings.HasPrefix(lower, "username") {
		line, _ := bufio.NewReader(tty).ReadString('\n')
		line = strings.TrimRight(line, "\r\n")
		if kind != "confirm" {
			return askpassPrint([]byte(line))
		}
		if answer := strings.ToLower(strings.TrimSpace(line)); answer == "yes" || answer == "y" {
			return 0
		}
		return 1
	}
	secret, err := readHidden(tty)
	_, _ = tty.WriteString("\n")
	if err != nil {
		return 1
	}
	return askpassPrint(secret)
}
