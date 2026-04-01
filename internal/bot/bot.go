package bot

import (
	"context"
	"log"
	"path/filepath"

	"github.com/gotd/td/session"
	"github.com/gotd/td/telegram"
	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"

	"github.com/dikurungyandere/goleecher/internal/config"
	"github.com/dikurungyandere/goleecher/internal/jobs"
	"github.com/dikurungyandere/goleecher/internal/store"
)

// Bot holds all bot state and dependencies.
type Bot struct {
	cfg     *config.Config
	st      *store.Store
	manager *jobs.Manager
	api     *tg.Client // set in Run before updates are dispatched
}

// New creates a new Bot instance.
func New(cfg *config.Config, st *store.Store) *Bot {
	return &Bot{
		cfg:     cfg,
		st:      st,
		manager: jobs.New(st),
	}
}

// Run starts the bot and blocks until ctx is cancelled.
func (b *Bot) Run(ctx context.Context) error {
	sessionPath := filepath.Join(b.cfg.TempDir, "session.json")

	h := &updateHandler{bot: b}

	client := telegram.NewClient(b.cfg.APIID, b.cfg.APIHash, telegram.Options{
		SessionStorage: &session.FileStorage{Path: sessionPath},
		UpdateHandler:  telegram.UpdateHandlerFunc(h.handle),
	})

	// client.API() is safe to call before Run; the tg.Client wraps the invoker.
	b.api = client.API()

	return client.Run(ctx, func(ctx context.Context) error {
		_, err := client.Auth().Bot(ctx, b.cfg.BotToken)
		if err != nil {
			if tgerr.Is(err, "ALREADY_AUTHORIZED") {
				log.Println("Bot already authorized")
			} else {
				return err
			}
		}

		log.Println("Bot authorized and running")
		<-ctx.Done()
		return ctx.Err()
	})
}
