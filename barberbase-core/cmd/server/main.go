package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"barberbase-core/internal/api"
	"barberbase-core/internal/bhejna"
	"barberbase-core/internal/config"
	"barberbase-core/internal/domain/presence"
	"barberbase-core/internal/jobs"
	"barberbase-core/internal/outbox"
	"barberbase-core/internal/realtime"
	"barberbase-core/internal/repository"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/google/uuid"
)

// Server is declared in package api.

func main() {
	log.Println("Starting BarberBase API Server...")

	// 1. Load configuration
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("Failed to load configuration: %v", err)
	}
	if cfg.PlatformAdminKey == "" {
		log.Fatal("PLATFORM_ADMIN_KEY environment variable is required")
	}
	log.Printf("Configuration loaded successfully. Environment: %s", cfg.Environment)

	// 2. Initialize database connection pool
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	log.Println("Initializing database connection pool...")
	pool, err := repository.InitPool(ctx, cfg.DatabaseURL)
	if err != nil {
		log.Fatalf("Database initialization failed: %v", err)
	}
	defer func() {
		log.Println("Closing database connection pool...")
		pool.Close()
	}()
	log.Println("Database connection pool initialized and pinged successfully.")

	// 3. Run database migrations
	log.Println("Running database migrations...")
	err = repository.Migrate(ctx, pool, "migrations/001_complete_schema.sql")
	if err != nil {
		log.Fatalf("Database migration failed: %v", err)
	}

	// 4. Setup router and middleware
	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)

	// Health check route
	r.Get("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"healthy"}`))
	})

	// 5. Register oapi-codegen generated handler
	bhejnaClient := bhejna.NewClient(pool, cfg.AESEncryptionKey, cfg.BhejnaAPIKey, cfg.BhejnaFromPhone)
	go outbox.NewWorker(pool, bhejnaClient).Run(ctx)
	
	mgr := realtime.NewManager()
	mgr.StartHeartbeats(ctx)

	watchdog := jobs.NewWatchdog(pool, mgr, cfg)
	eod      := jobs.NewEndOfDay(pool, mgr, cfg)
	weekly   := jobs.NewWeeklySummary(pool, cfg)
	go watchdog.Start(ctx)
	go eod.Start(ctx)
	go weekly.Start(ctx)

	apiServer := &api.Server{
		Pool:    pool,
		Bhejna:  bhejnaClient,
		Config:  cfg,
		Manager: mgr,
	}

	broadcast := func(locationID uuid.UUID, version int64) {
		mgr.Broadcast(locationID.String(), realtime.SSEEvent{
			Type:         "queue_changed",
			LocationID:   locationID.String(),
			QueueVersion: int(version),
		})
	}
	apiServer.Arrival = presence.NewService(pool, broadcast)

	apiHandler := api.Handler(apiServer)
	r.Route("/v1", func(r chi.Router) {
		r.With(apiServer.PlatformAdminKeyMiddleware).Post("/admin/setup", apiServer.ProvisionTenant)
		r.Mount("/", apiHandler)
	})

	// 6. Start HTTP Server
	serverAddr := fmt.Sprintf(":%s", cfg.Port)
	srv := &http.Server{
		Addr:         serverAddr,
		Handler:      r,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
		IdleTimeout:  30 * time.Second,
	}

	go func() {
		log.Printf("HTTP Server is listening on %s", serverAddr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("HTTP server failed: %v", err)
		}
	}()

	// 7. Graceful Shutdown
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	log.Println("Shutdown signal received. Shutting down server...")

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Fatalf("Server forced to shutdown: %v", err)
	}

	log.Println("BarberBase API Server stopped cleanly.")
}
