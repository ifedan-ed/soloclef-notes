package metrics

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"time"
)

// MetricsPath is where a scrape looks. The host's other exporters all serve
// /metrics, and the Go services on it serve it on :9464, so the convention is
// matched rather than invented (custom/docs/go-worker.md).
const MetricsPath = "/metrics"

// Collector fills one scrape from one source.
type Collector interface {
	Collect(ctx context.Context, into *Writer) error
}

type collectorFunc func(ctx context.Context, into *Writer) error

func (f collectorFunc) Collect(ctx context.Context, into *Writer) error { return f(ctx, into) }

// Server serves the scrape and nothing else.
type Server struct {
	collectors []Collector
	listener   net.Listener
	log        *slog.Logger
}

// NewServer binds the address now rather than when it is served, so a port
// already in use stops the worker starting instead of silently serving
// nothing.
func NewServer(addr string, collectors ...Collector) (*Server, error) {
	if addr == "" {
		return nil, errors.New("metrics: no address to serve on")
	}
	if len(collectors) == 0 {
		return nil, errors.New("metrics: nothing to collect")
	}
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("metrics: listening on %s: %w", addr, err)
	}
	return &Server{
		collectors: collectors,
		listener:   listener,
		log:        slog.Default().With("metrics", listener.Addr().String()),
	}, nil
}

// Address is what was bound, which is not what was asked for when the port was
// zero.
func (s *Server) Address() string { return s.listener.Addr().String() }

// Run serves until ctx is cancelled.
func (s *Server) Run(ctx context.Context) error {
	mux := http.NewServeMux()
	mux.HandleFunc(MetricsPath, s.serve)
	server := &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}

	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		<-ctx.Done()
		shutdown, done := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer done()
		_ = server.Shutdown(shutdown)
	}()

	err := server.Serve(s.listener)
	<-stopped
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

// serve answers a scrape, or fails it. A collection that could not read the
// broker must not answer with a page of zeroes: a dashboard would draw that as
// an empty queue rather than as an unknown one.
func (s *Server) serve(w http.ResponseWriter, r *http.Request) {
	into := newWriter()
	for _, collector := range s.collectors {
		if err := collector.Collect(r.Context(), into); err != nil {
			s.log.Error("a scrape could not be answered", "error", err)
			http.Error(w, "collecting: "+err.Error(), http.StatusInternalServerError)
			return
		}
	}
	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	_, _ = w.Write([]byte(into.String()))
}
