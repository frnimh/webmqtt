package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"webmqtt/internal/bridge"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	if err := run(logger); err != nil {
		logger.Error("server stopped", "error", err)
		os.Exit(1)
	}
}

func run(logger *slog.Logger) error {
	defaultPath := os.Getenv("WEBMQTT_CONFIG")
	if defaultPath == "" {
		defaultPath = "config.yml"
	}
	configPath := flag.String("config", defaultPath, "path to YAML configuration")
	flag.Parse()
	cfg, err := bridge.LoadConfig(*configPath)
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	history := bridge.NewHistory(cfg.Endpoints)
	connectCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	backend, err := bridge.ConnectMQTT(connectCtx, cfg.Global.MQTT, history, logger)
	cancel()
	if err != nil {
		return err
	}
	defer backend.Close()
	server := &http.Server{
		Addr:              net.JoinHostPort("", strconv.Itoa(cfg.Global.Port)),
		Handler:           bridge.NewHandler(cfg, backend, history, logger),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      10 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    16 << 10,
	}
	listener, err := net.Listen("tcp", server.Addr)
	if err != nil {
		return fmt.Errorf("listen HTTP: %w", err)
	}
	serveErr := make(chan error, 1)
	go func() { serveErr <- server.Serve(listener) }()
	logger.Info("HTTP server listening", "address", server.Addr, "endpoints", len(cfg.Endpoints))
	select {
	case err := <-serveErr:
		if !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("serve HTTP: %w", err)
		}
		return nil
	case <-ctx.Done():
		stop()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			_ = server.Close()
			return fmt.Errorf("shutdown HTTP: %w", err)
		}
		if err := <-serveErr; err != nil && !errors.Is(err, http.ErrServerClosed) {
			return err
		}
		return nil
	}
}
