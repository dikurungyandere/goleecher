package bot

import (
	"context"
	"fmt"
	"log"
	"crypto/rand"
	"encoding/binary"
	"os"
	"path/filepath"
	"strings"

	"github.com/gotd/td/tg"

	"github.com/dikurungyandere/goleecher/internal/downloader"
	"github.com/dikurungyandere/goleecher/internal/store"
)

// updateHandler dispatches incoming Telegram updates.
type updateHandler struct {
	bot *Bot
}

// handle is called for every UpdatesClass received from Telegram.
func (h *updateHandler) handle(ctx context.Context, u tg.UpdatesClass) error {
	switch upd := u.(type) {
	case *tg.Updates:
		// Build lookup maps for access hashes from entities in this batch.
		userMap := make(map[int64]*tg.User, len(upd.Users))
		for _, uc := range upd.Users {
			if usr, ok := uc.(*tg.User); ok {
				userMap[usr.ID] = usr
			}
		}
		chanMap := make(map[int64]*tg.Channel, len(upd.Chats))
		for _, ch := range upd.Chats {
			if channel, ok := ch.(*tg.Channel); ok {
				chanMap[channel.ID] = channel
			}
		}

		for _, update := range upd.Updates {
			if unm, ok := update.(*tg.UpdateNewMessage); ok {
				if msg, ok := unm.Message.(*tg.Message); ok {
					h.handleMessage(ctx, msg, userMap, chanMap)
				}
			}
		}

	case *tg.UpdatesCombined:
		userMap := make(map[int64]*tg.User, len(upd.Users))
		for _, uc := range upd.Users {
			if usr, ok := uc.(*tg.User); ok {
				userMap[usr.ID] = usr
			}
		}
		chanMap := make(map[int64]*tg.Channel, len(upd.Chats))
		for _, ch := range upd.Chats {
			if channel, ok := ch.(*tg.Channel); ok {
				chanMap[channel.ID] = channel
			}
		}
		for _, update := range upd.Updates {
			if unm, ok := update.(*tg.UpdateNewMessage); ok {
				if msg, ok := unm.Message.(*tg.Message); ok {
					h.handleMessage(ctx, msg, userMap, chanMap)
				}
			}
		}

	case *tg.UpdateShortMessage:
		// Direct message to bot - peer is the user themselves
		peer := &tg.InputPeerUser{UserID: upd.UserID}
		msg := &tg.Message{
			ID:      upd.ID,
			Message: upd.Message,
			PeerID:  &tg.PeerUser{UserID: upd.UserID},
		}
		if upd.Out {
			return nil
		}
		h.dispatchCommand(ctx, msg, upd.UserID, upd.UserID, peer)
	}
	return nil
}

// handleMessage processes a *tg.Message and dispatches commands.
func (h *updateHandler) handleMessage(ctx context.Context, msg *tg.Message, users map[int64]*tg.User, chans map[int64]*tg.Channel) {
	if msg.Out {
		return
	}

	var (
		fromID int64
		peer   tg.InputPeerClass
		chatID int64
	)

	// Determine from ID
	if fromPeer, ok := msg.GetFromID(); ok {
		if pu, ok := fromPeer.(*tg.PeerUser); ok {
			fromID = pu.UserID
		}
	}

	// Build InputPeer from PeerID for the chat we need to reply to
	switch p := msg.PeerID.(type) {
	case *tg.PeerUser:
		chatID = p.UserID
		var accessHash int64
		if usr, ok := users[p.UserID]; ok {
			accessHash = usr.AccessHash
		}
		peer = &tg.InputPeerUser{UserID: p.UserID, AccessHash: accessHash}
		if fromID == 0 {
			fromID = p.UserID
		}
	case *tg.PeerChat:
		chatID = p.ChatID
		peer = &tg.InputPeerChat{ChatID: p.ChatID}
	case *tg.PeerChannel:
		chatID = p.ChannelID
		var accessHash int64
		if ch, ok := chans[p.ChannelID]; ok {
			accessHash = ch.AccessHash
		}
		peer = &tg.InputPeerChannel{ChannelID: p.ChannelID, AccessHash: accessHash}
	default:
		return
	}

	h.dispatchCommand(ctx, msg, fromID, chatID, peer)
}

// dispatchCommand parses a command from a message and runs the appropriate handler.
func (h *updateHandler) dispatchCommand(ctx context.Context, msg *tg.Message, fromID, chatID int64, peer tg.InputPeerClass) {
	text := strings.TrimSpace(msg.Message)
	if text == "" || text[0] != '/' {
		return
	}

	// Strip @botusername suffix from commands
	parts := strings.Fields(text)
	cmd := parts[0]
	if idx := strings.Index(cmd, "@"); idx > 0 {
		cmd = cmd[:idx]
	}

	bot := h.bot

	switch cmd {
	case "/start":
		bot.cmdStart(ctx, fromID, chatID, peer)
	case "/leech":
		if len(parts) < 2 {
			bot.sendText(ctx, peer, "Usage: /leech <url|magnet> [document]")
			return
		}
		asDoc := len(parts) >= 3 && strings.ToLower(parts[len(parts)-1]) == "document"
		url := parts[1]
		bot.cmdLeech(ctx, fromID, chatID, peer, url, asDoc)
	case "/status":
		bot.cmdStatus(ctx, fromID, peer)
	case "/cancel":
		if len(parts) < 2 {
			bot.sendText(ctx, peer, "Usage: /cancel <job_id>")
			return
		}
		bot.cmdCancel(ctx, fromID, peer, parts[1])
	case "/cancelall":
		bot.cmdCancelAll(ctx, fromID, peer)
	}
}

// cmdStart handles /start.
func (b *Bot) cmdStart(ctx context.Context, fromID, chatID int64, peer tg.InputPeerClass) {
	if !b.st.IsAllowed(fromID) {
		b.sendText(ctx, peer, "⛔ You are not authorized to use this bot.")
		return
	}
	b.sendText(ctx, peer, "⚡ goleecher ready!\n\nCommands:\n/leech <url|magnet> [document] — download & upload\n/status — show active jobs\n/cancel <id> — cancel a job")
}

// cmdLeech handles /leech.
func (b *Bot) cmdLeech(ctx context.Context, fromID, chatID int64, peer tg.InputPeerClass, url string, asDoc bool) {
	if !b.st.IsAllowed(fromID) {
		b.sendText(ctx, peer, "⛔ You are not authorized to use this bot.")
		return
	}

	job, jobCtx, cancel := b.manager.NewJob(ctx, fromID, chatID, url)
	_ = cancel

	b.sendText(ctx, peer, fmt.Sprintf("✅ Job %s created. Starting download…", job.ID))

	go func() {
		b.runJob(jobCtx, job, peer, asDoc)
	}()
}

// runJob executes a full download → upload cycle for a job.
func (b *Bot) runJob(ctx context.Context, job *store.Job, peer tg.InputPeerClass, asDoc bool) {
	jobDir := filepath.Join(b.cfg.TempDir, job.ID)
	if err := os.MkdirAll(jobDir, 0o755); err != nil {
		b.manager.SetFailed(job.ID, err)
		b.sendText(ctx, peer, fmt.Sprintf("❌ Job %s failed: %v", job.ID, err))
		return
	}
	defer os.RemoveAll(jobDir)

	b.manager.SetDownloading(job.ID)
	progressFn := b.manager.ProgressUpdater(job.ID)

	var localPath string
	var err error

	if strings.HasPrefix(strings.ToLower(job.URL), "magnet:") {
		localPath, err = downloader.DownloadTorrent(ctx, job.URL, jobDir, progressFn)
	} else {
		localPath, err = downloader.DownloadHTTP(ctx, job.URL, jobDir, progressFn)
	}

	if err != nil {
		if ctx.Err() != nil {
			b.manager.SetCancelled(job.ID)
			b.sendText(ctx, peer, fmt.Sprintf("🚫 Job %s cancelled.", job.ID))
		} else {
			b.manager.SetFailed(job.ID, err)
			b.sendText(ctx, peer, fmt.Sprintf("❌ Job %s failed: %v", job.ID, err))
		}
		return
	}

	filename := filepath.Base(localPath)
	b.manager.SetUploading(job.ID, filename)

	info, err := os.Stat(localPath)
	if err != nil {
		b.manager.SetFailed(job.ID, err)
		b.sendText(ctx, peer, fmt.Sprintf("❌ Job %s failed: %v", job.ID, err))
		return
	}

	uploader := newUploader(b.api, b.cfg, b.manager, job.ID)

	if err := uploader.Upload(ctx, localPath, filename, info.Size(), peer, asDoc, progressFn); err != nil {
		if ctx.Err() != nil {
			b.manager.SetCancelled(job.ID)
			b.sendText(ctx, peer, fmt.Sprintf("🚫 Job %s cancelled.", job.ID))
		} else {
			b.manager.SetFailed(job.ID, err)
			b.sendText(ctx, peer, fmt.Sprintf("❌ Job %s upload failed: %v", job.ID, err))
		}
		return
	}

	b.manager.SetDone(job.ID, info.Size())
	b.sendText(ctx, peer, fmt.Sprintf("✅ Job %s done! Uploaded: %s", job.ID, filename))
}

// cmdStatus handles /status.
func (b *Bot) cmdStatus(ctx context.Context, fromID int64, peer tg.InputPeerClass) {
	if !b.st.IsAllowed(fromID) {
		b.sendText(ctx, peer, "⛔ You are not authorized.")
		return
	}

	active := b.st.Active()
	if len(active) == 0 {
		b.sendText(ctx, peer, "No active jobs.")
		return
	}

	var sb strings.Builder
	sb.WriteString("📋 Active jobs:\n\n")
	for _, j := range active {
		sb.WriteString(fmt.Sprintf(
			"• %s [%s] %.1f%% — %s\n",
			j.ID, j.Status, j.Progress, j.Filename,
		))
	}
	b.sendText(ctx, peer, sb.String())
}

// cmdCancel handles /cancel.
func (b *Bot) cmdCancel(ctx context.Context, fromID int64, peer tg.InputPeerClass, jobID string) {
	if !b.st.IsAllowed(fromID) {
		b.sendText(ctx, peer, "⛔ You are not authorized.")
		return
	}
	if err := b.manager.CancelJob(jobID, fromID); err != nil {
		b.sendText(ctx, peer, fmt.Sprintf("❌ %v", err))
		return
	}
	b.sendText(ctx, peer, fmt.Sprintf("🚫 Job %s cancel requested.", jobID))
}

// cmdCancelAll handles /cancelall (admin only).
func (b *Bot) cmdCancelAll(ctx context.Context, fromID int64, peer tg.InputPeerClass) {
	if !b.st.IsAdmin(fromID) {
		b.sendText(ctx, peer, "⛔ Admin only command.")
		return
	}
	b.st.CancelAll()
	b.sendText(ctx, peer, "🚫 All active jobs cancelled.")
}

// sendText sends a plain text message to a peer.
func (b *Bot) sendText(ctx context.Context, peer tg.InputPeerClass, text string) {
	if b.api == nil {
		log.Printf("sendText: api not ready, dropping message: %s", text)
		return
	}
	_, err := b.api.MessagesSendMessage(ctx, &tg.MessagesSendMessageRequest{
		Peer:     peer,
		Message:  text,
		RandomID: cryptoRandInt63(),
	})
	if err != nil {
		log.Printf("sendText error: %v", err)
	}
}

func cryptoRandInt63() int64 {
	var b [8]byte
	_, _ = rand.Read(b[:])
	v := int64(binary.LittleEndian.Uint64(b[:]))
	if v < 0 {
		v = -v
	}
	return v
}
