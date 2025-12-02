// Package web provides a web-based terminal server for TUIOS.
// It implements a ttyd-like experience with WebSocket/WebTransport communication,
// xterm.js terminal rendering, and full mouse support.
// Pure Go implementation with no CGO dependencies.
package web

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/log"
	"github.com/quic-go/quic-go/http3"
	"github.com/quic-go/webtransport-go"
)

//go:embed static/*
var staticFiles embed.FS

// Package-level logger
var logger *log.Logger

func init() {
	logger = log.NewWithOptions(os.Stderr, log.Options{
		ReportTimestamp: true,
		Prefix:          "web",
	})
}

// SetLogLevel sets the logging level for the web package.
func SetLogLevel(level log.Level) {
	logger.SetLevel(level)
}

// Config holds the web server configuration.
type Config struct {
	Host           string        // Host to bind to (default: "localhost")
	Port           string        // Port to listen on (default: "7681")
	ReadOnly       bool          // If true, disallow input from clients
	MaxConnections int           // Maximum concurrent connections (0 = unlimited)
	IdleTimeout    time.Duration // Idle timeout for connections (0 = no timeout)
	AllowOrigins   []string      // Allowed origins for CORS (empty = all)
	TLSCert        string        // Path to TLS certificate (enables HTTPS/WebTransport)
	TLSKey         string        // Path to TLS private key
	TuiosArgs      []string      // Arguments to pass to TUIOS instance (e.g., --show-keys, --theme)
	Debug          bool          // Enable debug logging
}

// DefaultConfig returns the default configuration.
func DefaultConfig() Config {
	return Config{
		Host:           "localhost",
		Port:           "7681",
		ReadOnly:       false,
		MaxConnections: 0,
		IdleTimeout:    0,
		AllowOrigins:   nil,
		TuiosArgs:      nil,
		Debug:          false,
	}
}

// Server represents the web terminal server.
type Server struct {
	config     Config
	httpServer *http.Server
	wtServer   *webtransport.Server
	sessions   sync.Map // map[string]*Session
	connCount  int32    // atomic counter
	certInfo   *CertInfo
}

// NewServer creates a new web terminal server.
func NewServer(config Config) *Server {
	if config.Host == "" {
		config.Host = "localhost"
	}
	if config.Port == "" {
		config.Port = "7681"
	}

	if config.Debug {
		logger.SetLevel(log.DebugLevel)
	}

	logger.Info("creating web server",
		"host", config.Host,
		"port", config.Port,
		"read_only", config.ReadOnly,
		"max_connections", config.MaxConnections,
	)

	if len(config.TuiosArgs) > 0 {
		logger.Debug("TUIOS args", "args", config.TuiosArgs)
	}

	return &Server{
		config: config,
	}
}

// Start starts the web server.
func (s *Server) Start(ctx context.Context) error {
	// Parse port number for WebTransport (use port+1)
	httpPort := s.config.Port
	wtPortNum := 7682 // Default WebTransport port
	if p, err := strconv.Atoi(s.config.Port); err == nil {
		wtPortNum = p + 1
	}
	wtPort := strconv.Itoa(wtPortNum)

	httpAddr := net.JoinHostPort(s.config.Host, httpPort)
	wtAddr := net.JoinHostPort("127.0.0.1", wtPort)

	// Generate self-signed certificate for WebTransport
	logger.Debug("generating self-signed certificate")
	certInfo, err := GenerateSelfSignedCert(s.config.Host)
	if err != nil {
		return fmt.Errorf("failed to generate self-signed certificate: %w", err)
	}
	s.certInfo = certInfo

	logger.Info("certificate generated",
		"validity", "10 days",
		"algorithm", "ECDSA P-256",
	)

	// Create mux for HTTP server (serves static files, WebSocket)
	httpMux := http.NewServeMux()

	// Serve static files
	httpMux.HandleFunc("/", s.handleIndex)
	httpMux.HandleFunc("/static/", s.handleStatic)

	// WebSocket endpoint (fallback)
	httpMux.HandleFunc("/ws", s.handleWebSocket)

	// Health check
	httpMux.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("OK"))
	})

	// Certificate hash endpoint for WebTransport
	httpMux.HandleFunc("/cert-hash", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Access-Control-Allow-Origin", "*")
		hashArray := make([]int, len(s.certInfo.Hash))
		for i, b := range s.certInfo.Hash {
			hashArray[i] = int(b)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"algorithm": "sha-256",
			"hashBytes": hashArray,
			"wtUrl":     fmt.Sprintf("https://127.0.0.1:%s/webtransport", wtPort),
		})
	})

	// Create mux for WebTransport server
	wtMux := http.NewServeMux()
	wtMux.HandleFunc("/webtransport", s.handleWebTransport)

	// Create WebTransport server (HTTP/3 over QUIC) on separate port
	s.wtServer = &webtransport.Server{
		H3: http3.Server{
			Addr:            wtAddr,
			TLSConfig:       s.certInfo.TLSConfig,
			Handler:         wtMux,
			EnableDatagrams: true,
		},
		CheckOrigin: func(_ *http.Request) bool { return true },
	}

	// Create HTTP server for static files
	s.httpServer = &http.Server{
		Addr:         httpAddr,
		Handler:      httpMux,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
	}

	// Start servers
	errChan := make(chan error, 2)

	go func() {
		logger.Info("HTTP server starting",
			"addr", httpAddr,
			"url", fmt.Sprintf("http://%s", httpAddr),
		)
		if err := s.httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errChan <- fmt.Errorf("HTTP server error: %w", err)
		}
	}()

	go func() {
		logger.Info("WebTransport server starting",
			"addr", wtAddr,
			"protocol", "QUIC/UDP",
		)
		if err := s.wtServer.ListenAndServe(); err != nil {
			logger.Warn("WebTransport server error", "err", err)
		}
	}()

	logger.Info("server ready",
		"url", fmt.Sprintf("http://%s", httpAddr),
	)

	// Wait for context cancellation or error
	select {
	case <-ctx.Done():
		logger.Info("shutting down web server")
		_ = s.httpServer.Shutdown(ctx)
		_ = s.wtServer.Close()
		return nil
	case err := <-errChan:
		return err
	}
}

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}

	logger.Debug("serving index", "remote", r.RemoteAddr)

	data, err := staticFiles.ReadFile("static/index.html")
	if err != nil {
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(data)
}

func (s *Server) handleStatic(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/")
	data, err := staticFiles.ReadFile(path)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	logger.Debug("serving static", "path", path, "size", len(data))

	// Set content type based on extension
	switch {
	case strings.HasSuffix(path, ".js"):
		w.Header().Set("Content-Type", "application/javascript")
	case strings.HasSuffix(path, ".css"):
		w.Header().Set("Content-Type", "text/css")
	case strings.HasSuffix(path, ".woff2"):
		w.Header().Set("Content-Type", "font/woff2")
	case strings.HasSuffix(path, ".woff"):
		w.Header().Set("Content-Type", "font/woff")
	case strings.HasSuffix(path, ".ttf"):
		w.Header().Set("Content-Type", "font/ttf")
	}

	// Cache fonts for 1 year
	if strings.Contains(path, "fonts/") {
		w.Header().Set("Cache-Control", "public, max-age=31536000")
	}

	_, _ = w.Write(data)
}

// checkConnectionLimit returns true if connection is allowed.
func (s *Server) checkConnectionLimit() bool {
	if s.config.MaxConnections <= 0 {
		return true
	}
	newCount := s.incrementConnCount()
	if int(newCount) > s.config.MaxConnections {
		s.decrementConnCount()
		logger.Warn("connection limit reached",
			"current", newCount-1,
			"max", s.config.MaxConnections,
		)
		return false
	}
	logger.Debug("connection accepted", "count", newCount)
	return true
}

func (s *Server) releaseConnection() {
	if s.config.MaxConnections <= 0 {
		return
	}
	newCount := s.decrementConnCount()
	logger.Debug("connection released", "count", newCount)
}
