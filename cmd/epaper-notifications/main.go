// Command epaper-notifications ist ein Notification-Microservice: Andere Services senden
// per REST-API Aufträge, der Epaper-Notifications Service rendert Templates und verschickt sie über
// konfigurierbare Channels (vorerst E-Mail).
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"epaper-notificaitons/internal/api"
	"epaper-notificaitons/internal/config"
	"epaper-notificaitons/internal/store/memory"
	"epaper-notificaitons/internal/template"
)

// version wird beim Build per -ldflags "-X main.version=..." gesetzt.
var version = "dev"

func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "healthcheck":
			os.Exit(healthcheck())
		case "templates":
			os.Exit(templatesCommand(os.Args[2:], os.Stdin, os.Stdout, os.Stderr))
		}
	}
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "epaper-notifications:", err)
		os.Exit(1)
	}
}

func run() error {
	// 1. Konfiguration. Vor dem Logger-Setup gehen Fehler direkt auf stderr.
	envFile := os.Getenv("ENV_FILE")
	if envFile == "" {
		envFile = ".env"
	}
	envLoaded, err := config.LoadDotEnv(envFile)
	if err != nil {
		return err
	}
	cfg, err := config.FromEnv()
	if err != nil {
		return err
	}

	logger := newLogger(cfg)
	slog.SetDefault(logger)
	logger.Info("starting epaper-notifications", "version", version, "config", cfg)
	if envLoaded {
		logger.Debug("loaded env file", "path", envFile)
	}

	// 2. Channel-Konfigurationen. Fehler hier verhindern den Start (Fail Fast).
	channels, err := config.LoadChannelConfigs(cfg.ChannelsDir, os.LookupEnv)
	if err != nil {
		return err
	}
	if len(channels) == 0 {
		return fmt.Errorf("%w in %s (expected e.g. email.yaml)", config.ErrNoChannels, cfg.ChannelsDir)
	}
	for _, c := range channels {
		logger.Info("channel config loaded", "channel", c.Name, "file", c.Path)
	}

	// 3. Templates. Fehler (Syntax, unbekannte Variablen, ...) verhindern den Start.
	tpls, err := template.Load(cfg.TemplatesDir)
	if err != nil {
		return err
	}
	if tpls.Len() == 0 {
		return fmt.Errorf("no templates found in %s (expected *.tmpl files)", cfg.TemplatesDir)
	}
	if err := checkTemplateChannels(tpls, channels); err != nil {
		return err
	}
	for _, info := range tpls.List() {
		logger.Info("template loaded", "template", info.Name, "channel", info.Channel, "format", info.Format)
	}
	for _, f := range tpls.Ignored() {
		logger.Warn("ignoring file in templates dir: templates must end in .tmpl", "file", f)
	}
	_ = tpls // wird ab Phase 5 (API-Validierung) und Phase 4 (Dispatcher) verdrahtet

	// 4. Persistenz (vorerst In-Memory). Dispatcher und API bekommen den Store ab Phase 4/5.
	st := memory.New()
	_ = st
	logger.Warn("using in-memory store: notification status and confirmations are lost on restart")

	// 5. API-Server. Der Port wird vor dem Ready-Signal gebunden, damit ein
	// belegter Port sofort als Startfehler auffällt.
	ln, err := net.Listen("tcp", ":"+strconv.Itoa(cfg.APIPort))
	if err != nil {
		return fmt.Errorf("listen on API port %d: %w", cfg.APIPort, err)
	}
	srv := api.New(ln.Addr().String(), logger)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	serveErr := make(chan error, 1)
	go func() { serveErr <- srv.Serve(ln) }()
	srv.SetReady(true)
	logger.Info("api server listening", "addr", ln.Addr().String())
	// TODO Phase 6: Public-Server auf cfg.PublicPort (nur /confirm/{token}).

	// 6. Warten auf Signal oder Serverfehler.
	select {
	case <-ctx.Done():
		logger.Info("shutdown signal received")
	case err := <-serveErr:
		if err != nil {
			return err
		}
		return errors.New("api server stopped unexpectedly")
	}

	// 7. Geordnetes Herunterfahren. /readyz sofort auf 503, dann laufende
	// Requests beenden. Ab Phase 4 kommt hier das Leerlaufen der Queue dazu.
	srv.SetReady(false)
	shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("graceful shutdown failed after %s: %w", cfg.ShutdownTimeout, err)
	}
	logger.Info("shutdown complete")
	return nil
}

func newLogger(cfg *config.Config) *slog.Logger {
	opts := &slog.HandlerOptions{Level: cfg.LogLevel}
	if cfg.LogFormat == "text" {
		return slog.New(slog.NewTextHandler(os.Stdout, opts))
	}
	return slog.New(slog.NewJSONHandler(os.Stdout, opts))
}

// healthcheck ist der Docker-HEALTHCHECK. Das Runtime-Image hat weder Shell
// noch curl, deshalb prüft das Binary sich selbst. Absichtlich ohne vollständige
// Config-Validierung: Es liest nur API_PORT.
func healthcheck() int {
	port := os.Getenv("API_PORT")
	if port == "" {
		port = "8080"
	}
	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get("http://127.0.0.1:" + port + "/healthz")
	if err != nil {
		fmt.Fprintln(os.Stderr, "healthcheck:", err)
		return 1
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		fmt.Fprintln(os.Stderr, "healthcheck: unexpected status", resp.Status)
		return 1
	}
	return 0
}

// checkTemplateChannels stellt sicher, dass jedes Template einen Channel
// verwendet, für den eine Konfigurationsdatei existiert. Sonst fiele der Fehler
// erst beim ersten Versand auf.
func checkTemplateChannels(tpls *template.Registry, channels []config.ChannelConfig) error {
	have := make(map[string]bool, len(channels))
	names := make([]string, 0, len(channels))
	for _, c := range channels {
		have[c.Name] = true
		names = append(names, c.Name)
	}
	var problems []string
	for _, info := range tpls.List() {
		if !have[info.Channel] {
			problems = append(problems, fmt.Sprintf("template %q uses channel %q, but there is no config/channels/%s.yaml (available: %s)",
				info.Name, info.Channel, info.Channel, strings.Join(names, ", ")))
		}
	}
	if len(problems) > 0 {
		return &config.Error{Problems: problems}
	}
	return nil
}
