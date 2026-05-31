// Pastebin is a simple pastebin web server.
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"pastebin/internal/cleanup"
	"pastebin/internal/config"
	"pastebin/internal/handler"
	"pastebin/internal/idgen"
	"pastebin/internal/ratelimit"
	"pastebin/internal/stats"
	"pastebin/internal/store"
)

func main() {
	var configPath string
	flag.StringVar(&configPath, "config", "config.json", "path to config file")
	flag.Parse()

	// Setup logger
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))
	slog.SetDefault(logger)

	// Load config
	cfg, err := config.Load(configPath)
	if err != nil {
		logger.Error("failed to load config", "error", err)
		os.Exit(1)
	}

	// Set log level
	level := slog.LevelInfo
	switch cfg.LogLevel {
	case "debug":
		level = slog.LevelDebug
	case "info":
		level = slog.LevelInfo
	case "warn":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	}
	logger = slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level}))
	slog.SetDefault(logger)

	// Parse TTL rules
	ttlRules, err := config.ParseTTLMap(cfg.TTLMap)
	if err != nil {
		logger.Error("failed to parse TTL map", "error", err)
		os.Exit(1)
	}
	logger.Info("TTL rules loaded", "count", len(ttlRules))

	// Open database
	s, err := store.Open(cfg.DBPath)
	if err != nil {
		logger.Error("failed to open database", "error", err)
		os.Exit(1)
	}
	defer s.Close()
	logger.Info("database opened", "path", cfg.DBPath)

	// Create ID generator
	gen := idgen.New(cfg.IDCharset, cfg.IDMinLength, cfg.IDMaxLength,
		cfg.IDInitLength, cfg.IDMaxCollisions)
	logger.Info("id generator ready", "init_len", cfg.IDInitLength)

	// Create rate limiter
	limiter := ratelimit.New(cfg.RateLimit.Read, cfg.RateLimit.Write)
	defer limiter.Stop()

	// Parse timer configs
	cleanupTimer, err := config.ParseTimer(cfg.CleanupInterval)
	if err != nil {
		logger.Error("failed to parse cleanup interval", "error", err)
		os.Exit(1)
	}

	statsTimer, err := config.ParseTimer(cfg.StatsInterval)
	if err != nil {
		logger.Error("failed to parse stats interval", "error", err)
		os.Exit(1)
	}

	// Create cleanup scheduler
	cleanupSched := cleanup.New(s, cleanupTimer, logger)
	cleanupSched.Start()
	defer cleanupSched.Stop()
	logger.Info("cleanup scheduler started", "interval", cfg.CleanupInterval)

	// Create stats reporter
	reporter := stats.New(s, statsTimer, logger)
	reporter.Start()
	defer reporter.Stop()
	logger.Info("stats reporter started", "interval", cfg.StatsInterval)

	// Read help text
	helpText, err := os.ReadFile("help.txt")
	if err != nil {
		logger.Error("failed to read help.txt", "error", err)
		os.Exit(1)
	}

	// Create handler
	h := handler.New(s, gen, cfg, ttlRules, limiter, reporter, string(helpText), logger)
	mux := h.Mux()

	// Wrap with logging middleware
	wrappedMux := handler.LoggingMiddleware(logger, cfg.BehindProxy)(mux)

	// Create HTTP server
	srv := &http.Server{
		Addr:         cfg.Listen,
		Handler:      wrappedMux,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	// Check admin key
	if cfg.AdminKeyHash != "" {
		logger.Info("admin authentication enabled")
	} else {
		logger.Warn("no admin key configured, admin endpoints will accept no one")
	}

	// Start server in background
	go func() {
		logger.Info("server starting", "listen", cfg.Listen)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Error("server error", "error", err)
			os.Exit(1)
		}
	}()

	// Wait for shutdown signal
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	sig := <-quit
	logger.Info("shutting down", "signal", sig.String())

	// Graceful shutdown with timeout
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := srv.Shutdown(ctx); err != nil {
		logger.Error("server shutdown error", "error", err)
	}

	fmt.Println("server stopped")
}
