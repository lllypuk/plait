package review_test

import (
	"errors"
	"flag"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lllypuk/plait/internal/config"
	"github.com/lllypuk/plait/internal/output"
	"github.com/lllypuk/plait/internal/review"
	"github.com/lllypuk/plait/internal/run"
	"github.com/lllypuk/plait/internal/task"
	"github.com/lllypuk/plait/internal/vcs"
)

var update = flag.Bool( //nolint:gochecknoglobals // go test -update flag
	"update", false, "rewrite golden files")

const (
	commitID = "0123456789abcdef0123456789abcdef01234567"
	baseID   = "89abcdef0123456789abcdef0123456789abcdef"
	agentsMD = "AGENTS.md"
	claudeMD = "CLAUDE.md"
	plaitMD  = ".plait/rules.md"
	diffText = "diff --git a/a.txt b/a.txt\n--- a/a.txt\n+++ b/a.txt\n@@ -1 +1 @@\n-a\n+a changed\n"
)

func TestRenderGolden(t *testing.T) {
	cases := []struct {
		name string
		p    review.Package
	}{
		{
			"criteria",
			review.Package{
				Run:      "PROJ-7-order-retry",
				N:        2,
				Commit:   commitID,
				Base:     baseID,
				Coverage: review.CoverageCriteria,
				Task:     "# PROJ-7: Retry failed orders\n<!-- plait:source jira -->\n\n## Критерии приёмки\n\n- retried 3 times\n",
				Rules: []review.Rule{
					{Path: agentsMD, Content: "Run make check.\n\n```sh\nmake check\n```\n"},
					{Path: plaitMD, Content: "No panics.\n"},
				},
				Diff: diffText,
			},
		},
		{
			"diff_only",
			review.Package{
				Run:      "2026-11-05-order-retry",
				N:        1,
				Commit:   commitID,
				Base:     baseID,
				Coverage: review.CoverageDiffOnly,
				Task:     "# Retry failed orders\n<!-- plait:source local -->\n\n## Критерии приёмки\n\n<!-- plait:gap acceptance_criteria -->\nНе заданы.\n",
				Rules:    []review.Rule{},
				Diff:     diffText,
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := review.Render(tc.p)
			if err != nil {
				t.Fatal(err)
			}
			golden := filepath.Join("testdata", "package-"+tc.name+".golden")
			if *update {
				if err = os.WriteFile(golden, got, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			want, err := os.ReadFile(golden)
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != string(want) {
				t.Errorf("package.md differs from %s (rerun with -update):\n%s", golden, got)
			}
		})
	}
}

func TestLoadRules(t *testing.T) {
	root := t.TempDir()
	write(t, root, agentsMD, "agents\n")
	write(t, root, plaitMD, "plait rules\n")
	if err := os.Mkdir(filepath.Join(root, "docs"), 0o750); err != nil {
		t.Fatal(err)
	}

	rules, err := review.LoadRules(root, []string{agentsMD, claudeMD, plaitMD, "./AGENTS.md"})
	if err != nil {
		t.Fatal(err)
	}
	if len(rules) != 2 || rules[0] != (review.Rule{Path: agentsMD, Content: "agents\n"}) ||
		rules[1] != (review.Rule{Path: plaitMD, Content: "plait rules\n"}) {
		t.Errorf("rules = %+v", rules)
	}

	rules, err = review.LoadRules(root, []string{claudeMD})
	if err != nil || len(rules) != 0 {
		t.Errorf("no rule files: rules = %+v, err = %v", rules, err)
	}

	for _, p := range []string{"../outside.md", "/etc/passwd", ""} {
		_, err = review.LoadRules(root, []string{p})
		wantInvalid(t, err, "rule_outside_repo")
	}
	_, err = review.LoadRules(root, []string{"docs"})
	wantInvalid(t, err, "rule_unreadable")
}

func TestExport(t *testing.T) {
	dir := newRepo(t)
	write(t, dir, agentsMD, "agents rules\n")
	git(t, dir, "add", agentsMD)
	git(t, dir, "commit", "-q", "-m", "rules")
	git(t, dir, "checkout", "-q", "-b", "feature")
	write(t, dir, "a.txt", "a changed\n")

	s := run.Open(dir)
	s.Now = func() time.Time { return time.Date(2026, 11, 5, 10, 0, 0, 0, time.UTC) }
	repo := vcs.Repo{Root: dir}
	cfg := config.Review{Rules: []string{agentsMD, claudeMD}}

	if _, _, err := s.New("order-retry", ""); err != nil {
		t.Fatal(err)
	}
	_, err := review.Export(t.Context(), s, repo, cfg)
	wantInvalid(t, err, "no_task")

	if _, err = task.Save(s, task.Input{Text: []byte("# Retry\n\n## Acceptance criteria\n\n- 3 times\n"),
		Source: "local"}); err != nil {
		t.Fatal(err)
	}
	first, err := review.Export(t.Context(), s, repo, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if first.Snapshot != 1 || !first.Changed || first.Step != run.StepExported ||
		first.Coverage != review.CoverageCriteria ||
		first.Package != ".plait/runs/2026-11-05-order-retry/review-1/package.md" {
		t.Errorf("first export = %+v", first)
	}
	if got := git(t, dir, "rev-parse", vcs.SnapshotRef(first.Run, 1)); got != first.Commit {
		t.Errorf("snapshot ref = %s, want %s", got, first.Commit)
	}
	pkg := read(t, filepath.Join(dir, first.Package))
	checkPackage(t, pkg, first.Commit)

	again, err := review.Export(t.Context(), s, repo, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if again.Snapshot != 1 || again.Changed || again.Commit != first.Commit {
		t.Errorf("repeat export = %+v, want snapshot 1 unchanged", again)
	}
	if read(t, filepath.Join(dir, again.Package)) != pkg {
		t.Error("repeat export rewrote the package")
	}

	write(t, dir, "a.txt", "a changed twice\n")
	second, err := review.Export(t.Context(), s, repo, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if second.Snapshot != 2 || !second.Changed || second.Commit == first.Commit {
		t.Errorf("export after change = %+v, want new snapshot 2", second)
	}
	if read(t, filepath.Join(dir, first.Package)) != pkg {
		t.Error("export after change rewrote the first package")
	}
	st, err := s.Current()
	if err != nil {
		t.Fatal(err)
	}
	if len(st.Snapshots) != 2 || st.Snapshots[1].Commit != second.Commit || st.Step != run.StepExported {
		t.Errorf("state = %+v", st)
	}
}

func TestExportNewTaskSameTree(t *testing.T) {
	dir := newRepo(t)
	git(t, dir, "checkout", "-q", "-b", "feature")
	write(t, dir, "a.txt", "a changed\n")
	s := run.Open(dir)
	repo := vcs.Repo{Root: dir}
	if _, _, err := s.New("order-retry", ""); err != nil {
		t.Fatal(err)
	}
	save := func(text string) {
		t.Helper()
		if _, err := task.Save(s, task.Input{Text: []byte(text), Source: "local"}); err != nil {
			t.Fatal(err)
		}
	}
	save("# Retry\n")
	first, err := review.Export(t.Context(), s, repo, config.Review{})
	if err != nil {
		t.Fatal(err)
	}
	if first.Coverage != review.CoverageDiffOnly {
		t.Errorf("coverage = %s, want diff_only", first.Coverage)
	}
	save("# Retry\n\n## Acceptance criteria\n\n- 3 times\n")
	second, err := review.Export(t.Context(), s, repo, config.Review{})
	if err != nil {
		t.Fatal(err)
	}
	if second.Snapshot != 2 || second.Commit != first.Commit || second.Coverage != review.CoverageCriteria {
		t.Errorf("export after a new task = %+v, want snapshot 2 of the same commit with criteria", second)
	}
}

func checkPackage(t *testing.T, pkg, commit string) {
	t.Helper()
	for _, want := range []string{commit, "+a changed", "### AGENTS.md", "agents rules", "- 3 times"} {
		if !strings.Contains(pkg, want) {
			t.Errorf("package lacks %q", want)
		}
	}
	if strings.Contains(pkg, "### CLAUDE.md") {
		t.Error("package quotes a missing rule file")
	}
}

func wantInvalid(t *testing.T, err error, code string) {
	t.Helper()
	e, ok := errors.AsType[*output.Error](err)
	if !ok || e.Kind != output.KindInvalid || e.Code != code || e.Fix == "" {
		t.Fatalf("err = %#v, want Invalid %q with fix", err, code)
	}
}

func newRepo(t *testing.T) string {
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
	return dir
}

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	argv := append([]string{"-C", dir}, args...)
	cmd := exec.CommandContext(t.Context(), "git", argv...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func write(t *testing.T, dir, name, content string) {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
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
