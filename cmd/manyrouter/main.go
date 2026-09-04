package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/evepupil/ManyRouter/internal/platform/bootstrap"
	"github.com/evepupil/ManyRouter/internal/platform/config"
	"github.com/evepupil/ManyRouter/internal/platform/observability"
)

func main() {
	logger := observability.NewLogger(os.Stdout, "info")
	if err := run(os.Args[1:]); err != nil {
		logger.Error("ManyRouter stopped", "error", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("usage: manyrouter <serve|worker|migrate|all>")
	}
	applicationConfig, err := config.Load(config.Mode(args[0]))
	if err != nil {
		return err
	}
	logger := observability.NewLogger(os.Stdout, applicationConfig.LogLevel)
	slog.SetDefault(logger)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return bootstrap.Run(ctx, applicationConfig, logger)
}
