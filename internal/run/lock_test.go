package run_test

import (
	"testing"
	"time"

	"github.com/lllypuk/plait/internal/run"
)

func TestHold(t *testing.T) {
	dir := t.TempDir()
	s, outer, err := run.Open(dir).Hold()
	if err != nil {
		t.Fatal(err)
	}
	inner, err := s.Lock()
	if err != nil {
		t.Fatal(err)
	}
	inner()

	got := make(chan struct{})
	go func() {
		unlock, lockErr := run.Open(dir).Lock()
		if lockErr == nil {
			unlock()
		}
		close(got)
	}()
	select {
	case <-got:
		t.Fatal("another store took the lock while the outer holder still had it")
	case <-time.After(100 * time.Millisecond):
	}
	outer()
	select {
	case <-got:
	case <-time.After(5 * time.Second):
		t.Fatal("the lock was not released by the outer unlock")
	}
}
