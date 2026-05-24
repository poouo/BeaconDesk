package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/poouo/BeaconDesk/internal/config"
	"github.com/poouo/BeaconDesk/internal/relay"
	"github.com/poouo/BeaconDesk/pkg/version"
)

func main() {
	configPath := flag.String("config", "", "path to relay config file")
	showVersion := flag.Bool("version", false, "print version and exit")
	flag.Parse()

	if *showVersion {
		fmt.Printf("BeaconDesk-relay %s commit=%s date=%s\n", version.Version, version.Commit, version.Date)
		return
	}

	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	cfg, err := config.LoadRelayConfig(*configPath)
	if err != nil {
		logger.Error("failed to load config", "error", err)
		os.Exit(1)
	}
	if cfg.TLSCertFile == "" && cfg.TLSKeyFile == "" && cfg.AllowInsecurePlaintext {
		logger.Warn("plaintext relay transport is enabled; use TLS for production", "transport", cfg.Transport)
	}
	if cfg.TLSCertFile != "" || cfg.TLSKeyFile != "" {
		logger.Info("native TLS listener enabled", "cert", cfg.TLSCertFile)
	}
	logger.Info("relay transport configured", "transport", cfg.Transport, "websocket_path", cfg.WebSocketPath)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	server := relay.NewServer(cfg, logger)
	if err := server.Serve(ctx); err != nil {
		logger.Error("relay server stopped", "error", err)
		os.Exit(1)
	}
}
