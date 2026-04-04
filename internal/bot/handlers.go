package bot

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"log"
	"math"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gotd/td/tg"

	"github.com/dikurungyandere/goleecher/internal/downloader"
	"github.com/dikurungyandere/goleecher/internal/jobs"
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
			if cb, ok := update.(*tg.UpdateBotCallbackQuery); ok {
				h.handleCallbackQuery(ctx, cb)
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
			if cb, ok := update.(*tg.UpdateBotCallbackQuery); ok {
				h.handleCallbackQuery(ctx, cb)
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
		var url string
		var asDoc, asZip bool

		// Determine if the first argument is a URL/magnet or a flag.
		if len(parts) >= 2 {
			candidate := parts[1]
			if !strings.EqualFold(candidate, "document") && !strings.EqualFold(candidate, "zip") {
				url = candidate
				for _, flag := range parts[2:] {
					switch strings.ToLower(flag) {
					case "document":
						asDoc = true
					case "zip":
						asZip = true
					}
				}
			} else {
				for _, flag := range parts[1:] {
					switch strings.ToLower(flag) {
					case "document":
						asDoc = true
					case "zip":
						asZip = true
					}
				}
			}
		}

		if url != "" {
			bot.cmdLeech(ctx, fromID, chatID, peer, msg.ID, url, asDoc, asZip)
			return
		}

		// No URL provided: look for a .torrent document in the replied-to message.
		replyHeader, ok := msg.ReplyTo.(*tg.MessageReplyHeader)
		if !ok || replyHeader.ReplyToMsgID == 0 {
			bot.sendText(ctx, peer, "Usage: /leech <url|magnet> [document] [zip]\nOr reply to a .torrent file with /leech [document] [zip]")
			return
		}
		bot.cmdLeechTorrentReply(ctx, fromID, chatID, peer, msg.ID, replyHeader.ReplyToMsgID, asDoc, asZip)
	case "/status":
		bot.cmdStatus(ctx, fromID, chatID, peer)
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
	b.sendText(ctx, peer, "⚡ goleecher ready!\n\nCommands:\n/leech <url|magnet> — download & upload\n/leech <url|magnet> document — force document upload\n/leech <url|magnet> zip — zip torrent before upload\n/leech — reply to a .torrent file to leech it\n/status — show active jobs\n/cancel <id> — cancel a job")
}

// cmdLeech handles /leech.
func (b *Bot) cmdLeech(ctx context.Context, fromID, chatID int64, peer tg.InputPeerClass, cmdMsgID int, url string, asDoc, asZip bool) {
	if !b.st.IsAllowed(fromID) {
		b.sendText(ctx, peer, "⛔ You are not authorized to use this bot.")
		return
	}

	job, jobCtx, cancel := b.manager.NewJob(b.rootCtx, fromID, chatID, url, cmdMsgID)
	_ = cancel

	// Send initial status message (will be updated with progress)
	statusMsgID := b.sendJobStatusMessage(ctx, peer, job, cmdMsgID)

	go func() {
		b.runJob(jobCtx, job, peer, statusMsgID, asDoc, asZip)
	}()
}

// cmdLeechTorrentReply handles /leech when replying to a .torrent file message.
func (b *Bot) cmdLeechTorrentReply(ctx context.Context, fromID, chatID int64, peer tg.InputPeerClass, cmdMsgID int, replyMsgID int, asDoc, asZip bool) {
	if !b.st.IsAllowed(fromID) {
		b.sendText(ctx, peer, "⛔ You are not authorized to use this bot.")
		return
	}

	repliedMsg, err := b.fetchMessage(ctx, peer, replyMsgID)
	if err != nil {
		b.sendText(ctx, peer, fmt.Sprintf("❌ Could not fetch replied message: %v", err))
		return
	}

	msg, ok := repliedMsg.(*tg.Message)
	if !ok {
		b.sendText(ctx, peer, "❌ Replied message is not a regular message.")
		return
	}

	mediaDoc, ok := msg.Media.(*tg.MessageMediaDocument)
	if !ok {
		b.sendText(ctx, peer, "❌ Replied message does not contain a document.")
		return
	}

	doc, ok := mediaDoc.Document.(*tg.Document)
	if !ok {
		b.sendText(ctx, peer, "❌ Could not read the document from the replied message.")
		return
	}

	if !isTorrentDocument(doc) {
		b.sendText(ctx, peer, "❌ Replied document is not a .torrent file.")
		return
	}

	job, jobCtx, cancel := b.manager.NewJob(b.rootCtx, fromID, chatID, torrentDocumentFilename(doc), cmdMsgID)
	_ = cancel

	// Send initial status message (will be updated with progress)
	statusMsgID := b.sendJobStatusMessage(ctx, peer, job, cmdMsgID)

	go func() {
		b.runJobFromTorrentDocument(jobCtx, job, peer, statusMsgID, doc, asDoc, asZip)
	}()
}

// runJob executes a full download → upload cycle for a job.
// statusMsgID is the message to edit with progress updates.
func (b *Bot) runJob(ctx context.Context, job *store.Job, peer tg.InputPeerClass, statusMsgID int, asDoc, asZip bool) {
	jobDir := filepath.Join(b.cfg.TempDir, job.ID)
	if err := os.MkdirAll(jobDir, 0o755); err != nil {
		b.manager.SetFailed(job.ID, err)
		b.sendReply(peer, job.ReplyToMsg, fmt.Sprintf("❌ Job %s failed: %v", job.ID, err))
		return
	}
	defer os.RemoveAll(jobDir)

	b.manager.SetDownloading(job.ID)
	progressFn := b.manager.ProgressUpdater(job.ID)

	// Start background goroutine to update status message periodically
	stopProgress := b.startJobProgressUpdater(ctx, job.ID, peer, statusMsgID)
	defer stopProgress()

	var localPath string
	var err error

	if strings.HasPrefix(strings.ToLower(job.URL), "magnet:") {
		localPath, err = downloader.DownloadTorrent(ctx, job.URL, jobDir, progressFn)
	} else {
		nameCallback := func(name string) { b.manager.SetFilename(job.ID, name) }
		localPath, err = downloader.DownloadHTTP(ctx, job.URL, jobDir, progressFn, nameCallback)
	}

	if err != nil {
		if ctx.Err() != nil {
			// Already marked as cancelled by CancelJob
			b.editJobFinalStatus(peer, statusMsgID, job, "🚫 Cancelled")
		} else {
			b.manager.SetFailed(job.ID, err)
			b.editJobFinalStatus(peer, statusMsgID, job, fmt.Sprintf("❌ Failed: %v", err))
		}
		return
	}

	updr := newUploader(b.api, b.cfg, b.manager, job.ID)
	b.runJobAfterDownload(ctx, job, peer, statusMsgID, localPath, asDoc, asZip, updr, progressFn)
}

// runJobFromTorrentDocument downloads a .torrent document from Telegram and then runs the torrent.
func (b *Bot) runJobFromTorrentDocument(ctx context.Context, job *store.Job, peer tg.InputPeerClass, statusMsgID int, doc *tg.Document, asDoc, asZip bool) {
	jobDir := filepath.Join(b.cfg.TempDir, job.ID)
	if err := os.MkdirAll(jobDir, 0o755); err != nil {
		b.manager.SetFailed(job.ID, err)
		b.sendReply(peer, job.ReplyToMsg, fmt.Sprintf("❌ Job %s failed: %v", job.ID, err))
		return
	}
	defer os.RemoveAll(jobDir)

	b.manager.SetDownloading(job.ID)
	progressFn := b.manager.ProgressUpdater(job.ID)

	// Start background goroutine to update status message periodically
	stopProgress := b.startJobProgressUpdater(ctx, job.ID, peer, statusMsgID)
	defer stopProgress()

	// Download the .torrent file from Telegram first.
	torrentFilePath := filepath.Join(jobDir, torrentDocumentFilename(doc))
	if err := b.downloadTelegramDocument(ctx, doc, torrentFilePath); err != nil {
		if ctx.Err() != nil {
			// Already marked as cancelled by CancelJob
			b.editJobFinalStatus(peer, statusMsgID, job, "🚫 Cancelled")
		} else {
			b.manager.SetFailed(job.ID, err)
			b.editJobFinalStatus(peer, statusMsgID, job, fmt.Sprintf("❌ Failed to fetch .torrent: %v", err))
		}
		return
	}

	localPath, err := downloader.DownloadTorrentFile(ctx, torrentFilePath, jobDir, progressFn)
	if err != nil {
		if ctx.Err() != nil {
			// Already marked as cancelled by CancelJob
			b.editJobFinalStatus(peer, statusMsgID, job, "🚫 Cancelled")
		} else {
			b.manager.SetFailed(job.ID, err)
			b.editJobFinalStatus(peer, statusMsgID, job, fmt.Sprintf("❌ Failed: %v", err))
		}
		return
	}

	updr := newUploader(b.api, b.cfg, b.manager, job.ID)
	b.runJobAfterDownload(ctx, job, peer, statusMsgID, localPath, asDoc, asZip, updr, progressFn)
}

// runJobAfterDownload handles stat → upload for a completed download.
func (b *Bot) runJobAfterDownload(ctx context.Context, job *store.Job, peer tg.InputPeerClass, statusMsgID int, localPath string, asDoc, asZip bool, updr *uploader, progressFn jobs.ProgressFunc) {
	info, err := os.Stat(localPath)
	if err != nil {
		b.manager.SetFailed(job.ID, err)
		b.editJobFinalStatus(peer, statusMsgID, job, fmt.Sprintf("❌ Failed: %v", err))
		return
	}

	if info.IsDir() {
		b.runJobDir(ctx, job, peer, statusMsgID, localPath, asDoc, asZip, updr, progressFn)
		return
	}

	filename := filepath.Base(localPath)
	b.manager.SetUploading(job.ID, filename)

	if err := updr.Upload(ctx, localPath, filename, info.Size(), peer, asDoc, progressFn); err != nil {
		if ctx.Err() != nil {
			// Already marked as cancelled by CancelJob
			b.editJobFinalStatus(peer, statusMsgID, job, "🚫 Cancelled")
		} else {
			b.manager.SetFailed(job.ID, err)
			b.editJobFinalStatus(peer, statusMsgID, job, fmt.Sprintf("❌ Upload failed: %v", err))
		}
		return
	}

	b.manager.SetDone(job.ID, info.Size())
	b.editJobFinalStatus(peer, statusMsgID, job, fmt.Sprintf("✅ Done! Uploaded: %s", filename))
}

// runJobDir handles uploading for a multi-file torrent (directory result).
// If asZip is true, all files are zipped into a single archive before upload.
// Otherwise, each file is uploaded individually with cumulative progress tracking.
func (b *Bot) runJobDir(ctx context.Context, job *store.Job, peer tg.InputPeerClass, statusMsgID int, dirPath string, asDoc, asZip bool, updr *uploader, progressFn jobs.ProgressFunc) {
	dirName := filepath.Base(dirPath)

	if asZip {
		zipPath := dirPath + ".zip"
		b.manager.SetFilename(job.ID, dirName+".zip (creating)")
		if err := zipDir(dirPath, zipPath); err != nil {
			b.manager.SetFailed(job.ID, err)
			b.editJobFinalStatus(peer, statusMsgID, job, fmt.Sprintf("❌ Failed to create zip: %v", err))
			return
		}

		zipInfo, err := os.Stat(zipPath)
		if err != nil {
			b.manager.SetFailed(job.ID, err)
			b.editJobFinalStatus(peer, statusMsgID, job, fmt.Sprintf("❌ Failed: %v", err))
			return
		}

		filename := dirName + ".zip"
		b.manager.SetUploading(job.ID, filename)

		if err := updr.Upload(ctx, zipPath, filename, zipInfo.Size(), peer, asDoc, progressFn); err != nil {
			if ctx.Err() != nil {
				// Already marked as cancelled by CancelJob
				b.editJobFinalStatus(peer, statusMsgID, job, "🚫 Cancelled")
			} else {
				b.manager.SetFailed(job.ID, err)
				b.editJobFinalStatus(peer, statusMsgID, job, fmt.Sprintf("❌ Upload failed: %v", err))
			}
			return
		}

		b.manager.SetDone(job.ID, zipInfo.Size())
		b.editJobFinalStatus(peer, statusMsgID, job, fmt.Sprintf("✅ Done! Uploaded: %s", filename))
		return
	}

	// Upload each file individually with cumulative progress.
	var allFiles []string
	var totalSize int64
	if err := filepath.Walk(dirPath, func(path string, fi os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if fi.IsDir() {
			return nil
		}
		allFiles = append(allFiles, path)
		totalSize += fi.Size()
		return nil
	}); err != nil {
		b.manager.SetFailed(job.ID, err)
		b.editJobFinalStatus(peer, statusMsgID, job, fmt.Sprintf("❌ Failed: %v", err))
		return
	}

	if len(allFiles) == 0 {
		b.manager.SetFailed(job.ID, fmt.Errorf("no files in torrent directory"))
		b.editJobFinalStatus(peer, statusMsgID, job, "❌ No files found in torrent.")
		return
	}

	b.manager.SetUploading(job.ID, fmt.Sprintf("%s (%d files)", dirName, len(allFiles)))

	var uploadedBytes int64
	for _, filePath := range allFiles {
		if ctx.Err() != nil {
			// Already marked as cancelled by CancelJob
			b.editJobFinalStatus(peer, statusMsgID, job, "🚫 Cancelled")
			return
		}

		fi, err := os.Stat(filePath)
		if err != nil {
			b.manager.SetFailed(job.ID, err)
			b.editJobFinalStatus(peer, statusMsgID, job, fmt.Sprintf("❌ Failed: %v", err))
			return
		}

		rel, err := filepath.Rel(dirPath, filePath)
		if err != nil {
			b.manager.SetFailed(job.ID, err)
			b.editJobFinalStatus(peer, statusMsgID, job, fmt.Sprintf("❌ Failed: %v", err))
			return
		}
		rel = filepath.ToSlash(rel)

		// Wrap progress to show cumulative bytes across all files.
		offset := uploadedBytes
		fileSize := fi.Size()
		wrapped := func(done, _, speed int64) {
			progressFn(offset+done, totalSize, speed)
		}

		if err := updr.Upload(ctx, filePath, rel, fileSize, peer, asDoc, wrapped); err != nil {
			if ctx.Err() != nil {
				// Already marked as cancelled by CancelJob
				b.editJobFinalStatus(peer, statusMsgID, job, "🚫 Cancelled")
			} else {
				b.manager.SetFailed(job.ID, err)
				b.editJobFinalStatus(peer, statusMsgID, job, fmt.Sprintf("❌ Upload failed: %v", err))
			}
			return
		}
		uploadedBytes += fileSize
	}

	b.manager.SetDone(job.ID, totalSize)
	b.editJobFinalStatus(peer, statusMsgID, job, fmt.Sprintf("✅ Done! Uploaded %d file(s) from: %s", len(allFiles), dirName))
}

// cmdStatus handles /status — sends a single auto-refreshing status message per chat.
func (b *Bot) cmdStatus(ctx context.Context, fromID, chatID int64, peer tg.InputPeerClass) {
	if !b.st.IsAllowed(fromID) {
		b.sendText(ctx, peer, "⛔ You are not authorized.")
		return
	}

	// Cancel the existing refresh goroutine and delete the old status message.
	b.statusMu.Lock()
	if old, ok := b.statusState[chatID]; ok {
		old.cancel()
		go b.deleteMessage(b.rootCtx, old.peer, old.msgID)
	}
	b.statusMu.Unlock()

	active := b.st.ActiveSorted()
	text := buildStatusText(active)
	msgID, err := b.sendStatusMessage(ctx, peer, text)
	if err != nil {
		log.Printf("cmdStatus: send: %v", err)
		return
	}

	refreshCtx, cancelRefresh := context.WithCancel(b.rootCtx)
	b.statusMu.Lock()
	b.statusState[chatID] = &statusEntry{
		msgID:    msgID,
		peer:     peer,
		cancel:   cancelRefresh,
		lastText: text,
	}
	b.statusMu.Unlock()

	if len(active) > 0 {
		go b.runStatusRefresh(refreshCtx, chatID)
	}
}

// runStatusRefresh edits the status message every 5 seconds until the context is
// cancelled or there are no more active jobs.
func (b *Bot) runStatusRefresh(ctx context.Context, chatID int64) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			b.statusMu.Lock()
			entry, ok := b.statusState[chatID]
			if !ok {
				b.statusMu.Unlock()
				return
			}

			active := b.st.ActiveSorted()
			text := buildStatusText(active)

			// Only edit if the text actually changed to avoid MESSAGE_NOT_MODIFIED
			if text != entry.lastText {
				entry.lastText = text
				b.statusMu.Unlock()

				apiCtx, cancel := context.WithTimeout(b.rootCtx, 30*time.Second)
				b.editStatusMessage(apiCtx, entry.peer, entry.msgID, text)
				cancel()
			} else {
				b.statusMu.Unlock()
			}

			if len(active) == 0 {
				return
			}
		}
	}
}

// handleCallbackQuery handles inline button presses (e.g. the Refresh button).
func (h *updateHandler) handleCallbackQuery(ctx context.Context, query *tg.UpdateBotCallbackQuery) {
	bot := h.bot

	// Always answer the callback to remove the loading indicator.
	defer func() {
		apiCtx, cancel := context.WithTimeout(bot.rootCtx, 10*time.Second)
		defer cancel()
		_, _ = bot.api.MessagesSetBotCallbackAnswer(apiCtx, &tg.MessagesSetBotCallbackAnswerRequest{
			QueryID: query.QueryID,
		})
	}()

	if string(query.Data) != "refresh_status" {
		return
	}
	if !bot.st.IsAllowed(query.UserID) {
		return
	}

	var chatID int64
	switch p := query.Peer.(type) {
	case *tg.PeerUser:
		chatID = p.UserID
	case *tg.PeerChat:
		chatID = p.ChatID
	case *tg.PeerChannel:
		chatID = p.ChannelID
	default:
		return
	}

	bot.statusMu.Lock()
	entry, ok := bot.statusState[chatID]
	if !ok || entry.msgID != query.MsgID {
		bot.statusMu.Unlock()
		return
	}

	active := bot.st.ActiveSorted()
	text := buildStatusText(active)

	// Only edit if the text actually changed to avoid MESSAGE_NOT_MODIFIED
	if text != entry.lastText {
		entry.lastText = text
		bot.statusMu.Unlock()
		bot.editStatusMessage(ctx, entry.peer, entry.msgID, text)
	} else {
		bot.statusMu.Unlock()
	}
}

// statusInlineMarkup returns the inline keyboard with a Refresh button.
func statusInlineMarkup() *tg.ReplyInlineMarkup {
	return &tg.ReplyInlineMarkup{
		Rows: []tg.KeyboardButtonRow{
			{
				Buttons: []tg.KeyboardButtonClass{
					&tg.KeyboardButtonCallback{
						Text: "🔄 Refresh",
						Data: []byte("refresh_status"),
					},
				},
			},
		},
	}
}

// sendStatusMessage sends a status message with the inline refresh keyboard and
// returns the new message ID.
func (b *Bot) sendStatusMessage(ctx context.Context, peer tg.InputPeerClass, text string) (int, error) {
	result, err := b.api.MessagesSendMessage(ctx, &tg.MessagesSendMessageRequest{
		Peer:        peer,
		Message:     text,
		RandomID:    cryptoRandInt63(),
		ReplyMarkup: statusInlineMarkup(),
	})
	if err != nil {
		return 0, err
	}
	return extractMessageID(result), nil
}

// editStatusMessage edits an existing status message in-place.
func (b *Bot) editStatusMessage(ctx context.Context, peer tg.InputPeerClass, msgID int, text string) {
	_, err := b.api.MessagesEditMessage(ctx, &tg.MessagesEditMessageRequest{
		Peer:        peer,
		ID:          msgID,
		Message:     text,
		ReplyMarkup: statusInlineMarkup(),
	})
	if err != nil {
		log.Printf("editStatusMessage: %v", err)
	}
}

// deleteMessage deletes a single message. For channels it uses the channel-specific API.
func (b *Bot) deleteMessage(ctx context.Context, peer tg.InputPeerClass, msgID int) {
	switch p := peer.(type) {
	case *tg.InputPeerChannel:
		_, err := b.api.ChannelsDeleteMessages(ctx, &tg.ChannelsDeleteMessagesRequest{
			Channel: &tg.InputChannel{
				ChannelID:  p.ChannelID,
				AccessHash: p.AccessHash,
			},
			ID: []int{msgID},
		})
		if err != nil {
			log.Printf("deleteMessage (channel): %v", err)
		}
	default:
		_, err := b.api.MessagesDeleteMessages(ctx, &tg.MessagesDeleteMessagesRequest{
			Revoke: true,
			ID:     []int{msgID},
		})
		if err != nil {
			log.Printf("deleteMessage: %v", err)
		}
	}
}

// extractMessageID pulls the real message ID out of an UpdatesClass response
// returned by MessagesSendMessage.
func extractMessageID(upds tg.UpdatesClass) int {
	switch u := upds.(type) {
	case *tg.Updates:
		for _, upd := range u.Updates {
			switch v := upd.(type) {
			case *tg.UpdateMessageID:
				return v.ID
			case *tg.UpdateNewMessage:
				if msg, ok := v.Message.(*tg.Message); ok {
					return msg.ID
				}
			}
		}
	case *tg.UpdateShortSentMessage:
		return u.ID
	}
	return 0
}

// buildStatusText formats the list of active jobs into a human-readable status string
// including a progress bar for each job.
func buildStatusText(active []*store.Job) string {
	if len(active) == 0 {
		return "✅ No active jobs."
	}
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("📋 Active jobs (%d):\n\n", len(active)))
	for _, j := range active {
		sb.WriteString(fmt.Sprintf("• %s [%s]\n", j.ID, j.Status))
		if j.Filename != "" {
			sb.WriteString(fmt.Sprintf("  📄 %s\n", j.Filename))
		}
		bar := makeProgressBar(j.Progress, 10)
		line := fmt.Sprintf("  %s %.1f%%", bar, j.Progress)
		if j.Speed > 0 {
			line += " @ " + fmtSpeed(j.Speed)
		}
		if j.TotalBytes > 0 {
			line += fmt.Sprintf(" (%s / %s)", fmtBytes(j.DoneBytes), fmtBytes(j.TotalBytes))
		}
		sb.WriteString(line + "\n\n")
	}
	return strings.TrimRight(sb.String(), "\n")
}

// makeProgressBar returns a text progress bar like [████░░░░░░] for the given percentage.
func makeProgressBar(pct float64, width int) string {
	if pct < 0 {
		pct = 0
	}
	if pct > 100 {
		pct = 100
	}
	filled := int(math.Round(pct / 100 * float64(width)))
	if filled > width {
		filled = width
	}
	return "[" + strings.Repeat("█", filled) + strings.Repeat("░", width-filled) + "]"
}

// fmtBytes formats a byte count as a human-readable string.
func fmtBytes(b int64) string {
	switch {
	case b >= 1024*1024*1024:
		return fmt.Sprintf("%.1f GB", float64(b)/(1024*1024*1024))
	case b >= 1024*1024:
		return fmt.Sprintf("%.1f MB", float64(b)/(1024*1024))
	case b >= 1024:
		return fmt.Sprintf("%.1f KB", float64(b)/1024)
	default:
		return fmt.Sprintf("%d B", b)
	}
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
	b.sendText(ctx, peer, fmt.Sprintf("🚫 Job %s cancelled.", jobID))
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

// sendNotify sends a status notification using the bot's root context so that
// it succeeds even when the job's own context has been cancelled.
func (b *Bot) sendNotify(peer tg.InputPeerClass, text string) {
	ctx, cancel := context.WithTimeout(b.rootCtx, 30*time.Second)
	defer cancel()
	b.sendText(ctx, peer, text)
}

// fmtSpeed formats bytes-per-second as a human-readable string.
func fmtSpeed(bps int64) string {
	switch {
	case bps >= 1024*1024:
		return fmt.Sprintf("%.1f MB/s", float64(bps)/(1024*1024))
	case bps >= 1024:
		return fmt.Sprintf("%.1f KB/s", float64(bps)/1024)
	default:
		return fmt.Sprintf("%d B/s", bps)
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

// sendJobStatusMessage sends an initial job status message replying to the command.
// Returns the message ID for future edits.
func (b *Bot) sendJobStatusMessage(ctx context.Context, peer tg.InputPeerClass, job *store.Job, replyTo int) int {
	text := buildSingleJobStatus(job)
	result, err := b.api.MessagesSendMessage(ctx, &tg.MessagesSendMessageRequest{
		Peer:       peer,
		Message:    text,
		RandomID:   cryptoRandInt63(),
		ReplyTo:    &tg.InputReplyToMessage{ReplyToMsgID: replyTo},
	})
	if err != nil {
		log.Printf("sendJobStatusMessage: %v", err)
		return 0
	}
	return extractMessageID(result)
}

// startJobProgressUpdater starts a goroutine that updates the status message periodically.
// Returns a stop function that should be called when the job completes.
func (b *Bot) startJobProgressUpdater(ctx context.Context, jobID string, peer tg.InputPeerClass, msgID int) func() {
	if msgID == 0 {
		return func() {}
	}

	stopCh := make(chan struct{})
	var lastText string

	go func() {
		ticker := time.NewTicker(3 * time.Second)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-stopCh:
				return
			case <-ticker.C:
				job, ok := b.st.Get(jobID)
				if !ok {
					return
				}
				text := buildSingleJobStatus(job)
				if text != lastText {
					lastText = text
					apiCtx, cancel := context.WithTimeout(b.rootCtx, 10*time.Second)
					_, err := b.api.MessagesEditMessage(apiCtx, &tg.MessagesEditMessageRequest{
						Peer:    peer,
						ID:      msgID,
						Message: text,
					})
					cancel()
					if err != nil && !strings.Contains(err.Error(), "MESSAGE_NOT_MODIFIED") {
						log.Printf("job progress update: %v", err)
					}
				}
			}
		}
	}()

	return func() { close(stopCh) }
}

// editJobFinalStatus edits the job status message with the final result and replies to original command.
func (b *Bot) editJobFinalStatus(peer tg.InputPeerClass, msgID int, job *store.Job, result string) {
	text := fmt.Sprintf("Job %s: %s", job.ID, result)
	if job.Filename != "" {
		text = fmt.Sprintf("Job %s [%s]: %s", job.ID, job.Filename, result)
	}

	if msgID != 0 {
		apiCtx, cancel := context.WithTimeout(b.rootCtx, 30*time.Second)
		_, err := b.api.MessagesEditMessage(apiCtx, &tg.MessagesEditMessageRequest{
			Peer:    peer,
			ID:      msgID,
			Message: text,
		})
		cancel()
		if err != nil && !strings.Contains(err.Error(), "MESSAGE_NOT_MODIFIED") {
			log.Printf("editJobFinalStatus: %v", err)
		}
	} else {
		// Fallback: send as reply if we don't have a status message
		b.sendReply(peer, job.ReplyToMsg, text)
	}
}

// sendReply sends a message replying to a specific message ID.
func (b *Bot) sendReply(peer tg.InputPeerClass, replyTo int, text string) {
	ctx, cancel := context.WithTimeout(b.rootCtx, 30*time.Second)
	defer cancel()

	req := &tg.MessagesSendMessageRequest{
		Peer:     peer,
		Message:  text,
		RandomID: cryptoRandInt63(),
	}
	if replyTo != 0 {
		req.ReplyTo = &tg.InputReplyToMessage{ReplyToMsgID: replyTo}
	}

	_, err := b.api.MessagesSendMessage(ctx, req)
	if err != nil {
		log.Printf("sendReply error: %v", err)
	}
}

// buildSingleJobStatus formats a single job's status for display.
func buildSingleJobStatus(j *store.Job) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("📥 Job %s [%s]\n", j.ID, j.Status))
	if j.Filename != "" {
		sb.WriteString(fmt.Sprintf("📄 %s\n", j.Filename))
	} else if j.URL != "" {
		// Show truncated URL if no filename yet
		url := j.URL
		if len(url) > 50 {
			url = url[:47] + "..."
		}
		sb.WriteString(fmt.Sprintf("🔗 %s\n", url))
	}
	bar := makeProgressBar(j.Progress, 10)
	line := fmt.Sprintf("%s %.1f%%", bar, j.Progress)
	if j.Speed > 0 {
		line += " @ " + fmtSpeed(j.Speed)
	}
	if j.TotalBytes > 0 {
		line += fmt.Sprintf(" (%s / %s)", fmtBytes(j.DoneBytes), fmtBytes(j.TotalBytes))
	}
	sb.WriteString(line)
	return sb.String()
}
