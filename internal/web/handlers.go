package web

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"
	"github.com/quic-go/webtransport-go"
)

// Message types for WebSocket/WebTransport communication.
const (
	MsgInput   = '0' // Terminal input (client -> server)
	MsgOutput  = '1' // Terminal output (server -> client)
	MsgResize  = '2' // Resize terminal
	MsgPing    = '3' // Ping
	MsgPong    = '4' // Pong
	MsgTitle   = '5' // Set window title
	MsgOptions = '6' // Configuration options
	MsgClose   = '7' // Session closed (server -> client)
)

// Buffer sizes
const (
	readBufSize  = 16 * 1024 // 16KB read buffer
	writeBufSize = 16*1024 + 5
)

// Buffer pools to reduce allocations
var (
	readBufPool = sync.Pool{
		New: func() any {
			b := make([]byte, readBufSize)
			return &b
		},
	}
	writeBufPool = sync.Pool{
		New: func() any {
			b := make([]byte, writeBufSize)
			return &b
		},
	}
	// Pool for variable-size input messages (tiered)
	smallBufPool = sync.Pool{
		New: func() any {
			b := make([]byte, 256)
			return &b
		},
	}
)

// ResizeMessage is sent when the terminal should be resized.
type ResizeMessage struct {
	Cols int `json:"cols"`
	Rows int `json:"rows"`
}

// OptionsMessage is sent to configure the terminal.
type OptionsMessage struct {
	ReadOnly bool `json:"readOnly"`
}

func (s *Server) handleWebSocket(w http.ResponseWriter, r *http.Request) {
	if !s.checkConnectionLimit() {
		http.Error(w, "Maximum connections reached", http.StatusServiceUnavailable)
		return
	}
	defer s.releaseConnection()

	logger.Info("WebSocket connection attempt",
		"remote", r.RemoteAddr,
		"user_agent", r.UserAgent(),
	)

	opts := &websocket.AcceptOptions{
		OriginPatterns: s.config.AllowOrigins,
	}
	if len(s.config.AllowOrigins) == 0 {
		opts.OriginPatterns = []string{"*"}
	}

	conn, err := websocket.Accept(w, r, opts)
	if err != nil {
		logger.Error("WebSocket accept failed", "err", err, "remote", r.RemoteAddr)
		return
	}
	defer func() { _ = conn.CloseNow() }()

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	// Wait for first message to get initial terminal size
	// The browser should send a resize message immediately after connecting
	cols, rows := 80, 24 // defaults
	
	// Read first message with timeout
	readCtx, readCancel := context.WithTimeout(ctx, 5*time.Second)
	_, data, err := conn.Read(readCtx)
	readCancel()
	
	if err == nil && len(data) > 0 && data[0] == MsgResize {
		var resize ResizeMessage
		if err := json.Unmarshal(data[1:], &resize); err == nil {
			cols = resize.Cols
			rows = resize.Rows
			logger.Debug("got initial size from browser", "cols", cols, "rows", rows)
		}
	}

	startTime := time.Now()
	// Create session with actual dimensions from browser
	session, err := s.createSession(ctx, cols, rows)
	if err != nil {
		logger.Error("session creation failed", "err", err, "remote", r.RemoteAddr)
		_ = conn.Close(websocket.StatusInternalError, err.Error())
		return
	}
	defer func() {
		s.closeSession(session)
		logger.Info("WebSocket session ended",
			"session", session.ID,
			"remote", r.RemoteAddr,
			"duration", time.Since(startTime).Round(time.Second),
		)
	}()

	logger.Info("WebSocket session started",
		"session", session.ID,
		"remote", r.RemoteAddr,
		"cols", session.Cols,
		"rows", session.Rows,
	)

	// Send initial options
	optionsData, _ := json.Marshal(OptionsMessage{ReadOnly: s.config.ReadOnly})
	_ = conn.Write(ctx, websocket.MessageBinary, append([]byte{MsgOptions}, optionsData...))

	var wg sync.WaitGroup
	wg.Add(2)

	// Output -> WebSocket
	go func() {
		defer wg.Done()
		defer cancel()
		s.streamPTYToWebSocket(ctx, conn, session)
	}()

	// WebSocket -> Input
	go func() {
		defer wg.Done()
		defer cancel()
		s.handleWebSocketInput(ctx, conn, session)
	}()

	wg.Wait()
}

func (s *Server) handleWebTransport(w http.ResponseWriter, r *http.Request) {
	if !s.checkConnectionLimit() {
		http.Error(w, "Maximum connections reached", http.StatusServiceUnavailable)
		return
	}
	defer s.releaseConnection()

	logger.Info("WebTransport connection attempt",
		"remote", r.RemoteAddr,
		"protocol", r.Proto,
	)

	wtSession, err := s.wtServer.Upgrade(w, r)
	if err != nil {
		logger.Error("WebTransport upgrade failed", "err", err, "remote", r.RemoteAddr)
		return
	}
	defer func() { _ = wtSession.CloseWithError(0, "session closed") }()

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	stream, err := wtSession.AcceptStream(ctx)
	if err != nil {
		logger.Error("stream accept failed", "err", err)
		return
	}
	defer func() { _ = stream.Close() }()

	// Wait for first message to get initial terminal size
	cols, rows := 80, 24 // defaults
	
	// Read first framed message
	lenBuf := make([]byte, 4)
	if _, err := io.ReadFull(stream, lenBuf); err == nil {
		length := binary.BigEndian.Uint32(lenBuf)
		if length < 1024 {
			data := make([]byte, length)
			if _, err := io.ReadFull(stream, data); err == nil && len(data) > 0 && data[0] == MsgResize {
				var resize ResizeMessage
				if err := json.Unmarshal(data[1:], &resize); err == nil {
					cols = resize.Cols
					rows = resize.Rows
					logger.Debug("got initial size from browser (WT)", "cols", cols, "rows", rows)
				}
			}
		}
	}

	startTime := time.Now()
	// Create session with actual dimensions from browser
	session, err := s.createSession(ctx, cols, rows)
	if err != nil {
		logger.Error("session creation failed", "err", err, "remote", r.RemoteAddr)
		return
	}
	defer func() {
		s.closeSession(session)
		logger.Info("WebTransport session ended",
			"session", session.ID,
			"remote", r.RemoteAddr,
			"duration", time.Since(startTime).Round(time.Second),
		)
	}()

	logger.Info("WebTransport session started",
		"session", session.ID,
		"remote", r.RemoteAddr,
		"cols", session.Cols,
		"rows", session.Rows,
	)

	// Send initial options (framed)
	optionsData, _ := json.Marshal(OptionsMessage{ReadOnly: s.config.ReadOnly})
	_ = writeFramed(stream, append([]byte{MsgOptions}, optionsData...))

	var wg sync.WaitGroup
	wg.Add(2)

	// Output -> WebTransport
	go func() {
		defer wg.Done()
		defer cancel()
		s.streamPTYToWebTransport(ctx, stream, session)
	}()

	// WebTransport -> Input
	go func() {
		defer wg.Done()
		defer cancel()
		s.handleWebTransportInput(ctx, stream, session)
	}()

	wg.Wait()
	<-wtSession.Context().Done()
}

// streamPTYToWebSocket reads from PTY and writes directly to WebSocket.
func (s *Server) streamPTYToWebSocket(ctx context.Context, conn *websocket.Conn, session *Session) {
	// Get pooled buffers
	bufPtr := readBufPool.Get().(*[]byte)
	buf := *bufPtr
	defer readBufPool.Put(bufPtr)

	msgPtr := writeBufPool.Get().(*[]byte)
	msg := *msgPtr
	msg[0] = MsgOutput
	defer writeBufPool.Put(msgPtr)

	var totalBytes int64

	for {
		select {
		case <-ctx.Done():
			logger.Debug("WebSocket output stopped (context)", "session", session.ID, "bytes_sent", totalBytes)
			return
		case <-session.Done():
			logger.Debug("session ended, sending close", "session", session.ID)
			_ = conn.Write(ctx, websocket.MessageBinary, []byte{MsgClose})
			return
		default:
		}

		n, err := session.PtyMaster.Read(buf)
		if err != nil {
			logger.Debug("output closed", "session", session.ID, "bytes_sent", totalBytes, "error", err)
			_ = conn.Write(ctx, websocket.MessageBinary, []byte{MsgClose})
			return
		}
		if n == 0 {
			continue
		}

		if totalBytes == 0 {
			logger.Debug("first output received", "session", session.ID, "bytes", n)
		}
		
		totalBytes += int64(n)
		copy(msg[1:], buf[:n])
		if err := conn.Write(ctx, websocket.MessageBinary, msg[:n+1]); err != nil {
			logger.Debug("WebSocket write error", "session", session.ID, "err", err)
			return
		}
	}
}

// streamPTYToWebTransport reads from PTY and writes framed messages to WebTransport.
func (s *Server) streamPTYToWebTransport(ctx context.Context, stream *webtransport.Stream, session *Session) {
	// Get pooled buffers
	bufPtr := readBufPool.Get().(*[]byte)
	buf := *bufPtr
	defer readBufPool.Put(bufPtr)

	framePtr := writeBufPool.Get().(*[]byte)
	frame := *framePtr
	defer writeBufPool.Put(framePtr)

	var totalBytes int64

	for {
		select {
		case <-ctx.Done():
			logger.Debug("WebTransport output stopped (context)", "session", session.ID, "bytes_sent", totalBytes)
			return
		case <-session.Done():
			logger.Debug("session ended, sending close", "session", session.ID)
			_ = writeFramed(stream, []byte{MsgClose})
			return
		default:
		}

		n, err := session.PtyMaster.Read(buf)
		if err != nil {
			logger.Debug("output closed", "session", session.ID, "bytes_sent", totalBytes, "error", err)
			_ = writeFramed(stream, []byte{MsgClose})
			return
		}
		if n == 0 {
			continue
		}

		if totalBytes == 0 {
			// Show first 100 bytes (or all if less) for debugging
			debugBytes := n
			if debugBytes > 100 {
				debugBytes = 100
			}
			logger.Debug("first output received (WT)", "session", session.ID, "bytes", n, "first_bytes", fmt.Sprintf("%q", string(buf[:debugBytes])))
		}

		totalBytes += int64(n)

		// Build framed message: [4-byte length][1-byte type][data]
		msgLen := n + 1
		binary.BigEndian.PutUint32(frame[0:4], uint32(msgLen))
		frame[4] = MsgOutput
		copy(frame[5:], buf[:n])

		if _, err := stream.Write(frame[:5+n]); err != nil {
			logger.Debug("WebTransport write error", "session", session.ID, "err", err)
			return
		}
	}
}

func (s *Server) handleWebSocketInput(ctx context.Context, conn *websocket.Conn, session *Session) {
	var totalBytes int64
	var msgCount int64

	for {
		select {
		case <-ctx.Done():
			logger.Debug("WebSocket input stopped", "session", session.ID, "messages", msgCount, "bytes", totalBytes)
			return
		case <-session.Done():
			return
		default:
		}

		_, data, err := conn.Read(ctx)
		if err != nil {
			return
		}

		totalBytes += int64(len(data))
		msgCount++
		s.processInput(data, session)
	}
}

func (s *Server) handleWebTransportInput(ctx context.Context, stream *webtransport.Stream, session *Session) {
	lenBuf := make([]byte, 4)
	var totalBytes int64
	var msgCount int64

	for {
		select {
		case <-ctx.Done():
			logger.Debug("WebTransport input stopped", "session", session.ID, "messages", msgCount, "bytes", totalBytes)
			return
		case <-session.Done():
			return
		default:
		}

		if _, err := io.ReadFull(stream, lenBuf); err != nil {
			return
		}

		length := binary.BigEndian.Uint32(lenBuf)
		if length > 1024*1024 {
			logger.Warn("message too large", "session", session.ID, "size", length)
			return
		}

		// Use pooled buffer for small messages
		var msg []byte
		if length <= 256 {
			bufPtr := smallBufPool.Get().(*[]byte)
			msg = (*bufPtr)[:length]
			defer smallBufPool.Put(bufPtr)
		} else {
			msg = make([]byte, length)
		}

		if _, err := io.ReadFull(stream, msg); err != nil {
			return
		}

		totalBytes += int64(length)
		msgCount++
		s.processInput(msg, session)
	}
}

func (s *Server) processInput(data []byte, session *Session) {
	if len(data) == 0 {
		return
	}

	msgType := data[0]
	payload := data[1:]

	switch msgType {
	case MsgInput:
		if !s.config.ReadOnly {
			_, _ = session.PtyMaster.Write(payload)
		}

	case MsgResize:
		var resize ResizeMessage
		if err := json.Unmarshal(payload, &resize); err != nil {
			logger.Warn("invalid resize message", "session", session.ID, "err", err)
			return
		}
		session.Resize(resize.Cols, resize.Rows)

		logger.Debug("terminal resized",
			"session", session.ID,
			"to", []int{resize.Cols, resize.Rows},
		)

	case MsgPing:
		// Pong handled at transport layer
	}
}

// writeFramed writes a message with 4-byte big-endian length prefix.
func writeFramed(w io.Writer, msg []byte) error {
	frame := make([]byte, 4+len(msg))
	binary.BigEndian.PutUint32(frame[0:4], uint32(len(msg)))
	copy(frame[4:], msg)
	_, err := w.Write(frame)
	return err
}

// Atomic connection counter methods for server.go
func (s *Server) incrementConnCount() int32 {
	return atomic.AddInt32(&s.connCount, 1)
}

func (s *Server) decrementConnCount() int32 {
	return atomic.AddInt32(&s.connCount, -1)
}
