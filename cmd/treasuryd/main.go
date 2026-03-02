package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"

	"github.com/luxfi/treasury/pkg/api"
	"github.com/luxfi/treasury/pkg/ledger"
	"github.com/luxfi/treasury/pkg/provider"
	"github.com/luxfi/treasury/pkg/provider/bridge"
	"github.com/luxfi/treasury/pkg/provider/column"
	"github.com/luxfi/treasury/pkg/provider/currencycloud"
	"github.com/luxfi/treasury/pkg/provider/increase"
	"github.com/luxfi/treasury/pkg/provider/lithic"
	"github.com/luxfi/treasury/pkg/provider/marqeta"
	"github.com/luxfi/treasury/pkg/provider/mercury"
	"github.com/luxfi/treasury/pkg/provider/moderntreasury"
	"github.com/luxfi/treasury/pkg/provider/plaid"
	"github.com/luxfi/treasury/pkg/provider/square"
	"github.com/luxfi/treasury/pkg/provider/stripe"
	"github.com/luxfi/treasury/pkg/provider/unit"
	"github.com/luxfi/treasury/pkg/provider/wise"
)

func main() {
	log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stderr})

	listenAddr := envOr("TREASURY_LISTEN", ":8091")

	registry := provider.NewRegistry()

	// CurrencyCloud — FX + cross-border payments
	if login := os.Getenv("CURRENCYCLOUD_LOGIN_ID"); login != "" {
		registry.Register(currencycloud.New(currencycloud.Config{
			BaseURL: envOr("CURRENCYCLOUD_BASE_URL", currencycloud.DemoURL),
			LoginID: login,
			APIKey:  os.Getenv("CURRENCYCLOUD_API_KEY"),
		}))
		log.Info().Msg("CurrencyCloud provider registered")
	}

	// Plaid — bank account linking + identity
	if clientID := os.Getenv("PLAID_CLIENT_ID"); clientID != "" {
		registry.Register(plaid.New(plaid.Config{
			BaseURL:  envOr("PLAID_BASE_URL", plaid.SandboxURL),
			ClientID: clientID,
			Secret:   os.Getenv("PLAID_SECRET"),
		}))
		log.Info().Msg("Plaid provider registered")
	}

	// Modern Treasury — payment operations + ledger
	if orgID := os.Getenv("MODERN_TREASURY_ORG_ID"); orgID != "" {
		registry.Register(moderntreasury.New(moderntreasury.Config{
			BaseURL: envOr("MODERN_TREASURY_BASE_URL", moderntreasury.ProdURL),
			OrgID:   orgID,
			APIKey:  os.Getenv("MODERN_TREASURY_API_KEY"),
		}))
		log.Info().Msg("Modern Treasury provider registered")
	}

	// Stripe Treasury — BaaS
	if key := os.Getenv("STRIPE_SECRET_KEY"); key != "" {
		registry.Register(stripe.New(stripe.Config{
			BaseURL:   envOr("STRIPE_BASE_URL", stripe.ProdURL),
			SecretKey: key,
		}))
		log.Info().Msg("Stripe Treasury provider registered")
	}

	// Column — direct bank access (Fed member)
	if key := os.Getenv("COLUMN_API_KEY"); key != "" {
		registry.Register(column.New(column.Config{
			BaseURL: envOr("COLUMN_BASE_URL", column.SandboxURL),
			APIKey:  key,
		}))
		log.Info().Msg("Column provider registered")
	}

	// Wise — international transfers + FX
	if token := os.Getenv("WISE_API_TOKEN"); token != "" {
		registry.Register(wise.New(wise.Config{
			BaseURL:   envOr("WISE_BASE_URL", wise.SandboxURL),
			APIToken:  token,
			ProfileID: os.Getenv("WISE_PROFILE_ID"),
		}))
		log.Info().Msg("Wise provider registered")
	}

	// Marqeta — card issuing
	if token := os.Getenv("MARQETA_APP_TOKEN"); token != "" {
		registry.Register(marqeta.New(marqeta.Config{
			BaseURL:     envOr("MARQETA_BASE_URL", marqeta.SandboxURL),
			AppToken:    token,
			AccessToken: os.Getenv("MARQETA_ACCESS_TOKEN"),
		}))
		log.Info().Msg("Marqeta provider registered")
	}

	// Bridge (by Stripe) — stablecoin orchestration
	if key := os.Getenv("BRIDGE_API_KEY"); key != "" {
		registry.Register(bridge.New(bridge.Config{
			BaseURL: envOr("BRIDGE_BASE_URL", bridge.ProdURL),
			APIKey:  key,
		}))
		log.Info().Msg("Bridge provider registered")
	}

	// Unit — BaaS (white-label banking)
	if token := os.Getenv("UNIT_TOKEN"); token != "" {
		registry.Register(unit.New(unit.Config{
			BaseURL: envOr("UNIT_BASE_URL", unit.SandboxURL),
			Token:   token,
		}))
		log.Info().Msg("Unit provider registered")
	}

	// Mercury — startup neobank
	if key := os.Getenv("MERCURY_API_KEY"); key != "" {
		registry.Register(mercury.New(mercury.Config{
			BaseURL: envOr("MERCURY_BASE_URL", mercury.ProdURL),
			APIKey:  key,
		}))
		log.Info().Msg("Mercury provider registered")
	}

	// Square — payments + commerce
	if token := os.Getenv("SQUARE_ACCESS_TOKEN"); token != "" {
		registry.Register(square.New(square.Config{
			BaseURL:     envOr("SQUARE_BASE_URL", square.ProdURL),
			AccessToken: token,
		}))
		log.Info().Msg("Square provider registered")
	}

	// Increase — banking infrastructure (ACH, wire, RTP, checks)
	if key := os.Getenv("INCREASE_API_KEY"); key != "" {
		registry.Register(increase.New(increase.Config{
			BaseURL: envOr("INCREASE_BASE_URL", increase.SandboxURL),
			APIKey:  key,
		}))
		log.Info().Msg("Increase provider registered")
	}

	// Lithic — card issuing (virtual + physical)
	if key := os.Getenv("LITHIC_API_KEY"); key != "" {
		registry.Register(lithic.New(lithic.Config{
			BaseURL: envOr("LITHIC_BASE_URL", lithic.SandboxURL),
			APIKey:  key,
		}))
		log.Info().Msg("Lithic provider registered")
	}

	if len(registry.List()) == 0 {
		log.Warn().Msg("No treasury providers configured. Set env vars to enable providers.")
	}

	// --- Ledger (Postgres or in-memory) ---
	var ledgerStore ledger.Store
	if dbURL := os.Getenv("DATABASE_URL"); dbURL != "" {
		pgStore, err := ledger.NewPgStore(dbURL)
		if err != nil {
			log.Fatal().Err(err).Msg("Failed to connect to database")
		}
		if err := pgStore.Migrate(context.Background()); err != nil {
			log.Fatal().Err(err).Msg("Failed to run ledger migrations")
		}
		ledgerStore = pgStore
		log.Info().Msg("Ledger: Postgres-backed")
	} else {
		ledgerStore = ledger.NewMemStore()
		log.Warn().Msg("Ledger: in-memory (set DATABASE_URL for persistence)")
	}
	ledgerSvc := ledger.NewService(ledgerStore)

	srv := api.NewServer(registry, ledgerSvc, listenAddr)

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		log.Info().Str("addr", listenAddr).Strs("providers", registry.List()).Msg("Treasury API starting")
		errCh <- srv.Start()
	}()

	select {
	case <-ctx.Done():
		log.Info().Msg("Shutting down...")
		if err := srv.Shutdown(context.Background()); err != nil {
			log.Error().Err(err).Msg("Shutdown error")
		}
	case err := <-errCh:
		if err != nil {
			log.Fatal().Err(err).Msg("Server failed")
		}
	}
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
