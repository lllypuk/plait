package audit_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lllypuk/plait/internal/audit"
)

const (
	unknownAgent = "unknown"
	claudeEnv    = "CLAUDECODE"
)

func readLines(t *testing.T, dir string) [][]byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, audit.FileName))
	if err != nil {
		t.Fatal(err)
	}
	return bytes.Split(bytes.TrimSuffix(b, []byte("\n")), []byte("\n"))
}

func TestAppend(t *testing.T) {
	dir := t.TempDir()
	ts := time.Date(2026, 11, 5, 10, 0, 0, 0, time.UTC)
	for _, e := range []audit.Entry{
		{TS: ts, Version: "v1", Cmd: "ctx save", Run: "r", StepFrom: "new", StepTo: "task_saved", Agent: "claude"},
		{TS: ts, Version: "v1", Cmd: "review import", Run: "r", StepFrom: "exported", StepTo: "exported",
			Agent: unknownAgent, Error: "answer_not_found"},
	} {
		if err := audit.Append(dir, e); err != nil {
			t.Fatal(err)
		}
	}
	lines := readLines(t, dir)
	want := []string{
		`{"ts":"2026-11-05T10:00:00Z","plait_version":"v1","cmd":"ctx save","run":"r","step_from":"new",` +
			`"step_to":"task_saved","agent":"claude"}`,
		`{"ts":"2026-11-05T10:00:00Z","plait_version":"v1","cmd":"review import","run":"r","step_from":"exported",` +
			`"step_to":"exported","agent":"unknown","error":"answer_not_found"}`,
	}
	if len(lines) != len(want) {
		t.Fatalf("lines = %q", lines)
	}
	for i := range want {
		if string(lines[i]) != want[i] {
			t.Errorf("line %d = %s\nwant %s", i, lines[i], want[i])
		}
	}
}

func TestAppendTruncates(t *testing.T) {
	dir := t.TempDir()
	e := audit.Entry{Cmd: "review export", Run: "r"}
	for i := range 2000 {
		path := fmt.Sprintf("src/%s/file-%d.go", strings.Repeat("d", 20), i)
		e.Context = append(e.Context, audit.File{Path: path, SHA256: audit.SHA256([]byte(path))})
	}
	e.Excluded = []audit.Excluded{{Path: ".env", Reason: audit.ReasonDenyGlob}}
	if err := audit.Append(dir, e); err != nil {
		t.Fatal(err)
	}
	lines := readLines(t, dir)
	if len(lines) != 1 || len(lines[0]) >= audit.MaxLine {
		t.Fatalf("got %d lines, first %d bytes", len(lines), len(lines[0]))
	}
	var got audit.Entry
	if err := json.Unmarshal(lines[0], &got); err != nil {
		t.Fatal(err)
	}
	if !got.Truncated || len(got.Context) == 0 || len(got.Context) >= len(e.Context) || len(got.Excluded) != 1 {
		t.Errorf("truncated = %v, context %d of %d, excluded %d",
			got.Truncated, len(got.Context), len(e.Context), len(got.Excluded))
	}
}

func TestAgent(t *testing.T) {
	for _, tt := range []struct {
		name string
		env  map[string]string
		want string
	}{
		{"explicit", map[string]string{"PLAIT_AGENT": "codex", claudeEnv: "1"}, "codex"},
		{"claude code", map[string]string{claudeEnv: "1"}, "claude"},
		{"claude code off", map[string]string{claudeEnv: "0"}, unknownAgent},
		{"nothing", nil, unknownAgent},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := audit.Agent(func(k string) string { return tt.env[k] }); got != tt.want {
				t.Errorf("Agent = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestAppendTruncatesExcluded(t *testing.T) {
	dir := t.TempDir()
	e := audit.Entry{Cmd: "review export", Run: "r", Context: []audit.File{{Path: "task.md"}}}
	for i := range 3000 {
		e.Excluded = append(e.Excluded, audit.Excluded{
			Path: fmt.Sprintf("secrets/%s/key-%d.pem", strings.Repeat("s", 20), i), Reason: audit.ReasonDenyGlob,
		})
	}
	if err := audit.Append(dir, e); err != nil {
		t.Fatal(err)
	}
	var got audit.Entry
	if err := json.Unmarshal(readLines(t, dir)[0], &got); err != nil {
		t.Fatal(err)
	}
	if !got.Truncated || len(got.Context) != 1 || len(got.Excluded) == 0 || len(got.Excluded) >= len(e.Excluded) {
		t.Errorf("truncated = %v, context %d, excluded %d of %d",
			got.Truncated, len(got.Context), len(got.Excluded), len(e.Excluded))
	}
}

func TestAppendErrors(t *testing.T) {
	dir := t.TempDir()
	if err := audit.Append(
		dir,
		audit.Entry{Cmd: "mr describe", Error: strings.Repeat("x", audit.MaxLine)},
	); err == nil {
		t.Error("oversized entry without lists: no error")
	}
	if _, err := os.Stat(filepath.Join(dir, audit.FileName)); !os.IsNotExist(err) {
		t.Errorf("oversized entry left a log: %v", err)
	}
	if err := audit.Append(filepath.Join(dir, "missing"), audit.Entry{Cmd: "ctx save"}); err == nil {
		t.Error("missing run dir: no error")
	}
}
