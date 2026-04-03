package downloader

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/forest6511/gdl"

	"github.com/dikurungyandere/goleecher/internal/jobs"
)

// DownloadHTTP downloads a URL to destDir using gdl and returns the local file path.
// nameCallback, if non-nil, is called with the resolved filename before the download begins.
func DownloadHTTP(ctx context.Context, url, destDir string, progress jobs.ProgressFunc, nameCallback func(string)) (string, error) {
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return "", fmt.Errorf("mkdir: %w", err)
	}

	// Resolve filename via a lightweight HEAD-equivalent call.
	info, err := gdl.GetFileInfo(ctx, url)
	if err != nil {
		return "", fmt.Errorf("get file info: %w", err)
	}

	filename := info.Filename
	if filename == "" {
		filename = "download"
	}

	if nameCallback != nil {
		nameCallback(filename)
	}

	destPath := filepath.Join(destDir, filename)

	var progressCallback gdl.ProgressCallback
	if progress != nil {
		progressCallback = func(p gdl.Progress) {
			progress(p.BytesDownloaded, p.TotalSize, p.Speed)
		}
	}

	opts := &gdl.Options{
		ProgressCallback:  progressCallback,
		OverwriteExisting: true,
		Quiet:             true,
	}

	if _, err := gdl.DownloadWithOptions(ctx, url, destPath, opts); err != nil {
		return "", fmt.Errorf("download: %w", err)
	}

	return destPath, nil
}
