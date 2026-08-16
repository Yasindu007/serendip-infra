// Command serendipd is the Serendip control-plane daemon: the HTTP API
// (api package) over core/. See specs/002-http-api/plan.md.
package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/serendip-infra/serendip-infra/api"
	"github.com/serendip-infra/serendip-infra/core/event"
	"github.com/serendip-infra/serendip-infra/core/store/sqlite"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "serendipd:", err)
		os.Exit(1)
	}
}

func run() error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	dbPath := envOr("SERENDIP_DB_PATH", "serendip.db")
	addr := envOr("SERENDIP_ADDR", ":8080")
	apiKeys := parseAPIKeys(os.Getenv("SERENDIP_API_KEYS"))

	db, err := sqlite.Open(ctx, dbPath)
	if err != nil {
		return fmt.Errorf("open store: %w", err)
	}
	defer func() {
		if err := db.Close(); err != nil {
			log.Printf("serendipd: close db: %v", err)
		}
	}()
	if err := sqlite.Migrate(ctx, db); err != nil {
		return fmt.Errorf("migrate: %w", err)
	}

	bus := event.NewInProcessBus()
	srv := api.NewServer(db, bus, apiKeys)
	srv.Addr = addr

	errCh := make(chan error, 1)
	go func() {
		log.Printf("serendipd: listening on %s (db: %s)", addr, dbPath)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
			return
		}
		close(errCh)
	}()

	select {
	case <-ctx.Done():
		log.Print("serendipd: shutdown signal received")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			return fmt.Errorf("shutdown: %w", err)
		}
		log.Print("serendipd: shut down cleanly")
		return nil
	case err := <-errCh:
		if err != nil {
			return fmt.Errorf("serve: %w", err)
		}
		return nil
	}
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// parseAPIKeys parses SERENDIP_API_KEYS, format "token1:actor1,token2:actor2"
// (see api.ActorMiddleware's doc comment -- this whole mechanism is
// explicitly temporary until a real identity provider exists). Malformed
// entries are skipped with a warning rather than failing startup; an
// empty/unset env var means every request gets 401, which is a safe
// default, not a silent open door.
func parseAPIKeys(raw string) map[string]string {
	keys := make(map[string]string)
	if raw == "" {
		return keys
	}
	for _, pair := range strings.Split(raw, ",") {
		token, actor, ok := strings.Cut(pair, ":")
		if !ok || token == "" || actor == "" {
			log.Printf("serendipd: skipping malformed SERENDIP_API_KEYS entry %q", pair)
			continue
		}
		keys[token] = actor
	}
	return keys
}
