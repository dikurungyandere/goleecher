package downloader

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	gotorrent "github.com/anacrolix/torrent"

	"github.com/dikurungyandere/goleecher/internal/jobs"
)

const torrentProgressTick = time.Second

// DownloadTorrent downloads a magnet link to destDir.
// It returns the path of the downloaded content (file or directory).
func DownloadTorrent(ctx context.Context, magnetURI, destDir string, progress jobs.ProgressFunc) (string, error) {
	client, err := newTorrentClient(destDir)
	if err != nil {
		return "", err
	}
	defer client.Close()

	t, err := client.AddMagnet(magnetURI)
	if err != nil {
		return "", fmt.Errorf("add magnet: %w", err)
	}

	return runTorrentDownload(ctx, t, destDir, progress)
}

// DownloadTorrentFile downloads using a local .torrent file to destDir.
// It returns the path of the downloaded content (file or directory).
func DownloadTorrentFile(ctx context.Context, torrentPath, destDir string, progress jobs.ProgressFunc) (string, error) {
	client, err := newTorrentClient(destDir)
	if err != nil {
		return "", err
	}
	defer client.Close()

	t, err := client.AddTorrentFromFile(torrentPath)
	if err != nil {
		return "", fmt.Errorf("add torrent file: %w", err)
	}

	return runTorrentDownload(ctx, t, destDir, progress)
}

// newTorrentClient creates a torrent client configured to download into destDir.
func newTorrentClient(destDir string) (*gotorrent.Client, error) {
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return nil, fmt.Errorf("mkdir destDir: %w", err)
	}

	cfg := gotorrent.NewDefaultClientConfig()
	cfg.DataDir = destDir
	cfg.NoUpload = true
	cfg.Seed = false

	client, err := gotorrent.NewClient(cfg)
	if err != nil {
		return nil, fmt.Errorf("torrent client: %w", err)
	}
	return client, nil
}

// runTorrentDownload waits for torrent metadata, downloads all files, and returns the local path.
func runTorrentDownload(ctx context.Context, t *gotorrent.Torrent, destDir string, progress jobs.ProgressFunc) (string, error) {
	// Wait for metadata
	select {
	case <-t.GotInfo():
	case <-ctx.Done():
		return "", ctx.Err()
	}

	t.DownloadAll()

	total := t.Length()
	ticker := time.NewTicker(torrentProgressTick)
	defer ticker.Stop()

	var lastDone int64
	lastTime := time.Now()

	for {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-ticker.C:
			done := t.BytesCompleted()
			now := time.Now()
			elapsed := now.Sub(lastTime).Seconds()
			var speed int64
			if elapsed > 0 {
				speed = int64(float64(done-lastDone) / elapsed)
			}
			lastDone = done
			lastTime = now
			if progress != nil {
				progress(done, total, speed)
			}
			if done >= total && total > 0 {
				goto done
			}
		case <-t.Complete().On():
			goto done
		}
	}

done:
	if progress != nil {
		progress(total, total, 0)
	}

	// Determine the output path: single file or directory
	files := t.Files()
	name := t.Name()
	destPath := filepath.Join(destDir, name)

	if len(files) == 1 {
		destPath = filepath.Join(destDir, files[0].Path())
	}

	return destPath, nil
}
