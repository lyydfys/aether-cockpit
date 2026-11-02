// Package mobile owns the process-wide mobile gateway lifecycle.
//
// This slice serves only the anonymous /healthz endpoint. Gateway wiring,
// persistence and Flush are added by later slices; Flush must not exist as a
// no-op success before there is state to flush.
package mobile

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"strconv"
	"sync"
	"time"

	"aether-cockpit/internal/core/apppaths"
	"aether-cockpit/internal/core/logging"
)

const (
	StateStarting = "starting"
	StateRunning  = "running"
	StateStopping = "stopping"
	StateStopped  = "stopped"
	StateFailed   = "failed"
)

// RuntimeConfig is the single lifecycle input for the mobile runtime. DataDir
// is <noBackupFilesDir>/aether, CacheDir is <filesDir>/aether/cache and TempDir
// is <cacheDir>/aether/tmp; all are absolute and host-provided. The runtime
// never derives them from the process environment or working directory.
type RuntimeConfig struct {
	DataDir    string `json:"dataDir"`
	CacheDir   string `json:"cacheDir"`
	TempDir    string `json:"tempDir"`
	ListenHost string `json:"listenHost"`
	Port       int    `json:"port"`
	// AdminSessionKey and CallerKeyRequired are carried for the Stage 2
	// management-auth layer. This slice serves only /healthz, so neither is
	// enforced here; the key is never logged.
	AdminSessionKey   string `json:"adminSessionKey,omitempty"`
	CallerKeyRequired bool   `json:"callerKeyRequired,omitempty"`
	Platform          string `json:"platform"`
}

// RuntimeStatus is the JSON contract shared with the host ABI.
type RuntimeStatus struct {
	State     string `json:"state"`
	Addr      string `json:"addr,omitempty"`
	StartedAt int64  `json:"startedAt,omitempty"`
	ErrorCode string `json:"errorCode,omitempty"`
}

// StartError carries a stable code so the ABI never exposes a raw error.
type StartError struct {
	Code string
	Err  error
}

func (e *StartError) Error() string { return e.Code + ": " + e.Err.Error() }
func (e *StartError) Unwrap() error { return e.Err }

// Runtime owns the loopback listener and HTTP server for one start.
type Runtime struct {
	mu        sync.Mutex
	state     string
	addr      string
	startedAt int64
	errorCode string
	server    *http.Server
	done      chan struct{}
	paths     apppaths.Paths
	closeLog  func()
}

// Start validates the host-owned path, binds 127.0.0.1 only and returns only
// after the listener is accepting. Port 0 asks the system for a free port.
// iOS is a separate milestone and is rejected until its host exists.
func Start(ctx context.Context, cfg RuntimeConfig) (*Runtime, error) {
	if cfg.DataDir == "" {
		return nil, &StartError{Code: "data_dir_required", Err: errors.New("data dir is required")}
	}
	host := cfg.ListenHost
	if host == "" {
		host = "127.0.0.1"
	}
	if host != "127.0.0.1" {
		return nil, &StartError{Code: "listen_host_must_be_loopback", Err: fmt.Errorf("host %q is not loopback", host)}
	}
	if cfg.Port < 0 || cfg.Port > 65535 {
		return nil, &StartError{Code: "invalid_port", Err: fmt.Errorf("port %d is invalid", cfg.Port)}
	}
	if cfg.Platform != "android" {
		return nil, &StartError{Code: "unsupported_platform", Err: fmt.Errorf("platform %q is not supported", cfg.Platform)}
	}
	paths, err := apppaths.NewPaths(cfg.DataDir, cfg.CacheDir, cfg.TempDir)
	if err != nil {
		return nil, &StartError{Code: "invalid_paths", Err: err}
	}
	for _, dir := range []string{paths.DataDir, paths.UsageDir, paths.CacheDir, paths.TempDir} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return nil, &StartError{Code: "data_dir_failed", Err: err}
		}
	}

	lc := net.ListenConfig{}
	ln, err := lc.Listen(ctx, "tcp", net.JoinHostPort(host, strconv.Itoa(cfg.Port)))
	if err != nil {
		return nil, &StartError{Code: "listen_failed", Err: err}
	}

	closeLog, logErr := logging.Setup(paths.LogPath)
	if logErr != nil {
		_ = ln.Close()
		return nil, &StartError{Code: "log_setup_failed", Err: logErr}
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	})

	rt := &Runtime{
		state:     StateRunning,
		addr:      ln.Addr().String(),
		startedAt: time.Now().Unix(),
		server:    &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second},
		done:      make(chan struct{}),
		paths:     paths,
		closeLog:  closeLog,
	}
	go func() {
		defer close(rt.done)
		_ = rt.server.Serve(ln)
	}()
	log.Printf("mobile runtime started")
	return rt, nil
}

func (r *Runtime) Status() RuntimeStatus {
	r.mu.Lock()
	defer r.mu.Unlock()
	return RuntimeStatus{State: r.state, Addr: r.addr, StartedAt: r.startedAt, ErrorCode: r.errorCode}
}

// Paths returns the immutable host-provided path set owned by this runtime.
func (r *Runtime) Paths() apppaths.Paths {
	return r.paths
}

// Stop shuts the listener down and waits for Serve to return, so a successful
// Stop means this runtime no longer owns the port. It is idempotent and safe
// while another Stop is already waiting.
func (r *Runtime) Stop(ctx context.Context) error {
	r.mu.Lock()
	switch r.state {
	case StateStopped:
		r.mu.Unlock()
		return nil
	case StateStopping:
		done := r.done
		r.mu.Unlock()
		<-done
		return nil
	}
	r.state = StateStopping
	server := r.server
	done := r.done
	r.mu.Unlock()

	err := server.Shutdown(ctx)
	if err != nil {
		_ = server.Close()
	}
	<-done

	r.mu.Lock()
	r.state = StateStopped
	r.addr = ""
	r.startedAt = 0
	closeLog := r.closeLog
	r.closeLog = nil
	r.mu.Unlock()

	log.Printf("mobile runtime stopped")
	if closeLog != nil {
		closeLog()
	}
	return err
}
