package web

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"

	"github.com/charmbracelet/colorprofile"
	xpty "github.com/charmbracelet/x/xpty"
)

// Session represents a terminal session.
type Session struct {
	ID         string
	Pty        xpty.Pty
	Cmd        *exec.Cmd
	Cols       int
	Rows       int
	cancelFunc context.CancelFunc
	ctx        context.Context
	mu         sync.Mutex
	closed     bool
	startTime  time.Time
}

// Done returns a channel that is closed when the session ends.
func (s *Session) Done() <-chan struct{} {
	return s.ctx.Done()
}

func (s *Server) createSession(ctx context.Context) (*Session, error) {
	cols, rows := 80, 24

	logger.Debug("creating PTY", "cols", cols, "rows", rows)

	pty, err := xpty.NewPty(cols, rows)
	if err != nil {
		return nil, fmt.Errorf("failed to create PTY: %w", err)
	}

	// Run TUIOS with forwarded arguments
	// Find the tuios binary (sibling to tuios-web)
	executable, err := os.Executable()
	if err != nil {
		_ = pty.Close()
		return nil, fmt.Errorf("failed to find executable: %w", err)
	}

	// Get the directory containing tuios-web and look for tuios
	execDir := filepath.Dir(executable)
	tuiosBinary := filepath.Join(execDir, "tuios")
	
	// Check if tuios exists in the same directory
	if _, err := os.Stat(tuiosBinary); err != nil {
		// Fallback: try to find tuios in PATH
		tuiosBinary, err = exec.LookPath("tuios")
		if err != nil {
			_ = pty.Close()
			return nil, fmt.Errorf("failed to find tuios binary (looked in %s and PATH): %w", execDir, err)
		}
	}

	// Build command with forwarded args
	args := s.config.TuiosArgs
	cmd := exec.Command(tuiosBinary, args...)

	logger.Debug("starting TUIOS",
		"executable", tuiosBinary,
		"args", args,
	)

	// Platform-specific PTY setup
	setupPTYCommand(cmd)

	// Set terminal environment
	termType, colorTerm := getTerminalEnv()
	cmd.Env = append(os.Environ(),
		"TERM="+termType,
		"COLORTERM="+colorTerm,
		"TERM_PROGRAM=TUIOS-Web",
		"TERM_PROGRAM_VERSION=0.1.0",
	)

	if err := pty.Start(cmd); err != nil {
		_ = pty.Close()
		return nil, fmt.Errorf("failed to start TUIOS: %w", err)
	}

	sessionCtx, cancel := context.WithCancel(ctx)
	session := &Session{
		ID:         fmt.Sprintf("%d", time.Now().UnixNano()),
		Pty:        pty,
		Cmd:        cmd,
		Cols:       cols,
		Rows:       rows,
		cancelFunc: cancel,
		ctx:        sessionCtx,
		startTime:  time.Now(),
	}

	// Monitor process exit and cancel context when done
	go func() {
		_ = xpty.WaitProcess(sessionCtx, cmd)
		logger.Debug("TUIOS process exited", "session", session.ID)
		cancel()
	}()

	s.sessions.Store(session.ID, session)

	logger.Debug("session created",
		"session", session.ID,
		"pid", cmd.Process.Pid,
	)

	return session, nil
}

func (s *Server) closeSession(session *Session) {
	session.mu.Lock()
	if session.closed {
		session.mu.Unlock()
		return
	}
	session.closed = true
	session.mu.Unlock()

	duration := time.Since(session.startTime)

	session.cancelFunc()

	if session.Pty != nil {
		_ = session.Pty.Close()
	}

	if session.Cmd != nil && session.Cmd.Process != nil {
		_ = session.Cmd.Process.Kill()
	}

	s.sessions.Delete(session.ID)

	logger.Debug("session closed",
		"session", session.ID,
		"duration", duration.Round(time.Millisecond),
	)
}

func getTerminalEnv() (termType, colorTerm string) {
	profile := colorprofile.Detect(os.Stdout, os.Environ())

	switch profile {
	case colorprofile.TrueColor:
		return "xterm-256color", "truecolor"
	case colorprofile.ANSI256:
		return "xterm-256color", ""
	case colorprofile.ANSI:
		return "xterm", ""
	default:
		return "xterm-256color", "truecolor"
	}
}
