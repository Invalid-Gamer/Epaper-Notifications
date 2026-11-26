// Package api enthält den internen REST-Server (PSK-geschützt, ab Phase 5).
// Vorerst nur die Health-Endpunkte für Docker und Reverse Proxy.
package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"sync/atomic"
	"time"
)

// Server ist der HTTP-Server des internen API-Ports.
type Server struct {
	http   *http.Server
	logger *slog.Logger
	ready  atomic.Bool
}

// New erzeugt den Server. addr hat die Form ":8080".
func New(addr string, logger *slog.Logger) *Server {
	s := &Server{logger: logger}
	s.http = &http.Server{
		Addr:              addr,
		Handler:           s.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    1 << 16,
		ErrorLog:          slog.NewLogLogger(logger.Handler(), slog.LevelWarn),
	}
	return s
}

// Handler liefert den Router (separat, damit Tests ohne Netzwerk auskommen).
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.healthz)
	mux.HandleFunc("GET /readyz", s.readyz)
	return mux
}

// SetReady schaltet /readyz um. Beim Herunterfahren zuerst auf false setzen,
// damit Load Balancer und Orchestrierung keinen neuen Traffic mehr schicken.
func (s *Server) SetReady(v bool) { s.ready.Store(v) }

// Serve blockiert, bis der Server beendet wird. Nach einem regulären
// Shutdown kommt nil zurück.
func (s *Server) Serve(ln net.Listener) error {
	if err := s.http.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("api server: %w", err)
	}
	return nil
}

// Shutdown beendet den Server geordnet; laufende Requests dürfen bis zum Ablauf von ctx fertig werden.
func (s *Server) Shutdown(ctx context.Context) error { return s.http.Shutdown(ctx) }

// Addr ist die konfigurierte Listen-Adresse.
func (s *Server) Addr() string { return s.http.Addr }

func (s *Server) healthz(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) readyz(w http.ResponseWriter, _ *http.Request) {
	if !s.ready.Load() {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "not_ready"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
