package run

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/lllypuk/plait/internal/output"
)

const lockName = "lock"

// Lock waits for the store-wide lock; a command that reads, checks and writes a run holds it until unlock.
func (s *Store) Lock() (func(), error) {
	if err := os.MkdirAll(s.Dir, dirPerm); err != nil {
		return nil, output.Internal(fmt.Errorf("create %s: %w", s.Dir, err))
	}
	path := filepath.Join(s.Dir, lockName)
	f, err := os.OpenFile(filepath.Clean(path), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, output.Internal(fmt.Errorf("open %s: %w", path, err))
	}
	if err = lockFile(f); err != nil {
		return nil, output.Internal(errors.Join(fmt.Errorf("lock %s: %w", path, err), f.Close()))
	}
	return func() { _ = errors.Join(unlockFile(f), f.Close()) }, nil
}
