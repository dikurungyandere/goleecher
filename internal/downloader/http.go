package downloader

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"github.com/dikurungyandere/goleecher/internal/jobs"
)

const (
	httpChunkSize    = 8 * 1024 * 1024 // 8 MB per chunk
	httpWorkers      = 4
	httpProgressTick = 500 * time.Millisecond
)

// DownloadHTTP downloads a URL to destDir and returns the local file path.
// It attempts concurrent chunked downloading; falls back to single stream if
// the server does not support range requests.
func DownloadHTTP(ctx context.Context, url, destDir string, progress jobs.ProgressFunc) (string, error) {
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return "", fmt.Errorf("mkdir: %w", err)
	}

	// HEAD request to get content info
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, url, nil)
	if err != nil {
		return "", fmt.Errorf("head request: %w", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("head: %w", err)
	}
	resp.Body.Close()

	filename := filenameFromURL(url, resp)
	destPath := filepath.Join(destDir, filename)

	contentLength := resp.ContentLength
	acceptsRanges := resp.Header.Get("Accept-Ranges") == "bytes"

	if contentLength <= 0 || !acceptsRanges {
		return downloadSingle(ctx, url, destPath, progress)
	}

	return downloadChunked(ctx, url, destPath, contentLength, progress)
}

func downloadSingle(ctx context.Context, url, destPath string, progress jobs.ProgressFunc) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("get: %w", err)
	}
	defer resp.Body.Close()

	f, err := os.Create(destPath)
	if err != nil {
		return "", fmt.Errorf("create file: %w", err)
	}
	defer f.Close()

	total := resp.ContentLength
	var done int64
	var mu sync.Mutex
	lastTime := time.Now()
	var lastDone int64

	ticker := time.NewTicker(httpProgressTick)
	defer ticker.Stop()

	go func() {
		for range ticker.C {
			mu.Lock()
			d := done
			now := time.Now()
			elapsed := now.Sub(lastTime).Seconds()
			var speed int64
			if elapsed > 0 {
				speed = int64(float64(d-lastDone) / elapsed)
			}
			lastDone = d
			lastTime = now
			mu.Unlock()
			if progress != nil {
				progress(d, total, speed)
			}
		}
	}()

	buf := make([]byte, 32*1024)
	for {
		n, rerr := resp.Body.Read(buf)
		if n > 0 {
			if _, werr := f.Write(buf[:n]); werr != nil {
				return "", fmt.Errorf("write: %w", werr)
			}
			mu.Lock()
			done += int64(n)
			mu.Unlock()
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			return "", fmt.Errorf("read: %w", rerr)
		}
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
	}

	if progress != nil {
		progress(done, total, 0)
	}
	return destPath, nil
}

type chunkResult struct {
	index int
	err   error
}

func downloadChunked(ctx context.Context, url, destPath string, total int64, progress jobs.ProgressFunc) (string, error) {
	f, err := os.Create(destPath)
	if err != nil {
		return "", fmt.Errorf("create file: %w", err)
	}
	defer f.Close()

	// Pre-allocate file
	if err := f.Truncate(total); err != nil {
		return "", fmt.Errorf("truncate: %w", err)
	}

	type chunk struct {
		index int
		start int64
		end   int64
	}

	var chunks []chunk
	for i := int64(0); i*httpChunkSize < total; i++ {
		start := i * httpChunkSize
		end := start + httpChunkSize - 1
		if end >= total {
			end = total - 1
		}
		chunks = append(chunks, chunk{index: int(i), start: start, end: end})
	}

	workCh := make(chan chunk, len(chunks))
	for _, c := range chunks {
		workCh <- c
	}
	close(workCh)

	var (
		mu       sync.Mutex
		done     int64
		lastDone int64
		lastTime = time.Now()
	)

	errCh := make(chan error, httpWorkers)
	var wg sync.WaitGroup

	ticker := time.NewTicker(httpProgressTick)
	defer ticker.Stop()

	go func() {
		for range ticker.C {
			mu.Lock()
			d := done
			now := time.Now()
			elapsed := now.Sub(lastTime).Seconds()
			var speed int64
			if elapsed > 0 {
				speed = int64(float64(d-lastDone) / elapsed)
			}
			lastDone = d
			lastTime = now
			mu.Unlock()
			if progress != nil {
				progress(d, total, speed)
			}
		}
	}()

	for i := 0; i < httpWorkers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for c := range workCh {
				if ctx.Err() != nil {
					return
				}
				n, err := downloadChunk(ctx, url, destPath, c.start, c.end)
				if err != nil {
					errCh <- fmt.Errorf("chunk %d: %w", c.index, err)
					return
				}
				mu.Lock()
				done += n
				mu.Unlock()
			}
		}()
	}

	wg.Wait()
	close(errCh)

	if err := ctx.Err(); err != nil {
		return "", err
	}
	for err := range errCh {
		if err != nil {
			return "", err
		}
	}

	if progress != nil {
		progress(total, total, 0)
	}
	return destPath, nil
}

func downloadChunk(ctx context.Context, url, destPath string, start, end int64) (int64, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", start, end))

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()

	f, err := os.OpenFile(destPath, os.O_WRONLY, 0o644)
	if err != nil {
		return 0, err
	}
	defer f.Close()

	if _, err := f.Seek(start, io.SeekStart); err != nil {
		return 0, err
	}

	n, err := io.Copy(f, resp.Body)
	return n, err
}

func filenameFromURL(url string, resp *http.Response) string {
	// Try Content-Disposition header
	if cd := resp.Header.Get("Content-Disposition"); cd != "" {
		if _, params, err := parseContentDisposition(cd); err == nil {
			if name, ok := params["filename"]; ok && name != "" {
				return sanitizeFilename(name)
			}
		}
	}
	// Extract from URL path
	for i := len(url) - 1; i >= 0; i-- {
		if url[i] == '/' {
			name := url[i+1:]
			// Strip query string
			for j := 0; j < len(name); j++ {
				if name[j] == '?' || name[j] == '#' {
					name = name[:j]
					break
				}
			}
			if name != "" {
				return sanitizeFilename(name)
			}
			break
		}
	}
	return "download"
}

func parseContentDisposition(cd string) (string, map[string]string, error) {
	params := make(map[string]string)
	// Very minimal parser
	for i, part := range splitComma(cd) {
		part = trimSpace(part)
		if i == 0 {
			continue
		}
		kv := splitN(part, "=", 2)
		if len(kv) == 2 {
			k := trimSpace(kv[0])
			v := trimSpace(kv[1])
			if len(v) >= 2 && v[0] == '"' && v[len(v)-1] == '"' {
				v = v[1 : len(v)-1]
			}
			params[k] = v
		}
	}
	return "", params, nil
}

func splitComma(s string) []string {
	var res []string
	cur := ""
	for _, r := range s {
		if r == ';' {
			res = append(res, cur)
			cur = ""
		} else {
			cur += string(r)
		}
	}
	res = append(res, cur)
	return res
}

func splitN(s, sep string, n int) []string {
	idx := -1
	for i := 0; i <= len(s)-len(sep); i++ {
		if s[i:i+len(sep)] == sep {
			idx = i
			break
		}
	}
	if idx < 0 || n <= 1 {
		return []string{s}
	}
	return []string{s[:idx], s[idx+len(sep):]}
}

func trimSpace(s string) string {
	start := 0
	for start < len(s) && (s[start] == ' ' || s[start] == '\t') {
		start++
	}
	end := len(s)
	for end > start && (s[end-1] == ' ' || s[end-1] == '\t') {
		end--
	}
	return s[start:end]
}

func sanitizeFilename(name string) string {
	result := make([]byte, 0, len(name))
	for i := 0; i < len(name); i++ {
		c := name[i]
		if c == '/' || c == '\\' || c == ':' || c == '*' || c == '?' || c == '"' || c == '<' || c == '>' || c == '|' {
			result = append(result, '_')
		} else {
			result = append(result, c)
		}
	}
	if len(result) == 0 {
		return "download"
	}
	// avoid length > 255
	if len(result) > 200 {
		ext := ""
		for i := len(result) - 1; i >= 0 && i >= len(result)-10; i-- {
			if result[i] == '.' {
				ext = string(result[i:])
				break
			}
		}
		result = append(result[:200], []byte(ext)...)
	}
	quoted := strconv.Quote(string(result))
	return quoted[1 : len(quoted)-1]
}
