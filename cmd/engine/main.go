package main

import (
	"context"
	"errors"
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
	zerolog.SetGlobalLevel(zerolog.InfoLevel) // read from configuration
	if os.Getenv("GOERP_ENV") == "development" {
		log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stderr, TimeFormat: time.RFC3339})
	} else {
		log.Logger = zerolog.New(os.Stdout).With().Timestamp().Logger()
	}

	cfg, err := config.Load()
	if err != nil {
		log.Fatal().Err(err).Msg("failed to load config")
	}

	eng, err := engine.New(cfg)
	if err != nil {
		log.Fatal().Err(err).Msg("failed to initialize engine")
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)

	if err := eng.Start(ctx); err != nil {
		log.Fatal().Err(err).Msg("engine failed to start")
	}

	<-ctx.Done()
	stop()
	log.Info().Msg("shutdown signal received")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
	defer cancel()

	if err = eng.Shutdown(shutdownCtx); err != nil && !errors.Is(err, context.DeadlineExceeded) {
		log.Err(err).Msg("unclean shutdown")
		os.Exit(1)
	}
}
