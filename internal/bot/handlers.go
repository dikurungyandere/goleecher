package bot

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"log"
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
			bot.cmdLeech(ctx, fromID, chatID, peer, url, asDoc, asZip)
			return
		}

		// No URL provided: look for a .torrent document in the replied-to message.
		replyHeader, ok := msg.ReplyTo.(*tg.MessageReplyHeader)
		if !ok || replyHeader.ReplyToMsgID == 0 {
			bot.sendText(ctx, peer, "Usage: /leech <url|magnet> [document] [zip]\nOr reply to a .torrent file with /leech [document] [zip]")
			return
		}
		bot.cmdLeechTorrentReply(ctx, fromID, chatID, peer, replyHeader.ReplyToMsgID, asDoc, asZip)
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
	b.sendText(ctx, peer, "⚡ goleecher ready!\n\nCommands:\n/leech <url|magnet> — download & upload\n/leech <url|magnet> document — force document upload\n/leech <url|magnet> zip — zip torrent before upload\n/leech — reply to a .torrent file to leech it\n/status — show active jobs\n/cancel <id> — cancel a job")
}

// cmdLeech handles /leech.
func (b *Bot) cmdLeech(ctx context.Context, fromID, chatID int64, peer tg.InputPeerClass, url string, asDoc, asZip bool) {
	if !b.st.IsAllowed(fromID) {
		b.sendText(ctx, peer, "⛔ You are not authorized to use this bot.")
		return
	}

	job, jobCtx, cancel := b.manager.NewJob(b.rootCtx, fromID, chatID, url)
	_ = cancel

	b.sendText(ctx, peer, fmt.Sprintf("✅ Job %s created. Starting download…", job.ID))

	go func() {
		b.runJob(jobCtx, job, peer, asDoc, asZip)
	}()
}

// cmdLeechTorrentReply handles /leech when replying to a .torrent file message.
func (b *Bot) cmdLeechTorrentReply(ctx context.Context, fromID, chatID int64, peer tg.InputPeerClass, replyMsgID int, asDoc, asZip bool) {
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

	job, jobCtx, cancel := b.manager.NewJob(b.rootCtx, fromID, chatID, torrentDocumentFilename(doc))
	_ = cancel

	b.sendText(ctx, peer, fmt.Sprintf("✅ Job %s created. Starting download…", job.ID))

	go func() {
		b.runJobFromTorrentDocument(jobCtx, job, peer, doc, asDoc, asZip)
	}()
}

// runJob executes a full download → upload cycle for a job.
func (b *Bot) runJob(ctx context.Context, job *store.Job, peer tg.InputPeerClass, asDoc, asZip bool) {
	jobDir := filepath.Join(b.cfg.TempDir, job.ID)
	if err := os.MkdirAll(jobDir, 0o755); err != nil {
		b.manager.SetFailed(job.ID, err)
		b.sendNotify(peer, fmt.Sprintf("❌ Job %s failed: %v", job.ID, err))
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
		nameCallback := func(name string) { b.manager.SetFilename(job.ID, name) }
		localPath, err = downloader.DownloadHTTP(ctx, job.URL, jobDir, progressFn, nameCallback)
	}

	if err != nil {
		if ctx.Err() != nil {
			b.manager.SetCancelled(job.ID)
			b.sendNotify(peer, fmt.Sprintf("🚫 Job %s cancelled.", job.ID))
		} else {
			b.manager.SetFailed(job.ID, err)
			b.sendNotify(peer, fmt.Sprintf("❌ Job %s failed: %v", job.ID, err))
		}
		return
	}

	updr := newUploader(b.api, b.cfg, b.manager, job.ID)
	b.runJobAfterDownload(ctx, job, peer, localPath, asDoc, asZip, updr, progressFn)
}

// runJobFromTorrentDocument downloads a .torrent document from Telegram and then runs the torrent.
func (b *Bot) runJobFromTorrentDocument(ctx context.Context, job *store.Job, peer tg.InputPeerClass, doc *tg.Document, asDoc, asZip bool) {
	jobDir := filepath.Join(b.cfg.TempDir, job.ID)
	if err := os.MkdirAll(jobDir, 0o755); err != nil {
		b.manager.SetFailed(job.ID, err)
		b.sendNotify(peer, fmt.Sprintf("❌ Job %s failed: %v", job.ID, err))
		return
	}
	defer os.RemoveAll(jobDir)

	b.manager.SetDownloading(job.ID)
	progressFn := b.manager.ProgressUpdater(job.ID)

	// Download the .torrent file from Telegram first.
	torrentFilePath := filepath.Join(jobDir, torrentDocumentFilename(doc))
	if err := b.downloadTelegramDocument(ctx, doc, torrentFilePath); err != nil {
		if ctx.Err() != nil {
			b.manager.SetCancelled(job.ID)
			b.sendNotify(peer, fmt.Sprintf("🚫 Job %s cancelled.", job.ID))
		} else {
			b.manager.SetFailed(job.ID, err)
			b.sendNotify(peer, fmt.Sprintf("❌ Job %s failed to fetch .torrent: %v", job.ID, err))
		}
		return
	}

	localPath, err := downloader.DownloadTorrentFile(ctx, torrentFilePath, jobDir, progressFn)
	if err != nil {
		if ctx.Err() != nil {
			b.manager.SetCancelled(job.ID)
			b.sendNotify(peer, fmt.Sprintf("🚫 Job %s cancelled.", job.ID))
		} else {
			b.manager.SetFailed(job.ID, err)
			b.sendNotify(peer, fmt.Sprintf("❌ Job %s failed: %v", job.ID, err))
		}
		return
	}

	updr := newUploader(b.api, b.cfg, b.manager, job.ID)
	b.runJobAfterDownload(ctx, job, peer, localPath, asDoc, asZip, updr, progressFn)
}

// runJobAfterDownload handles stat → upload for a completed download.
func (b *Bot) runJobAfterDownload(ctx context.Context, job *store.Job, peer tg.InputPeerClass, localPath string, asDoc, asZip bool, updr *uploader, progressFn jobs.ProgressFunc) {
	info, err := os.Stat(localPath)
	if err != nil {
		b.manager.SetFailed(job.ID, err)
		b.sendNotify(peer, fmt.Sprintf("❌ Job %s failed: %v", job.ID, err))
		return
	}

	if info.IsDir() {
		b.runJobDir(ctx, job, peer, localPath, asDoc, asZip, updr, progressFn)
		return
	}

	filename := filepath.Base(localPath)
	b.manager.SetUploading(job.ID, filename)

	if err := updr.Upload(ctx, localPath, filename, info.Size(), peer, asDoc, progressFn); err != nil {
		if ctx.Err() != nil {
			b.manager.SetCancelled(job.ID)
			b.sendNotify(peer, fmt.Sprintf("🚫 Job %s cancelled.", job.ID))
		} else {
			b.manager.SetFailed(job.ID, err)
			b.sendNotify(peer, fmt.Sprintf("❌ Job %s upload failed: %v", job.ID, err))
		}
		return
	}

	b.manager.SetDone(job.ID, info.Size())
	b.sendNotify(peer, fmt.Sprintf("✅ Job %s done! Uploaded: %s", job.ID, filename))
}

// runJobDir handles uploading for a multi-file torrent (directory result).
// If asZip is true, all files are zipped into a single archive before upload.
// Otherwise, each file is uploaded individually with cumulative progress tracking.
func (b *Bot) runJobDir(ctx context.Context, job *store.Job, peer tg.InputPeerClass, dirPath string, asDoc, asZip bool, updr *uploader, progressFn jobs.ProgressFunc) {
	dirName := filepath.Base(dirPath)

	if asZip {
		zipPath := dirPath + ".zip"
		b.sendNotify(peer, fmt.Sprintf("📦 Job %s: creating zip archive…", job.ID))
		if err := zipDir(dirPath, zipPath); err != nil {
			b.manager.SetFailed(job.ID, err)
			b.sendNotify(peer, fmt.Sprintf("❌ Job %s failed to create zip: %v", job.ID, err))
			return
		}

		zipInfo, err := os.Stat(zipPath)
		if err != nil {
			b.manager.SetFailed(job.ID, err)
			b.sendNotify(peer, fmt.Sprintf("❌ Job %s failed: %v", job.ID, err))
			return
		}

		filename := dirName + ".zip"
		b.manager.SetUploading(job.ID, filename)

		if err := updr.Upload(ctx, zipPath, filename, zipInfo.Size(), peer, asDoc, progressFn); err != nil {
			if ctx.Err() != nil {
				b.manager.SetCancelled(job.ID)
				b.sendNotify(peer, fmt.Sprintf("🚫 Job %s cancelled.", job.ID))
			} else {
				b.manager.SetFailed(job.ID, err)
				b.sendNotify(peer, fmt.Sprintf("❌ Job %s upload failed: %v", job.ID, err))
			}
			return
		}

		b.manager.SetDone(job.ID, zipInfo.Size())
		b.sendNotify(peer, fmt.Sprintf("✅ Job %s done! Uploaded: %s", job.ID, filename))
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
		b.sendNotify(peer, fmt.Sprintf("❌ Job %s failed: %v", job.ID, err))
		return
	}

	if len(allFiles) == 0 {
		b.manager.SetFailed(job.ID, fmt.Errorf("no files in torrent directory"))
		b.sendNotify(peer, fmt.Sprintf("❌ Job %s: no files found.", job.ID))
		return
	}

	b.manager.SetUploading(job.ID, dirName)
	b.sendNotify(peer, fmt.Sprintf("📤 Job %s: uploading %d file(s)…", job.ID, len(allFiles)))

	var uploadedBytes int64
	for _, filePath := range allFiles {
		if ctx.Err() != nil {
			b.manager.SetCancelled(job.ID)
			b.sendNotify(peer, fmt.Sprintf("🚫 Job %s cancelled.", job.ID))
			return
		}

		fi, err := os.Stat(filePath)
		if err != nil {
			b.manager.SetFailed(job.ID, err)
			b.sendNotify(peer, fmt.Sprintf("❌ Job %s failed: %v", job.ID, err))
			return
		}

		rel, err := filepath.Rel(dirPath, filePath)
		if err != nil {
			b.manager.SetFailed(job.ID, err)
			b.sendNotify(peer, fmt.Sprintf("❌ Job %s failed: %v", job.ID, err))
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
				b.manager.SetCancelled(job.ID)
				b.sendNotify(peer, fmt.Sprintf("🚫 Job %s cancelled.", job.ID))
			} else {
				b.manager.SetFailed(job.ID, err)
				b.sendNotify(peer, fmt.Sprintf("❌ Job %s upload failed: %v", job.ID, err))
			}
			return
		}
		uploadedBytes += fileSize
	}

	b.manager.SetDone(job.ID, totalSize)
	b.sendNotify(peer, fmt.Sprintf("✅ Job %s done! Uploaded %d file(s) from: %s", job.ID, len(allFiles), dirName))
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
		speed := ""
		if j.Speed > 0 {
			speed = " @ " + fmtSpeed(j.Speed)
		}
		sb.WriteString(fmt.Sprintf(
			"• %s [%s] %.1f%%%s — %s\n",
			j.ID, j.Status, j.Progress, speed, j.Filename,
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
