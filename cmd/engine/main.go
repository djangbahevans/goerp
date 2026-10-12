package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/djangbahevans/goerp/internal/engine"
	"github.com/djangbahevans/goerp/internal/engine/config"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

func main() {
	zerolog.SetGlobalLevel(zerolog.InfoLevel)
	if os.Getenv("GOERP_ENV") == "development" {
		log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stderr, TimeFormat: time.RFC3339})
	} else {
		log.Logger = zerolog.New(os.Stdout).With().Timestamp().Logger()
	}

	cfg, err := config.Load()
	if err != nil {
		log.Fatal().Err(err).Msg("failed to load config")
	}

	level, err := zerolog.ParseLevel(cfg.LogLevel)
	if err != nil {
		log.Fatal().Err(err).Msg("invalid log level")
	}
	zerolog.SetGlobalLevel(level)

	eng, err := engine.New(cfg)
	if err != nil {
		log.Fatal().Err(err).Msg("failed to initialize engine")
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	serviceCtx, cancelServices := context.WithCancel(context.Background())

	if err := eng.Start(serviceCtx); err != nil {
		log.Fatal().Err(err).Msg("engine failed to start")
	}

	exitCode := 0
	select {
	case <-ctx.Done():
		log.Info().Msg("shutdown signal received")
	case err := <-eng.ServeErrors():
		log.Error().Err(err).Msg("engine server failed, shutting down")
		exitCode = 1
	}
	stop()

	err = eng.Shutdown(context.Background())
	cancelServices()
	if err != nil {
		log.Err(err).Msg("unclean shutdown")
		exitCode = 1
	}
	os.Exit(exitCode)
}
