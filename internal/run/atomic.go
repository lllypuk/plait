package run

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// staleAge keeps a temporary file of a concurrent writer from being taken for a crash leftover.
const staleAge = time.Minute

// WriteFile replaces path atomically via a temporary file and rename; it first removes temporaries left by a crash.
func WriteFile(path string, data []byte) error {
	dir, base := filepath.Split(path)
	if dir == "" {
		dir = "."
	}
	prefix := "." + base + ".tmp-"
	if err := removeStale(dir, prefix); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, prefix+"*")
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

// removeStale lists dir instead of globbing, so glob metacharacters in the path do not break the match.
func removeStale(dir, prefix string) error {
	entries, err := os.ReadDir(dir)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("find stale temp files: %w", err)
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasPrefix(e.Name(), prefix) {
			continue
		}
		info, infoErr := e.Info()
		if infoErr != nil || time.Since(info.ModTime()) < staleAge {
			continue
		}
		if rmErr := os.Remove(filepath.Join(dir, e.Name())); rmErr != nil && !errors.Is(rmErr, fs.ErrNotExist) {
			return fmt.Errorf("remove stale temp file: %w", rmErr)
		}
	}
	return nil
}

// WriteIfChanged replaces path via WriteFile only when its content differs and reports whether it wrote.
func WriteIfChanged(path string, data []byte) (bool, error) {
	old, err := os.ReadFile(filepath.Clean(path))
	if err == nil && bytes.Equal(old, data) {
		return false, nil
	}
	if err = WriteFile(path, data); err != nil {
		return false, err
	}
	return true, nil
}
