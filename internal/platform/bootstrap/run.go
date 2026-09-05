package bootstrap

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/evepupil/ManyRouter/internal/adapters/gateway/newapi"
	"github.com/evepupil/ManyRouter/internal/adapters/storage/postgres"
	supplieropenai "github.com/evepupil/ManyRouter/internal/adapters/supplier/openai"
	"github.com/evepupil/ManyRouter/internal/application/auth"
	"github.com/evepupil/ManyRouter/internal/application/idempotency"
	"github.com/evepupil/ManyRouter/internal/application/onboarding"
	"github.com/evepupil/ManyRouter/internal/application/operations"
	"github.com/evepupil/ManyRouter/internal/application/reconciliation"
	"github.com/evepupil/ManyRouter/internal/jobs"
	"github.com/evepupil/ManyRouter/internal/platform/config"
	platformcrypto "github.com/evepupil/ManyRouter/internal/platform/crypto"
	httptransport "github.com/evepupil/ManyRouter/internal/transport/http"
	"github.com/google/uuid"
)

func Run(ctx context.Context, applicationConfig config.Config, logger *slog.Logger) error {
	if applicationConfig.Mode == config.ModeMigrate {
		return postgres.Migrate(ctx, applicationConfig.DatabaseURL)
	}
	vault, err := platformcrypto.NewVaultFromBase64(applicationConfig.MasterKey, 1)
	if err != nil {
		return fmt.Errorf("configure credential vault: %w", err)
	}
	store, err := postgres.Open(ctx, applicationConfig.DatabaseURL)
	if err != nil {
		return err
	}
	defer store.Close()

	now := time.Now
	newID := uuid.New
	dispatcher := jobs.NewDispatcher()
	reconciliationService, err := reconciliation.NewService(
		store,
		vault,
		newapi.Factory{HTTPClient: gatewayHTTPClient()},
		dispatcher,
		now,
		newID,
	)
	if err != nil {
		return err
	}
	riverClient, err := jobs.NewClient(store.Pool(), reconciliationService, applicationConfig.RunsWorkers())
	if err != nil {
		return fmt.Errorf("configure River: %w", err)
	}
	if err := dispatcher.Bind(riverClient); err != nil {
		return err
	}
	if applicationConfig.RunsWorkers() {
		if err := riverClient.Start(ctx); err != nil {
			return fmt.Errorf("start River: %w", err)
		}
	}

	var server *http.Server
	serverErrors := make(chan error, 1)
	if applicationConfig.ServesHTTP() {
		onboardingService, err := onboarding.NewService(store, vault, now, newID)
		if err != nil {
			return err
		}
		idempotencyService, err := idempotency.NewService(store, now, 24*time.Hour)
		if err != nil {
			return err
		}
		authService, err := auth.NewService(store, applicationConfig.OperatorToken, now)
		if err != nil {
			return err
		}
		operationsClient := gatewayHTTPClient()
		supplierCredentialChecker, err := supplieropenai.NewCredentialChecker(operationsClient)
		if err != nil {
			return err
		}
		operationsService, err := operations.NewService(store, vault, newapi.Factory{HTTPClient: operationsClient}, supplierCredentialChecker)
		if err != nil {
			return err
		}
		handler, err := httptransport.NewHandler(onboardingService, reconciliationService, idempotencyService, logger, httptransport.WithOperations(operationsService))
		if err != nil {
			return err
		}
		router, err := httptransport.NewRouter(handler, applicationConfig.OperatorToken, logger, httptransport.WithAuth(authService, applicationConfig.AuthCookieSecure))
		if err != nil {
			return err
		}
		httptransport.RegisterOperationsRoutes(router, handler)
		server = &http.Server{
			Addr:              applicationConfig.HTTPAddress,
			Handler:           router,
			ReadHeaderTimeout: 5 * time.Second,
			ReadTimeout:       15 * time.Second,
			WriteTimeout:      90 * time.Second,
			IdleTimeout:       60 * time.Second,
			BaseContext: func(net.Listener) context.Context {
				return ctx
			},
		}
		go func() {
			logger.Info("HTTP server starting", "address", applicationConfig.HTTPAddress, "mode", applicationConfig.Mode)
			serverErrors <- server.ListenAndServe()
		}()
	}

	var runErr error
	if server == nil {
		<-ctx.Done()
		runErr = context.Cause(ctx)
	} else {
		select {
		case <-ctx.Done():
			runErr = context.Cause(ctx)
		case err := <-serverErrors:
			if !errors.Is(err, http.ErrServerClosed) {
				runErr = err
			}
		}
	}

	shutdownContext, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	var shutdownErrors []error
	if server != nil {
		if err := server.Shutdown(shutdownContext); err != nil {
			shutdownErrors = append(shutdownErrors, fmt.Errorf("stop HTTP server: %w", err))
		}
	}
	if applicationConfig.RunsWorkers() {
		if err := riverClient.Stop(shutdownContext); err != nil {
			shutdownErrors = append(shutdownErrors, fmt.Errorf("stop River: %w", err))
		}
	}
	if runErr != nil && !errors.Is(runErr, context.Canceled) {
		shutdownErrors = append(shutdownErrors, runErr)
	}
	return errors.Join(shutdownErrors...)
}

func gatewayHTTPClient() *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.MaxIdleConns = 20
	transport.MaxIdleConnsPerHost = 5
	transport.IdleConnTimeout = 60 * time.Second
	transport.ResponseHeaderTimeout = 60 * time.Second
	transport.TLSHandshakeTimeout = 10 * time.Second
	return &http.Client{Transport: transport, Timeout: 75 * time.Second}
}
