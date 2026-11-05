package check_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/lllypuk/plait/internal/check"
	"github.com/lllypuk/plait/internal/output"
	"github.com/lllypuk/plait/internal/run"
	"github.com/lllypuk/plait/internal/vcs"
)

func git(t *testing.T, dir string, args ...string) {
	t.Helper()
	argv := append([]string{"-C", dir}, args...)
	cmd := exec.CommandContext(t.Context(), "git", argv...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func write(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

// setup returns a repo with one commit and a current run in it.
func setup(t *testing.T) (*run.Store, vcs.Repo) {
	t.Helper()
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	dir := t.TempDir()
	git(t, dir, "init", "-q", "-b", "main")
	git(t, dir, "config", "user.email", "dev@example.com")
	git(t, dir, "config", "user.name", "dev")
	write(t, dir, "a.txt", "a\n")
	git(t, dir, "add", "-A")
	git(t, dir, "commit", "-q", "-m", "init")
	s := run.Open(dir)
	s.Now = func() time.Time { return time.Date(2026, 11, 5, 10, 0, 0, 0, time.UTC) }
	if _, _, err := s.New("order-retry", ""); err != nil {
		t.Fatal(err)
	}
	return s, vcs.Repo{Root: dir}
}

func readRecord(t *testing.T, path string) check.Record {
	t.Helper()
	b, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		t.Fatal(err)
	}
	var r check.Record
	if err = json.Unmarshal(b, &r); err != nil {
		t.Fatal(err)
	}
	return r
}

const testCheck = "test"

func TestSave(t *testing.T) {
	s, repo := setup(t)
	write(t, repo.Root, "b.txt", "b\n")
	tree, err := repo.CurrentTree(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	in := check.Input{Name: testCheck, ExitCode: 1, Output: []byte("ok  pkg/a\nFAIL pkg/b\n")}
	res, err := check.Save(t.Context(), s, repo, in)
	if err != nil {
		t.Fatal(err)
	}
	st, err := s.Current()
	if err != nil {
		t.Fatal(err)
	}
	wantPath := filepath.Join(s.RunDir(st.ID), "checks", testCheck+".json")
	if !res.Changed || res.Path != wantPath || res.Tree != tree || res.Step != run.StepNew || res.Run != st.ID {
		t.Fatalf("result = %+v", res)
	}
	r := readRecord(t, wantPath)
	if r.Name != testCheck || r.ExitCode != 1 || r.Tree != tree || r.OutputTail != string(in.Output) ||
		r.OutputSHA256 == "" || r.SavedAt.IsZero() {
		t.Errorf("record = %+v", r)
	}

	before, _ := os.ReadFile(filepath.Clean(wantPath))
	res, err = check.Save(t.Context(), s, repo, in)
	if err != nil || res.Changed {
		t.Fatalf("repeat: result = %+v, err = %v", res, err)
	}
	if after, _ := os.ReadFile(filepath.Clean(wantPath)); string(after) != string(before) {
		t.Error("repeat rewrote the record")
	}

	for name, next := range map[string]check.Input{
		"exit code": {Name: testCheck, ExitCode: 0, Output: in.Output},
		"output":    {Name: testCheck, ExitCode: 0, Output: []byte("ok\n")},
	} {
		res, err = check.Save(t.Context(), s, repo, next)
		if err != nil || !res.Changed {
			t.Errorf("%s changed: result = %+v, err = %v", name, res, err)
		}
	}
	write(t, repo.Root, "b.txt", "b2\n")
	res, err = check.Save(t.Context(), s, repo, check.Input{Name: testCheck, Output: []byte("ok\n")})
	if err != nil || !res.Changed || res.Tree == tree {
		t.Errorf("tree changed: result = %+v, err = %v", res, err)
	}
}

func TestSaveKeepsStep(t *testing.T) {
	s, repo := setup(t)
	st, err := s.Current()
	if err != nil {
		t.Fatal(err)
	}
	st.Step = run.StepReported
	if err = s.Save(st); err != nil {
		t.Fatal(err)
	}
	res, err := check.Save(t.Context(), s, repo, check.Input{Name: "lint"})
	if err != nil || res.Step != run.StepReported {
		t.Fatalf("result = %+v, err = %v", res, err)
	}
	if cur, _ := s.Current(); cur.Step != run.StepReported {
		t.Errorf("step = %s", cur.Step)
	}
}

func TestTail(t *testing.T) {
	var many strings.Builder
	for i := range 250 {
		fmt.Fprintf(&many, "line %d\n", i)
	}
	long := strings.Repeat("x", 100) + "\n"
	wide := strings.Repeat(long, 300)
	tests := []struct {
		name  string
		in    string
		check func(t *testing.T, got string)
	}{
		{"short", "a\nb", func(t *testing.T, got string) {
			if got != "a\nb" {
				t.Errorf("tail = %q", got)
			}
		}},
		{"200 lines", many.String(), func(t *testing.T, got string) {
			if !strings.HasPrefix(got, "line 50\n") || !strings.HasSuffix(got, "line 249\n") ||
				strings.Count(got, "\n") != 200 {
				t.Errorf("tail starts %q, %d lines", got[:10], strings.Count(got, "\n"))
			}
		}},
		{"16 KiB", wide, func(t *testing.T, got string) {
			if len(got) > check.MaxTailBytes || !strings.HasPrefix(got, long) || !strings.HasSuffix(got, long) {
				t.Errorf("tail is %d bytes, starts %q", len(got), got[:10])
			}
		}},
		{"one huge line", strings.Repeat("я", 20000), func(t *testing.T, got string) {
			if len(got) > check.MaxTailBytes || len(got) < check.MaxTailBytes-1 || !utf8.ValidString(got) {
				t.Errorf("tail is %d bytes, valid utf8 %v", len(got), utf8.ValidString(got))
			}
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.check(t, check.Tail([]byte(tt.in)))
		})
	}
}

func TestRedact(t *testing.T) {
	tests := []struct{ in, want string }{
		{"GITHUB_TOKEN=ghp_abc123", "GITHUB_TOKEN=[redacted]"},
		{"export DB_PASSWORD = hunter2 # prod", "export DB_PASSWORD = [redacted]"},
		{"client_secret: s3cr3t", "client_secret: [redacted]"},
		{`  "api_key": "k-1",`, `  "api_key": [redacted]`},
		{"ApiKey=1\nPRIVATE_KEY: ---BEGIN", "ApiKey=[redacted]\nPRIVATE_KEY: [redacted]"},
		{"passwd:\nnext line", "passwd:\nnext line"},
		{"token expired", "token expired"},
		{"ok  github.com/x/auth 0.1s", "ok  github.com/x/auth 0.1s"},
	}
	for _, tt := range tests {
		if got := check.Redact(tt.in); got != tt.want {
			t.Errorf("Redact(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestSaveRedactsTailButHashesRaw(t *testing.T) {
	s, repo := setup(t)
	raw := []byte("token=abc\n")
	res, err := check.Save(t.Context(), s, repo, check.Input{Name: testCheck, Output: raw})
	if err != nil {
		t.Fatal(err)
	}
	r := readRecord(t, res.Path)
	if r.OutputTail != "token=[redacted]\n" {
		t.Errorf("tail = %q", r.OutputTail)
	}
	b, _ := os.ReadFile(filepath.Clean(res.Path))
	if strings.Contains(string(b), "abc") {
		t.Errorf("record leaks the secret: %s", b)
	}
}

func TestSaveInvalid(t *testing.T) {
	s, repo := setup(t)
	for _, name := range []string{"", "Test", "-x", "a/b", "../x", "a b"} {
		_, err := check.Save(t.Context(), s, repo, check.Input{Name: name})
		wantInvalid(t, err, "invalid_check_name")
	}
	_, err := check.Save(t.Context(), run.Open(t.TempDir()), repo, check.Input{Name: testCheck})
	wantInvalid(t, err, "no_current_run")
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
