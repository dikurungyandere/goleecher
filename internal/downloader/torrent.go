package downloader

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	gotorrent "github.com/anacrolix/torrent"
	"github.com/anacrolix/torrent/metainfo"
	"github.com/anacrolix/torrent/storage"

	"github.com/dikurungyandere/goleecher/internal/jobs"
)

const torrentProgressTick = time.Second

var (
	sharedClient   *gotorrent.Client
	sharedClientMu sync.Mutex
	activeJobs     int
)

// getSharedClient returns the shared torrent client, creating it if needed.
func getSharedClient() (*gotorrent.Client, error) {
	sharedClientMu.Lock()
	defer sharedClientMu.Unlock()

	if sharedClient != nil {
		activeJobs++
		return sharedClient, nil
	}

	cfg := gotorrent.NewDefaultClientConfig()
	cfg.DefaultStorage = storage.NewFileByInfoHash(os.TempDir())
	cfg.NoUpload = true
	cfg.Seed = false
	cfg.ListenPort = 0 // Let OS assign a random port

	client, err := gotorrent.NewClient(cfg)
	if err != nil {
		return nil, fmt.Errorf("torrent client: %w", err)
	}

	sharedClient = client
	activeJobs = 1
	return sharedClient, nil
}

// releaseSharedClient decrements the active job count and closes the client if no jobs remain.
func releaseSharedClient() {
	sharedClientMu.Lock()
	defer sharedClientMu.Unlock()

	activeJobs--
	if activeJobs <= 0 && sharedClient != nil {
		sharedClient.Close()
		sharedClient = nil
		activeJobs = 0
	}
}

// DownloadTorrent downloads a magnet link to destDir.
// It returns the path of the downloaded content (file or directory).
func DownloadTorrent(ctx context.Context, magnetURI, destDir string, progress jobs.ProgressFunc) (string, error) {
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return "", fmt.Errorf("mkdir destDir: %w", err)
	}

	client, err := getSharedClient()
	if err != nil {
		return "", err
	}

	spec, err := gotorrent.TorrentSpecFromMagnetUri(magnetURI)
	if err != nil {
		releaseSharedClient()
		return "", fmt.Errorf("parse magnet: %w", err)
	}
	spec.Storage = storage.NewFile(destDir)

	t, _, err := client.AddTorrentSpec(spec)
	if err != nil {
		releaseSharedClient()
		return "", fmt.Errorf("add magnet: %w", err)
	}

	result, err := runTorrentDownload(ctx, t, destDir, progress)

	// Drop the torrent and release the client BEFORE returning.
	// This ensures the torrent's timers are stopped before the caller
	// might clean up the download directory.
	t.Drop()
	// Brief pause to let any pending timer callbacks complete after Drop.
	time.Sleep(100 * time.Millisecond)
	releaseSharedClient()

	return result, err
}

// DownloadTorrentFile downloads using a local .torrent file to destDir.
// It returns the path of the downloaded content (file or directory).
func DownloadTorrentFile(ctx context.Context, torrentPath, destDir string, progress jobs.ProgressFunc) (string, error) {
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return "", fmt.Errorf("mkdir destDir: %w", err)
	}

	client, err := getSharedClient()
	if err != nil {
		return "", err
	}

	mi, err := metainfo.LoadFromFile(torrentPath)
	if err != nil {
		releaseSharedClient()
		return "", fmt.Errorf("load torrent file: %w", err)
	}

	spec := gotorrent.TorrentSpecFromMetaInfo(mi)
	spec.Storage = storage.NewFile(destDir)

	t, _, err := client.AddTorrentSpec(spec)
	if err != nil {
		releaseSharedClient()
		return "", fmt.Errorf("add torrent: %w", err)
	}

	result, err := runTorrentDownload(ctx, t, destDir, progress)

	// Drop the torrent and release the client BEFORE returning.
	// This ensures the torrent's timers are stopped before the caller
	// might clean up the download directory.
	t.Drop()
	// Brief pause to let any pending timer callbacks complete after Drop.
	time.Sleep(100 * time.Millisecond)
	releaseSharedClient()

	return result, err
}

// DownloadTorrentURL downloads a .torrent file from a URL and then downloads its content.
func DownloadTorrentURL(ctx context.Context, torrentURL, destDir string, progress jobs.ProgressFunc) (string, error) {
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return "", fmt.Errorf("mkdir destDir: %w", err)
	}

	// Download the .torrent file first
	torrentPath := filepath.Join(destDir, "meta.torrent")
	if err := downloadFile(ctx, torrentURL, torrentPath); err != nil {
		return "", fmt.Errorf("download .torrent: %w", err)
	}

	return DownloadTorrentFile(ctx, torrentPath, destDir, progress)
}

// downloadFile downloads a URL to a local file path.
func downloadFile(ctx context.Context, url, destPath string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	f, err := os.Create(destPath)
	if err != nil {
		return err
	}
	defer f.Close()

	_, err = io.Copy(f, resp.Body)
	return err
}

// IsTorrentURL checks if a URL points to a .torrent file.
func IsTorrentURL(url string) bool {
	lower := strings.ToLower(url)
	// Check for .torrent extension (ignore query params)
	if idx := strings.Index(lower, "?"); idx > 0 {
		lower = lower[:idx]
	}
	return strings.HasSuffix(lower, ".torrent")
}

// runTorrentDownload waits for torrent metadata, downloads all files, and returns the local path.
func runTorrentDownload(ctx context.Context, t *gotorrent.Torrent, destDir string, progress jobs.ProgressFunc) (string, error) {
	// Wait for metadata
	select {
	case <-t.GotInfo():
	case <-ctx.Done():
		return "", ctx.Err()
	}

	// Set download directory for this torrent's files
	for _, file := range t.Files() {
		file.Download()
	}

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

	// Wait a bit for files to be flushed
	time.Sleep(500 * time.Millisecond)

	// Determine the output path: single file or directory
	// Files are stored directly in destDir via storage.NewFile(destDir)
	files := t.Files()
	name := t.Name()
	destPath := filepath.Join(destDir, name)

	if len(files) == 1 {
		destPath = filepath.Join(destDir, files[0].Path())
	}

	// Verify file exists
	if _, err := os.Stat(destPath); err != nil {
		return "", err
	}

	return destPath, nil
}
