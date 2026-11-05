package mr_test

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lllypuk/plait/internal/config"
	"github.com/lllypuk/plait/internal/mr"
	"github.com/lllypuk/plait/internal/output"
	"github.com/lllypuk/plait/internal/review"
	"github.com/lllypuk/plait/internal/run"
	"github.com/lllypuk/plait/internal/task"
	"github.com/lllypuk/plait/internal/vcs"
)

var update = flag.Bool( //nolint:gochecknoglobals // go test -update flag
	"update", false, "rewrite golden files")

const (
	commitID  = "0123456789abcdef0123456789abcdef01234567"
	baseID    = "89abcdef0123456789abcdef0123456789abcdef"
	version   = "v0.1.0"
	key       = "PROJ-7"
	tmplBad   = "mr_template_invalid"
	taskInput = "# Retry failed orders\n\nRetry them.\n\n## Acceptance criteria\n\n" +
		"- a failed order is retried 3 times\n- every retry is logged\n"
)

func TestRenderGolden(t *testing.T) {
	findings := review.Compare(1, nil, nil, []review.Finding{
		{ID: "F1", Severity: "blocker", File: "b.go", Line: 10, Title: "no retry", Rationale: "fails at once",
			Criterion: "A failed order is retried\n3 times"},
		{ID: "F2", Severity: "minor", File: "c.go", Line: 2, Title: "log level", Rationale: "debug, not info",
			Criterion: "every retry is logged"},
		{ID: "F3", Severity: "major", File: "d.go", Line: 0, Title: "no tests", Rationale: "untested"},
	})
	fixed := review.Compare(2, []review.Previous{{Tracked: findings[1],
		At: vcs.Location{File: "c.go", Line: 2, State: vcs.LineUnchanged}}},
		[]review.Verdict{{Key: "R1-F2", Verdict: review.VerdictFixed, Note: "info now"}}, nil)
	findings = append(findings[:1:1], findings[2], fixed[0])
	checks := []review.Check{
		{Name: "test", State: review.CheckFresh, ExitCode: 0, Output: "ok"},
		{Name: "lint", State: review.CheckStale, ExitCode: 1},
		{Name: "vet", State: review.CheckMissing},
	}
	gaps := []run.Gap{{Code: review.GapChecksMissing, Items: []string{"vet"}}}
	cases := []struct {
		name     string
		key      string
		in       string
		coverage string
		findings []review.Tracked
		checks   []review.Check
		gaps     []run.Gap
		summary  string
		stale    bool
	}{
		{"key-summary-flagged-checks", key, taskInput, review.CoverageCriteria, findings, checks, gaps,
			"Retries failed orders with a backoff.\n", false},
		{"plain", "", "# Retry failed orders\n", review.CoverageDiffOnly, nil, nil, nil, "", false},
		{"stale", key, taskInput, review.CoverageCriteria, nil, checks[:1], nil, "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			doc, err := task.Normalize(task.Input{Text: []byte(tc.in), Key: tc.key, Source: task.DefaultSource})
			if err != nil {
				t.Fatal(err)
			}
			st := &run.State{ID: "run", Task: &run.Task{Key: tc.key}}
			snap := run.Snapshot{N: 2, Commit: commitID, Base: baseID, CoverageGaps: tc.gaps}
			d := mr.NewData(st, snap, run.Review{N: 2, Coverage: tc.coverage}, tc.findings, doc.Markdown, tc.checks,
				mr.Input{Summary: tc.summary, Version: version}, tc.stale)
			tmpl, err := mr.Template(t.TempDir(), "")
			if err != nil {
				t.Fatal(err)
			}
			body, err := mr.Render(tmpl, d, "")
			if err != nil {
				t.Fatal(err)
			}
			golden(t, "mr-"+tc.name+".md.golden", []byte("# "+d.Title+"\n\n"+body))
		})
	}
}

func TestDescribe(t *testing.T) {
	dir, s, repo := setup(t)
	cfg := config.Config{}
	_, err := mr.Describe(t.Context(), s, repo, cfg, mr.Input{Version: version})
	wantInvalid(t, err, "no_review")

	exp, err := review.Export(t.Context(), s, repo, cfg)
	if err != nil {
		t.Fatal(err)
	}
	_, err = mr.Describe(t.Context(), s, repo, cfg, mr.Input{Version: version})
	wantInvalid(t, err, "no_review")

	ans := fmt.Appendf(nil, `{"snapshot":%q,"coverage":"criteria","findings":[`+
		`{"id":"F1","severity":"major","file":"a.txt","line":1,"title":"t","rationale":"r",`+
		`"criterion":"every retry is logged"}]}`, exp.Commit)
	if _, err = review.Import(t.Context(), s, repo, ans, "-"); err != nil {
		t.Fatal(err)
	}
	res, err := mr.Describe(t.Context(), s, repo, cfg, mr.Input{Version: version})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Changed || res.Stale || res.Step != run.StepDescribed || res.Title != "PROJ-7: Retry failed orders" ||
		res.Path != ".plait/runs/"+exp.Run+"/mr.md" || !strings.Contains(res.Body, "every retry is logged — **flagged**") {
		t.Errorf("result = %+v", res)
	}
	if got := read(t, filepath.Join(dir, res.Path)); got != "# "+res.Title+"\n\n"+res.Body {
		t.Errorf("mr.md = %q", got)
	}
	again, err := mr.Describe(t.Context(), s, repo, cfg, mr.Input{Version: version})
	if err != nil || again.Changed || again.Body != res.Body {
		t.Errorf("repeat = %+v, err = %v, want unchanged", again, err)
	}

	write(t, dir, "a.txt", "a changed after review\n")
	_, err = mr.Describe(t.Context(), s, repo, cfg, mr.Input{Version: version})
	wantInvalid(t, err, "snapshot_stale")
	stale, err := mr.Describe(t.Context(), s, repo, cfg, mr.Input{Version: version, AllowStale: true})
	if err != nil || !stale.Stale || !stale.Changed || !strings.Contains(stale.Body, "**Stale:**") {
		t.Errorf("allow stale = %+v, err = %v", stale, err)
	}

	if _, err = review.Export(t.Context(), s, repo, cfg); err != nil {
		t.Fatal(err)
	}
	_, err = mr.Describe(t.Context(), s, repo, cfg, mr.Input{Version: version, AllowStale: true})
	wantInvalid(t, err, "no_review")
}

func TestDescribeTemplate(t *testing.T) {
	dir, s, repo := setup(t)
	write(t, dir, "own.tmpl", `{{.Task}} [{{join .Labels ","}}]`+"\n")
	write(t, dir, "parse.tmpl", "{{.Task")
	write(t, dir, "exec.tmpl", "{{.Nope}}")
	exp, err := review.Export(t.Context(), s, repo, config.Config{})
	if err != nil {
		t.Fatal(err)
	}
	ans := fmt.Appendf(nil, `{"snapshot":%q,"coverage":"criteria","findings":[]}`, exp.Commit)
	if _, err = review.Import(t.Context(), s, repo, ans, "-"); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ name, path, code string }{
		{"outside", "../own.tmpl", "mr_template_outside_repo"},
		{"absolute", filepath.Join(dir, "own.tmpl"), "mr_template_outside_repo"},
		{"missing", "none.tmpl", tmplBad},
		{"parse", "parse.tmpl", tmplBad},
		{"exec", "exec.tmpl", tmplBad},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := config.Config{MR: config.MR{Template: tc.path}}
			_, descErr := mr.Describe(t.Context(), s, repo, cfg, mr.Input{Version: version})
			wantInvalid(t, descErr, tc.code)
			if e, _ := errors.AsType[*output.Error](descErr); !strings.Contains(e.Message, tc.path) {
				t.Errorf("message %q does not name %s", e.Message, tc.path)
			}
		})
	}
	res, err := mr.Describe(t.Context(), s, repo, config.Config{MR: config.MR{Template: "own.tmpl"}},
		mr.Input{Version: version})
	if err != nil || res.Body != "Retry failed orders [ai-assisted]\n" {
		t.Errorf("own template = %+v, err = %v", res, err)
	}
}

// setup returns a repository on a feature branch with a change and a run whose task has criteria.
func setup(t *testing.T) (string, *run.Store, vcs.Repo) {
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
	git(t, dir, "checkout", "-q", "-b", "feature")
	write(t, dir, "a.txt", "a changed\n")
	s := run.Open(dir)
	if _, _, err := s.New("order-retry", key); err != nil {
		t.Fatal(err)
	}
	in := task.Input{Text: []byte(taskInput), Key: key, Source: task.DefaultSource}
	if _, err := task.Save(s, in); err != nil {
		t.Fatal(err)
	}
	return dir, s, vcs.Repo{Root: dir}
}

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

func read(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
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

func golden(t *testing.T, name string, got []byte) {
	t.Helper()
	path := filepath.Join("testdata", name)
	if *update {
		if err := os.WriteFile(path, got, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("%s differs (rerun with -update):\n%s", path, got)
	}
}
