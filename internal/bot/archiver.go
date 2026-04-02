package bot

import (
	"archive/zip"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// zipDir creates a zip archive at destPath containing all files under srcDir.
// Directory entries are not stored; only files are included with their relative paths.
func zipDir(srcDir, destPath string) error {
	f, err := os.Create(destPath)
	if err != nil {
		return fmt.Errorf("create zip: %w", err)
	}
	defer f.Close()

	w := zip.NewWriter(f)
	defer w.Close()

	return filepath.Walk(srcDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}

		rel, err := filepath.Rel(srcDir, path)
		if err != nil {
			return fmt.Errorf("rel path for %s: %w", path, err)
		}
		rel = filepath.ToSlash(rel) // use forward slashes inside zip

		fw, err := w.Create(rel)
		if err != nil {
			return fmt.Errorf("zip create entry %s: %w", rel, err)
		}

		src, err := os.Open(path)
		if err != nil {
			return fmt.Errorf("zip open %s: %w", path, err)
		}

		_, copyErr := io.Copy(fw, src)
		closeErr := src.Close()
		if copyErr != nil {
			return fmt.Errorf("zip copy %s: %w", rel, copyErr)
		}
		if closeErr != nil {
			return fmt.Errorf("zip close %s: %w", rel, closeErr)
		}
		return nil
	})
}
