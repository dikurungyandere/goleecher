package bot

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/gotd/td/tg"
)

const tgDownloadChunkSize = 512 * 1024 // 512 KB per chunk

// fetchMessage retrieves a single message by ID from the given peer.
func (b *Bot) fetchMessage(ctx context.Context, peer tg.InputPeerClass, msgID int) (tg.MessageClass, error) {
	ids := []tg.InputMessageClass{&tg.InputMessageID{ID: msgID}}

	var result tg.MessagesMessagesClass
	var err error

	if ch, ok := peer.(*tg.InputPeerChannel); ok {
		result, err = b.api.ChannelsGetMessages(ctx, &tg.ChannelsGetMessagesRequest{
			Channel: &tg.InputChannel{ChannelID: ch.ChannelID, AccessHash: ch.AccessHash},
			ID:      ids,
		})
	} else {
		result, err = b.api.MessagesGetMessages(ctx, ids)
	}

	if err != nil {
		return nil, fmt.Errorf("get message: %w", err)
	}

	modified, ok := result.AsModified()
	if !ok {
		return nil, fmt.Errorf("message not found")
	}

	msgs := modified.GetMessages()
	if len(msgs) == 0 {
		return nil, fmt.Errorf("message not found")
	}

	return msgs[0], nil
}

// downloadTelegramDocument downloads a Telegram document to destPath.
func (b *Bot) downloadTelegramDocument(ctx context.Context, doc *tg.Document, destPath string) error {
	f, err := os.Create(destPath)
	if err != nil {
		return fmt.Errorf("create file: %w", err)
	}
	defer f.Close()

	location := &tg.InputDocumentFileLocation{
		ID:            doc.ID,
		AccessHash:    doc.AccessHash,
		FileReference: doc.FileReference,
	}

	var offset int64
	for {
		result, err := b.api.UploadGetFile(ctx, &tg.UploadGetFileRequest{
			Location: location,
			Offset:   offset,
			Limit:    tgDownloadChunkSize,
		})
		if err != nil {
			return fmt.Errorf("get file chunk: %w", err)
		}

		fileData, ok := result.(*tg.UploadFile)
		if !ok {
			return fmt.Errorf("CDN redirect not supported for torrent files")
		}

		if len(fileData.Bytes) == 0 {
			break
		}

		if _, err := f.Write(fileData.Bytes); err != nil {
			return fmt.Errorf("write chunk: %w", err)
		}

		offset += int64(len(fileData.Bytes))
		if len(fileData.Bytes) < tgDownloadChunkSize {
			break
		}
	}

	return nil
}

// isTorrentDocument reports whether doc is a .torrent file.
func isTorrentDocument(doc *tg.Document) bool {
	if doc.MimeType == "application/x-bittorrent" {
		return true
	}
	for _, attr := range doc.Attributes {
		if fn, ok := attr.(*tg.DocumentAttributeFilename); ok {
			if strings.HasSuffix(strings.ToLower(fn.FileName), ".torrent") {
				return true
			}
		}
	}
	return false
}

// torrentDocumentFilename returns the filename of a torrent document.
func torrentDocumentFilename(doc *tg.Document) string {
	for _, attr := range doc.Attributes {
		if fn, ok := attr.(*tg.DocumentAttributeFilename); ok {
			return fn.FileName
		}
	}
	return fmt.Sprintf("%d.torrent", doc.ID)
}
