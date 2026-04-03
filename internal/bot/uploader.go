package bot

import (
	"context"
	"fmt"
	"io"
	"mime"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gotd/td/tg"

	"github.com/dikurungyandere/goleecher/internal/config"
	"github.com/dikurungyandere/goleecher/internal/jobs"
	"github.com/dikurungyandere/goleecher/internal/splitter"
)

const (
	// Telegram upload limits
	bigFileThreshold = 10 * 1024 * 1024  // 10 MB: use big file API above this
	partSize         = 512 * 1024        // 512 KB per part
	uploadWorkers    = 4
)

type uploader struct {
	api     *tg.Client
	cfg     *config.Config
	manager *jobs.Manager
	jobID   string
}

func newUploader(api *tg.Client, cfg *config.Config, manager *jobs.Manager, jobID string) *uploader {
	return &uploader{api: api, cfg: cfg, manager: manager, jobID: jobID}
}

// Upload splits the file if needed and uploads all parts to Telegram.
func (u *uploader) Upload(ctx context.Context, filePath, filename string, size int64, peer tg.InputPeerClass, asDoc bool, progress jobs.ProgressFunc) error {
	parts, err := splitter.Split(filePath)
	if err != nil {
		return fmt.Errorf("split: %w", err)
	}

	// Clean up split parts (but NOT the original if no split happened)
	isSplit := len(parts) > 1
	if isSplit {
		defer func() {
			for _, p := range parts {
				os.Remove(p)
			}
		}()
	}

	for i, part := range parts {
		partName := filename
		if isSplit {
			partName = fmt.Sprintf("%s.part%03d", filename, i+1)
		}

		info, err := os.Stat(part)
		if err != nil {
			return fmt.Errorf("stat part %d: %w", i+1, err)
		}

		inputFile, err := u.uploadFile(ctx, part, partName, info.Size(), progress)
		if err != nil {
			return fmt.Errorf("upload part %d: %w", i+1, err)
		}

		if err := u.sendFile(ctx, peer, inputFile, partName, asDoc); err != nil {
			return fmt.Errorf("send part %d: %w", i+1, err)
		}
	}

	return nil
}

// uploadFile uploads a single file to Telegram and returns the InputFile reference.
func (u *uploader) uploadFile(ctx context.Context, filePath, filename string, size int64, progress jobs.ProgressFunc) (tg.InputFileClass, error) {
	f, err := os.Open(filePath)
	if err != nil {
		return nil, fmt.Errorf("open: %w", err)
	}
	defer f.Close()

	fileID := cryptoRandInt63()
	totalParts := int((size + partSize - 1) / partSize)
	isBig := size > bigFileThreshold

	type partWork struct {
		index int
		data  []byte
	}

	workCh := make(chan partWork, uploadWorkers*2)
	var uploadErr atomic.Value
	var wg sync.WaitGroup
	var uploaded int64

	// abort is closed when a worker encounters an error so the reader can unblock.
	abort := make(chan struct{})
	var abortOnce sync.Once
	doAbort := func() { abortOnce.Do(func() { close(abort) }) }

	startTime := time.Now()

	// Worker goroutines
	for w := 0; w < uploadWorkers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for pw := range workCh {
				if ctx.Err() != nil {
					return
				}
				if uploadErr.Load() != nil {
					return
				}
				var werr error
				if isBig {
					_, werr = u.api.UploadSaveBigFilePart(ctx, &tg.UploadSaveBigFilePartRequest{
						FileID:         fileID,
						FilePart:       pw.index,
						FileTotalParts: totalParts,
						Bytes:          pw.data,
					})
				} else {
					_, werr = u.api.UploadSaveFilePart(ctx, &tg.UploadSaveFilePartRequest{
						FileID:   fileID,
						FilePart: pw.index,
						Bytes:    pw.data,
					})
				}
				if werr != nil {
					uploadErr.Store(werr)
					doAbort()
					return
				}
				cur := atomic.AddInt64(&uploaded, int64(len(pw.data)))
				if progress != nil {
					elapsed := time.Since(startTime).Seconds()
					var speed int64
					if elapsed > 0 {
						speed = int64(float64(cur) / elapsed)
					}
					progress(cur, size, speed)
				}
			}
		}()
	}

	// Read file and dispatch parts
	buf := make([]byte, partSize)
	for partIdx := 0; partIdx < totalParts; partIdx++ {
		if ctx.Err() != nil {
			close(workCh)
			wg.Wait()
			return nil, ctx.Err()
		}
		if uploadErr.Load() != nil {
			close(workCh)
			wg.Wait()
			return nil, uploadErr.Load().(error)
		}

		n, err := io.ReadFull(f, buf)
		if err != nil && err != io.ErrUnexpectedEOF && err != io.EOF {
			close(workCh)
			wg.Wait()
			return nil, fmt.Errorf("read part %d: %w", partIdx, err)
		}
		if n == 0 {
			break
		}

		data := make([]byte, n)
		copy(data, buf[:n])

		// Use select so we don't block forever if workers have stopped.
		select {
		case workCh <- partWork{index: partIdx, data: data}:
		case <-ctx.Done():
			close(workCh)
			wg.Wait()
			return nil, ctx.Err()
		case <-abort:
			close(workCh)
			wg.Wait()
			if v := uploadErr.Load(); v != nil {
				return nil, v.(error)
			}
			return nil, fmt.Errorf("upload aborted")
		}
	}

	close(workCh)
	wg.Wait()

	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if v := uploadErr.Load(); v != nil {
		return nil, v.(error)
	}

	if isBig {
		return &tg.InputFileBig{
			ID:    fileID,
			Parts: totalParts,
			Name:  filename,
		}, nil
	}
	return &tg.InputFile{
		ID:    fileID,
		Parts: totalParts,
		Name:  filename,
	}, nil
}

// sendFile sends an uploaded file as a document or media to the peer.
func (u *uploader) sendFile(ctx context.Context, peer tg.InputPeerClass, inputFile tg.InputFileClass, filename string, asDoc bool) error {
	mimeType := mime.TypeByExtension(filepath.Ext(filename))
	if mimeType == "" {
		mimeType = "application/octet-stream"
	}

	media := &tg.InputMediaUploadedDocument{
		File:     inputFile,
		MimeType: mimeType,
		Attributes: []tg.DocumentAttributeClass{
			&tg.DocumentAttributeFilename{FileName: filename},
		},
	}
	if asDoc {
		media.ForceFile = true
	}

	_, err := u.api.MessagesSendMedia(ctx, &tg.MessagesSendMediaRequest{
		Peer:     peer,
		Media:    media,
		RandomID: cryptoRandInt63(),
		Message:  "",
	})
	return err
}
