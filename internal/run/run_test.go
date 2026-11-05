package run_test

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/lllypuk/plait/internal/output"
	"github.com/lllypuk/plait/internal/run"
)

const codeInvalidSlug = "invalid_slug"

// newStore pins the clock to 2026-11-05 local time.
func newStore(t *testing.T) *run.Store {
	t.Helper()
	s := run.Open(t.TempDir())
	s.Now = func() time.Time { return time.Date(2026, 11, 5, 10, 0, 0, 0, time.Local) }
	return s
}

func wantInvalid(t *testing.T, err error, code string) {
	t.Helper()
	e, ok := errors.AsType[*output.Error](err)
	if !ok || e.Kind != output.KindInvalid || e.Code != code {
		t.Fatalf("err = %#v, want Invalid %q", err, code)
	}
	if e.Fix == "" {
		t.Errorf("error %q has no fix", code)
	}
}

func TestNew(t *testing.T) {
	s := newStore(t)
	st, created, err := s.New("order-retry", "")
	if err != nil || !created {
		t.Fatalf("New: created = %v, err = %v", created, err)
	}
	if st.ID != "2026-11-05-order-retry" || st.Step != run.StepNew || st.Schema != run.StateSchema {
		t.Errorf("state = %+v", st)
	}

	b, err := os.ReadFile(filepath.Join(s.RunDir(st.ID), "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	var onDisk map[string]any
	if err = json.Unmarshal(b, &onDisk); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"schema", "id", "created_at", "snapshots", "reviews", "step"} {
		if _, ok := onDisk[k]; !ok {
			t.Errorf("state.json lacks %q: %s", k, b)
		}
	}

	cur, err := s.Current()
	if err != nil || cur.ID != st.ID {
		t.Errorf("Current = %v, %v", cur, err)
	}
}

func TestNewSameDayIsIdempotent(t *testing.T) {
	s := newStore(t)
	first, _, err := s.New("order-retry", "")
	if err != nil {
		t.Fatal(err)
	}
	first.Step = run.StepTaskSaved
	if err = s.Save(first); err != nil {
		t.Fatal(err)
	}
	if _, _, err = s.New("other", ""); err != nil {
		t.Fatal(err)
	}

	again, created, err := s.New("order-retry", "")
	if err != nil || created {
		t.Fatalf("second New: created = %v, err = %v", created, err)
	}
	if again.ID != first.ID || again.Step != run.StepTaskSaved {
		t.Errorf("second New = %+v, want existing run", again)
	}
	if cur, _ := s.Current(); cur == nil || cur.ID != first.ID {
		t.Errorf("current = %v, want %s", cur, first.ID)
	}

	s.Now = func() time.Time { return time.Date(2026, 11, 6, 9, 0, 0, 0, time.Local) }
	next, created, err := s.New("order-retry", "")
	if err != nil || !created || next.ID != "2026-11-06-order-retry" {
		t.Errorf("next day New = %v, created %v, err %v", next, created, err)
	}
}

func TestNewWithKey(t *testing.T) {
	s := newStore(t)
	st, _, err := s.New("order-retry", "PROJ-123")
	if err != nil || st.ID != "PROJ-123-order-retry" {
		t.Fatalf("New = %v, %v", st, err)
	}
}

func TestNewRejectsBadInput(t *testing.T) {
	tests := []struct {
		name, slug, key, code string
	}{
		{name: "upper slug", slug: "Order", code: codeInvalidSlug},
		{name: "path slug", slug: "../x", code: codeInvalidSlug},
		{name: "empty slug", slug: "", code: codeInvalidSlug},
		{name: "double hyphen", slug: "a--b", code: codeInvalidSlug},
		{name: "bad key", slug: "ok", key: "proj-1", code: "invalid_key"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, err := newStore(t).New(tt.slug, tt.key)
			wantInvalid(t, err, tt.code)
		})
	}
}

func TestResumeAndList(t *testing.T) {
	s := newStore(t)
	for _, slug := range []string{"beta", "alpha"} {
		if _, _, err := s.New(slug, ""); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.Resume("2026-11-05-beta"); err != nil {
		t.Fatal(err)
	}
	runs, err := s.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 2 || runs[0].ID != "2026-11-05-alpha" || runs[0].Current || !runs[1].Current {
		t.Errorf("List = %+v", runs)
	}
}

func TestListEmpty(t *testing.T) {
	runs, err := newStore(t).List()
	if err != nil || runs == nil || len(runs) != 0 {
		t.Errorf("List = %#v, %v; want empty non-nil", runs, err)
	}
}

func TestMissingRun(t *testing.T) {
	s := newStore(t)
	_, err := s.Resume("2026-01-01-nope")
	wantInvalid(t, err, "run_not_found")
	_, err = s.Resume("../../etc")
	wantInvalid(t, err, "run_not_found")
	_, err = s.Current()
	wantInvalid(t, err, "no_current_run")

	if _, _, err = s.New("gone", ""); err != nil {
		t.Fatal(err)
	}
	if err = os.RemoveAll(s.RunDir("2026-11-05-gone")); err != nil {
		t.Fatal(err)
	}
	_, err = s.Current()
	wantInvalid(t, err, "run_not_found")
}

func TestCorruptState(t *testing.T) {
	s := newStore(t)
	st, _, err := s.New("x", "")
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(s.RunDir(st.ID), "state.json"), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err = s.Current()
	if e, ok := errors.AsType[*output.Error](err); !ok || e.Kind != output.KindNeedsHuman {
		t.Errorf("err = %v, want NeedsHuman", err)
	}
}

func TestLeftoverTempFile(t *testing.T) {
	s := newStore(t)
	st, _, err := s.New("x", "")
	if err != nil {
		t.Fatal(err)
	}
	dir := s.RunDir(st.ID)
	leftover := filepath.Join(dir, ".state.json.tmp-123")
	if err = os.WriteFile(leftover, []byte(`{"id":"broken`), 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := s.Load(st.ID)
	if err != nil || got.ID != st.ID {
		t.Fatalf("Load with leftover temp = %v, %v", got, err)
	}
	if runs, listErr := s.List(); listErr != nil || len(runs) != 1 {
		t.Fatalf("List with leftover temp = %v, %v", runs, err)
	}

	if err = s.Save(got); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(leftover); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("leftover temp file survived Save: %v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Errorf("run dir has %d entries, want only state.json", len(entries))
	}
}

func TestWriteFileReplaces(t *testing.T) {
	path := filepath.Join(t.TempDir(), "f")
	for _, content := range []string{"one", "two"} {
		if err := run.WriteFile(path, []byte(content)); err != nil {
			t.Fatal(err)
		}
		b, err := os.ReadFile(path)
		if err != nil || string(b) != content {
			t.Errorf("content = %q, %v; want %q", b, err, content)
		}
	}
}
