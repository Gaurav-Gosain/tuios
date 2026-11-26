package progresume

import (
	"errors"
	"fmt"
	"io"
	"os"
	"sync"
	"time"
)

// ErrNoToken is what Report and Clear return when the terminal gave this
// process no token. The terminal does not support the protocol, or another
// process already took the token. A program can ignore it.
var ErrNoToken = errors.New("progresume: the terminal gave no " + TokenEnv)

// ErrInvalid is what Report returns for a report that breaks a rule of the
// protocol. Nothing was sent.
var ErrInvalid = errors.New("progresume: the report is not valid")

// Reporter sends reports for one program, with the token, pid and output
// its fields name. The package functions Report and Clear share one Reporter
// made from the environment.
type Reporter struct {
	// Token is the terminal's token.
	Token string
	// PID is the owner the reports name: the process the terminal should
	// watch. Report and Clear use the pid of this process.
	PID int
	// Out is where the reports go: the controlling terminal for Report and
	// Clear.
	Out io.Writer
	// Now is the clock. nil uses time.Now.
	Now func() time.Time
}

// Report tells the terminal how to start the program again: argv is the
// command, with argv[0] a bare name that the terminal looks up on PATH or an
// absolute path. cwd is the folder to start it in, "" for none. A report
// that breaks a rule of the protocol is not sent, and the error says which.
func (r *Reporter) Report(argv []string, cwd string) error {
	return r.send(Message{Argv: argv, Cwd: cwd})
}

// Clear tells the terminal that the program has nothing to resume, for
// example when the person quit it on purpose.
func (r *Reporter) Clear() error {
	return r.send(Message{Clear: true})
}

func (r *Reporter) send(rep Message) error {
	if r == nil || !ValidToken(r.Token) {
		return ErrNoToken
	}
	now := time.Now
	if r.Now != nil {
		now = r.Now
	}
	t := now()
	rep.PID, rep.Time = r.PID, t.Unix()
	seq := Sequence(rep, r.Token)
	// The report is checked as a terminal would check it, so a program finds
	// out here that a report breaks a rule, and not by a missing resume.
	if res := Parse([]byte(seq[2:len(seq)-2]), r.Token, t); res.Outcome != Accepted {
		return fmt.Errorf("%w: %s", ErrInvalid, res.Reason)
	}
	_, err := io.WriteString(r.Out, seq)
	return err
}

var (
	envOnce     sync.Once
	envReporter *Reporter
	envErr      error
)

// fromEnv is the Reporter of this process. The first call reads the token
// and removes it from the environment, so no process this one starts can
// report in its place.
func fromEnv() (*Reporter, error) {
	envOnce.Do(func() {
		tok := os.Getenv(TokenEnv)
		_ = os.Unsetenv(TokenEnv)
		if !ValidToken(tok) {
			envErr = ErrNoToken
			return
		}
		out, err := openTTY()
		if err != nil {
			envErr = err
			return
		}
		envReporter = &Reporter{Token: tok, PID: os.Getpid(), Out: out}
	})
	return envReporter, envErr
}

// TakeToken removes OSC7577_TOKEN from the environment of this process
// without sending anything, so the programs it starts do not get the token.
// Report and Clear still work after it.
func TakeToken() {
	_, _ = fromEnv()
}

// Report tells the terminal how to start this program again. See
// Reporter.Report. The first call of Report, Clear or TakeToken takes the
// token out of the environment. ErrNoToken means the terminal gave none.
func Report(argv []string, cwd string) error {
	r, err := fromEnv()
	if err != nil {
		return err
	}
	return r.Report(argv, cwd)
}

// Clear tells the terminal that this program has nothing to resume. See
// Reporter.Clear.
func Clear() error {
	r, err := fromEnv()
	if err != nil {
		return err
	}
	return r.Clear()
}
