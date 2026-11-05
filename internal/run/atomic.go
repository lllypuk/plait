package run

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// WriteFile replaces path atomically via a temporary file and rename; it first removes temporaries left by a crash.
func WriteFile(path string, data []byte) error {
	dir, base := filepath.Split(path)
	if dir == "" {
		dir = "."
	}
	pattern := "." + base + ".tmp-*"
	if err := removeStale(filepath.Join(dir, pattern)); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, pattern)
	if err != nil {
		return fmt.Errorf("create temp file for %s: %w", path, err)
	}
	tmp := f.Name()
	if err = writeAndClose(f, data); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("write %s: %w", path, err)
	}
	if err = os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("replace %s: %w", path, err)
	}
	return nil
}

func writeAndClose(f *os.File, data []byte) error {
	_, werr := f.Write(data)
	if werr == nil {
		werr = f.Sync()
	}
	return errors.Join(werr, f.Close())
}

func removeStale(glob string) error {
	stale, err := filepath.Glob(glob)
	if err != nil {
		return fmt.Errorf("find stale temp files: %w", err)
	}
	for _, p := range stale {
		if rmErr := os.Remove(p); rmErr != nil && !errors.Is(rmErr, fs.ErrNotExist) {
			return fmt.Errorf("remove stale temp file: %w", rmErr)
		}
	}
	return nil
}
