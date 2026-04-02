package splitter

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// MaxPartSize is 1.95 GiB (1950 * 1024 * 1024 bytes).
const MaxPartSize int64 = 1950 * 1024 * 1024

// Split splits a file into parts no larger than MaxPartSize.
// If the file is smaller, it returns a slice with just the original path.
// The caller is responsible for removing part files after use.
func Split(filePath string) ([]string, error) {
	info, err := os.Stat(filePath)
	if err != nil {
		return nil, fmt.Errorf("stat: %w", err)
	}

	if info.Size() <= MaxPartSize {
		return []string{filePath}, nil
	}

	f, err := os.Open(filePath)
	if err != nil {
		return nil, fmt.Errorf("open: %w", err)
	}
	defer f.Close()

	dir := filepath.Dir(filePath)
	base := filepath.Base(filePath)

	var parts []string
	buf := make([]byte, 4*1024*1024) // 4 MB copy buffer
	partIdx := 1

	for {
		partPath := filepath.Join(dir, fmt.Sprintf("%s.part%03d", base, partIdx))
		written, err := writePartFile(f, partPath, MaxPartSize, buf)
		if err != nil {
			// Clean up on error
			for _, p := range parts {
				os.Remove(p)
			}
			os.Remove(partPath)
			return nil, fmt.Errorf("write part %d: %w", partIdx, err)
		}
		if written == 0 {
			break
		}
		parts = append(parts, partPath)
		partIdx++

		if written < MaxPartSize {
			break
		}
	}

	return parts, nil
}

// writePartFile writes up to maxBytes from src into a new file at path.
// Returns the number of bytes written.
func writePartFile(src io.Reader, path string, maxBytes int64, buf []byte) (int64, error) {
	dst, err := os.Create(path)
	if err != nil {
		return 0, err
	}
	defer dst.Close()

	lr := io.LimitReader(src, maxBytes)
	n, err := io.CopyBuffer(dst, lr, buf)
	if err != nil {
		return n, err
	}
	return n, nil
}
