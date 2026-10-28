package main

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/atomicfile"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/lookshare"
	"github.com/Gaurav-Gosain/tuios/internal/session"
)

// newLookExportCommand creates `tuios look export`.
func newLookExportCommand() *cobra.Command {
	var out, name, author string
	var all bool
	cmd := &cobra.Command{
		Use:   "export",
		Short: "Print your look as a look file",
		Long: `Print the look in force as a look file. "tuios look apply" takes the file
unchanged.

tuios reads the value of every key that "tuios look keys" lists. The file sets
the keys that differ from the defaults. Use --all to set every key. A theme or
glyph set from your own folder goes into the file. A built-in one goes in by
name.

The author is your GitHub login when gh is signed in. Use --author to set it.`,
		Example: `  tuios look export
  tuios look export -o my.look.toml --name "Neon 84"
  tuios look export --all`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := loadLookConfig()
			if err != nil {
				return err
			}
			if author == "" {
				author = lookshare.Author()
			}
			ex, err := config.ExportLook(cfg, config.LookExportOptions{Name: name, Author: author, All: all})
			if err != nil {
				return err
			}
			for _, n := range ex.Notes {
				fmt.Fprintf(cmd.ErrOrStderr(), "Note: %s.\n", n)
			}
			if out == "" || out == "-" {
				_, err = cmd.OutOrStdout().Write(ex.Text)
				return err
			}
			if err := atomicfile.Write(out, ex.Text, 0o644, false); err != nil {
				return fmt.Errorf("failed to write the look: %w", err)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "tuios wrote the look to %s.\n", out)
			return nil
		},
	}
	cmd.Flags().StringVarP(&out, "output", "o", "", "Write the look to this file instead of stdout")
	cmd.Flags().StringVar(&name, "name", "", "The name of the look")
	cmd.Flags().StringVar(&author, "author", "", "The author of the look (default: your GitHub login)")
	cmd.Flags().BoolVar(&all, "all", false, "Set every look key, also the ones at their default")
	return cmd
}

// newLookShareCommand creates `tuios look share`.
func newLookShareCommand() *cobra.Command {
	var name, author, sessionName string
	var printOnly bool
	cmd := &cobra.Command{
		Use:   "share",
		Short: "Share your look in the setups discussion on GitHub",
		Long: `Share your look in "Show us your tuios setup", discussion #849 on GitHub.

tuios exports your look and takes a screenshot of the session. It puts a
comment with the look on your clipboard and opens the discussion in your
browser. Paste the comment, then drag the screenshot into it.

The running tuios takes the screenshot and writes the clipboard, also over
ssh. With no tuios attached, the clipboard write goes to this terminal and
there is no screenshot. Without a browser, tuios prints the address.

Use --print to write the comment to stdout. tuios then opens nothing.`,
		Example: `  tuios look share
  tuios look share --name "Neon 84"
  tuios look share --print > comment.md`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			w := cmd.OutOrStdout()
			if printOnly {
				comment, _, err := buildLookComment(name, author)
				if err != nil {
					return err
				}
				_, err = io.WriteString(w, comment)
				return err
			}
			res, err := routeLookShare(sessionName, name, author)
			if err != nil {
				if !errors.Is(err, errNoShareClient) {
					return err
				}
				if res, err = shareLookHere(name, author); err != nil {
					return err
				}
			}
			for _, line := range res.lines() {
				fmt.Fprintln(w, line)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "The name of the look")
	cmd.Flags().StringVar(&author, "author", "", "The author of the look (default: your GitHub login)")
	cmd.Flags().BoolVar(&printOnly, "print", false, "Write the comment to stdout and open nothing")
	cmd.Flags().StringVarP(&sessionName, "session", "s", "", "The session to capture (default: this pane's session, else the most recent)")
	_ = cmd.RegisterFlagCompletionFunc("session", completeSessionNames)
	return cmd
}

// loadLookConfig reads the config in force from disk.
func loadLookConfig() (*config.UserConfig, error) {
	mainPath, err := config.GetConfigPath()
	if err != nil {
		return nil, fmt.Errorf("could not determine config path: %w", err)
	}
	return config.LoadLookConfig(mainPath)
}

// buildLookComment exports the look on disk and writes the comment.
func buildLookComment(name, author string) (string, *config.LookExport, error) {
	cfg, err := loadLookConfig()
	if err != nil {
		return "", nil, err
	}
	ex, err := lookshare.Look(cfg, name, author)
	if err != nil {
		return "", nil, err
	}
	return lookshare.Comment(name, lookshare.Summary(cfg), ex.Text), ex, nil
}

// lookShareResult is what a share did.
type lookShareResult struct {
	Path    string `json:"path"`
	Opened  bool   `json:"opened"`
	Copied  bool   `json:"copied"`
	TooLong bool   `json:"too_long"`
}

// lines are what the command prints: at most three lines in the usual case.
func (r lookShareResult) lines() []string {
	var out []string
	if r.Copied {
		out = append(out, lookshare.MsgCopied)
	} else {
		out = append(out, "tuios cannot reach a clipboard. Run \"tuios look share --print\" and copy the comment.")
	}
	if r.Path != "" {
		out = append(out, fmt.Sprintf(lookshare.MsgShot, r.Path))
	} else {
		out = append(out, lookshare.MsgNoShot)
	}
	if !r.Opened {
		out = append(out, fmt.Sprintf(lookshare.MsgOpen, lookshare.DiscussionURL))
	}
	if r.TooLong {
		out = append(out, lookshare.MsgTooLong)
	}
	return out
}

// errNoShareClient says no tuios client can run the share.
var errNoShareClient = errors.New("no tuios client is attached")

// routeLookShare asks the attached client to run the share. It returns
// errNoShareClient when there is no daemon or no client.
func routeLookShare(sessionName, name, author string) (lookShareResult, error) {
	if requireDaemon() != nil {
		return lookShareResult{}, errNoShareClient
	}
	client := session.NewClient(&session.ClientConfig{Version: version})
	if err := client.Connect(); err != nil {
		return lookShareResult{}, errNoShareClient
	}
	defer func() { _ = client.Close() }()
	requestID := uuid.New().String()
	msg, err := session.NewMessage(session.MsgExecuteCommand, &session.ExecuteCommandPayload{
		SessionName: sessionName,
		CommandType: "ShareLook",
		Args:        []string{name, author},
		RequestID:   requestID,
	})
	if err != nil {
		return lookShareResult{}, err
	}
	resp, err := client.SendControlMessage(msg)
	if err != nil {
		return lookShareResult{}, fmt.Errorf("failed to share the look: %w", err)
	}
	var result session.CommandResultPayload
	if resp.Type != session.MsgCommandResult || resp.ParsePayload(&result) != nil {
		return lookShareResult{}, errNoShareClient
	}
	if !result.Success {
		// With no client attached, or no session, the share runs here.
		// Any other failure is the client's answer, and it is the error.
		if strings.Contains(result.Message, "requires an attached client") || result.Message == "session not found" {
			return lookShareResult{}, errNoShareClient
		}
		return lookShareResult{}, fmt.Errorf("tuios cannot share the look: %s", result.Message)
	}
	var res lookShareResult
	data, err := json.Marshal(result.Data)
	if err == nil {
		err = json.Unmarshal(data, &res)
	}
	return res, err
}

// shareLookHere runs the share in this process, when no client can: the
// clipboard write goes to this terminal and the native clipboard, the daemon
// captures the focused pane when it can, and the browser opens here.
func shareLookHere(name, author string) (lookShareResult, error) {
	comment, ex, err := buildLookComment(name, author)
	if err != nil {
		return lookShareResult{}, err
	}
	res := lookShareResult{TooLong: lookshare.TooLong(ex.Text)}
	res.Copied = copyCommentHere(comment)
	res.Path = shotHere()
	if !viewerOverSSH() {
		opener := ""
		if cfg, err := loadLookConfig(); err == nil {
			opener = cfg.Appearance.LinkOpener
		}
		res.Opened = app.OpenWebLink(opener, lookshare.DiscussionURL) == nil
	}
	return res, nil
}

// copyCommentHere writes the comment to the clipboard through this terminal
// (OSC 52) and the native clipboard tool, and reports whether either ran.
func copyCommentHere(text string) bool {
	copied := false
	if tty, err := os.OpenFile("/dev/tty", os.O_WRONLY, 0); err == nil {
		if term.IsTerminal(int(tty.Fd())) {
			_, werr := fmt.Fprintf(tty, "\x1b]52;c;%s\x07", base64.StdEncoding.EncodeToString([]byte(text)))
			copied = werr == nil
		}
		_ = tty.Close()
	}
	if !viewerOverSSH() {
		if tool := app.DetectClipboardTool(); tool != nil && tool.Write(text) == nil {
			copied = true
		}
	}
	return copied
}

// shotHere asks the daemon for a picture of the focused pane, and returns
// its path, or "" when there is no daemon or no pane.
func shotHere() string {
	if requireDaemon() != nil {
		return ""
	}
	path, err := lookshare.ShotPath(time.Now())
	if err != nil {
		return ""
	}
	client, err := session.DialVerbClientAs(version)
	if err != nil {
		return ""
	}
	defer func() { _ = client.Close() }()
	raw, err := client.Call("screenshot", map[string]any{"format": "png", "out": path})
	if err != nil {
		return ""
	}
	var res struct {
		Path string `json:"path"`
	}
	if json.Unmarshal(raw, &res) != nil {
		return ""
	}
	return res.Path
}

// viewerOverSSH reports whether the person reached this machine over ssh, so
// a browser or a clipboard tool here is not theirs.
func viewerOverSSH() bool {
	for _, key := range []string{"SSH_CONNECTION", "SSH_CLIENT", "SSH_TTY"} {
		if os.Getenv(key) != "" {
			return true
		}
	}
	return false
}
