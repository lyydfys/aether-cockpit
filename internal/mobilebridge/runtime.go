package mobilebridge

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"strconv"
	"sync"
	"time"
)

const (
	StateStarting = "starting"
	StateRunning  = "running"
	StateStopping = "stopping"
	StateStopped  = "stopped"
	StateFailed   = "failed"
)

// Config is the narrow lifecycle input for the Android host. DataDir is owned by
// the host; the runtime never derives a path from the process environment.
type Config struct {
	DataDir    string `json:"dataDir"`
	ListenHost string `json:"listenHost"`
	Port       int    `json:"port"`
	Platform   string `json:"platform"`
}

// Status is the JSON contract shared with the host ABI.
type Status struct {
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
}

// Start validates the host-owned path, binds 127.0.0.1 only and returns only
// after the listener is accepting. Port 0 asks the system for a free port.
func Start(ctx context.Context, cfg Config) (*Runtime, error) {
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
	if err := os.MkdirAll(cfg.DataDir, 0o700); err != nil {
		return nil, &StartError{Code: "data_dir_failed", Err: err}
	}

	lc := net.ListenConfig{}
	ln, err := lc.Listen(ctx, "tcp", net.JoinHostPort(host, strconv.Itoa(cfg.Port)))
	if err != nil {
		return nil, &StartError{Code: "listen_failed", Err: err}
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
	}
	go func() {
		defer close(rt.done)
		_ = rt.server.Serve(ln)
	}()
	return rt, nil
}

func (r *Runtime) Status() Status {
	r.mu.Lock()
	defer r.mu.Unlock()
	return Status{State: r.state, Addr: r.addr, StartedAt: r.startedAt, ErrorCode: r.errorCode}
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
	r.mu.Unlock()
	return err
}
