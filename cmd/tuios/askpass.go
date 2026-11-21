package main

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

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
	// a prompt to fail at once rather than wait for a person. git runs
	// SSH_ASKPASS before it looks at the variable, so the program does.
	if os.Getenv("GIT_TERMINAL_PROMPT") == "0" {
		fmt.Fprintln(os.Stderr, "tuios: GIT_TERMINAL_PROMPT=0 is set, so nothing was asked.")
		return 1
	}
	tty, onTerminal := foregroundTerminal()
	if onTerminal {
		defer func() { _ = tty.Close() }()
	}
	switch {
	case kind == "none":
		if onTerminal {
			_, _ = tty.WriteString(prompt + "\n")
		}
		return 0
	case kind == "confirm" && onTerminal:
		return askpassConfirm(tty, prompt)
	case kind == "confirm":
		fmt.Fprintln(os.Stderr, "tuios: the Inbox cannot ask this yes or no question yet. Run the command in a terminal.")
		return 1
	case onTerminal:
		return askpassOnTerminal(tty, prompt)
	}
	sock := os.Getenv("TUIOS_SOCKET")
	if sock == "" {
		fmt.Fprintln(os.Stderr, "tuios: no tuios daemon can ask for this. Run the command in a terminal.")
		return 1
	}
	client, err := session.DialVerbClientAt(sock, "")
	if err != nil {
		fmt.Fprintf(os.Stderr, "tuios: the tuios daemon does not answer (%v). Run the command in a terminal.\n", err)
		return 1
	}
	defer func() { _ = client.Close() }()
	raw, err := client.CallWithTimeout("askpass", map[string]any{"prompt": prompt}, askpassWait)
	if err != nil {
		fmt.Fprintf(os.Stderr, "tuios: the prompt could not be shown: %v.\n", err)
		return 1
	}
	var res struct {
		Status string `json:"status"`
		Secret []byte `json:"secret"`
	}
	err = jsonx.Unmarshal(raw, &res)
	clear(raw)
	defer clear(res.Secret)
	if err != nil || res.Status != "answered" {
		fmt.Fprintln(os.Stderr, "tuios: "+askpassEnded(res.Status))
		return 1
	}
	out := append(res.Secret, '\n')
	_, err = os.Stdout.Write(out)
	clear(out)
	if err != nil {
		return 1
	}
	return 0
}

// askpassEnded says why a prompt has no answer.
func askpassEnded(status string) string {
	switch status {
	case "cancelled", "dismissed":
		return "the prompt was cancelled. Nothing was sent."
	case "timeout":
		return "nobody answered the prompt in 5 minutes. Nothing was sent."
	case "":
		return "the tuios daemon gave no answer. Nothing was sent."
	}
	return "the prompt ended (" + strings.ReplaceAll(status, "_", " ") + "). Nothing was sent."
}

// askpassConfirm asks a yes or no question on tty. Yes is exit status 0.
func askpassConfirm(tty *os.File, prompt string) int {
	if _, err := tty.WriteString(prompt + " (yes/no) "); err != nil {
		return 1
	}
	line, _ := bufio.NewReader(tty).ReadString('\n')
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "yes", "y":
		return 0
	}
	return 1
}

// askpassOnTerminal asks on tty as ssh does: a yes or no question and a user
// name with echo, anything else with echo off.
func askpassOnTerminal(tty *os.File, prompt string) int {
	if _, err := tty.WriteString(prompt); err != nil {
		return 1
	}
	lower := strings.ToLower(prompt)
	if strings.Contains(lower, "yes/no") || strings.HasPrefix(lower, "username") {
		line, err := bufio.NewReader(tty).ReadString('\n')
		if err != nil && line == "" {
			return 1
		}
		fmt.Println(strings.TrimRight(line, "\r\n"))
		return 0
	}
	secret, err := readHidden(tty)
	_, _ = tty.WriteString("\n")
	defer clear(secret)
	if err != nil {
		return 1
	}
	out := append(secret, '\n')
	_, err = os.Stdout.Write(out)
	clear(out)
	if err != nil {
		return 1
	}
	return 0
}
