package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/dikurungyandere/goleecher/internal/bot"
	"github.com/dikurungyandere/goleecher/internal/config"
	"github.com/dikurungyandere/goleecher/internal/store"
	"github.com/dikurungyandere/goleecher/internal/web"
)

func main() {
	cfg := config.Load()

	if cfg.APIID == 0 || cfg.APIHash == "" || cfg.BotToken == "" {
		fmt.Fprintln(os.Stderr, "Missing required env vars: API_ID, API_HASH, BOT_TOKEN")
		os.Exit(1)
	}

	if err := os.MkdirAll(cfg.TempDir, 0o755); err != nil {
		log.Fatalf("Failed to create temp dir: %v", err)
	}

	st := store.New(cfg.AdminID, cfg.AllowedIDs)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// Start web server in background
	webSrv := web.NewServer(cfg, st)
	go func() {
		log.Printf("Web dashboard listening on :%s", cfg.Port)
		if err := webSrv.Start(); err != nil {
			log.Printf("Web server error: %v", err)
		}
	}()

	// Start bot (blocking)
	b := bot.New(cfg, st)
	log.Println("Starting goleecher bot...")
	if err := b.Run(ctx); err != nil {
		log.Printf("Bot stopped: %v", err)
	}
}
