package review_test

import (
	"errors"
	"flag"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/lllypuk/plait/internal/check"
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
	commitID      = "0123456789abcdef0123456789abcdef01234567"
	baseID        = "89abcdef0123456789abcdef0123456789abcdef"
	agentsMD      = "AGENTS.md"
	claudeMD      = "CLAUDE.md"
	plaitMD       = ".plait/rules.md"
	envFile       = ".env"
	pemGlob       = "**/*.pem"
	nestedDirGlob = "**/secrets/**"
	badGlob       = "[bad"
	docsDir       = "docs"
	bigGo         = "big.go"
	runID         = "2026-11-05-order-retry"
	checkLint     = "lint"
	checkVet      = "vet"
	checkBuild    = "build"
	keyF1         = "R1-F1"
	keyF2         = "R1-F2"
	sevBlocker    = "blocker"
	diffText      = "diff --git a/a.txt b/a.txt\n--- a/a.txt\n+++ b/a.txt\n@@ -1 +1 @@\n-a\n+a changed\n"
)

type renderCase struct {
	name string
	p    review.Package
}

func TestRenderGolden(t *testing.T) {
	for _, tc := range append(renderCases(), previousCase()) {
		renderGolden(t, tc)
	}
}

func renderCases() []renderCase {
	return []renderCase{
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
				Run:      runID,
				N:        1,
				Commit:   commitID,
				Base:     baseID,
				Coverage: review.CoverageDiffOnly,
				Task:     "# Retry failed orders\n<!-- plait:source local -->\n\n## Критерии приёмки\n\n<!-- plait:gap acceptance_criteria -->\nНе заданы.\n",
				Rules:    []review.Rule{},
				Diff:     diffText,
			},
		},
		{
			"scoped",
			review.Package{
				Run:      runID,
				N:        1,
				Commit:   commitID,
				Base:     baseID,
				Coverage: review.CoverageCriteria,
				Task:     "# Retry failed orders\n\n## Acceptance criteria\n\n- retried 3 times\n",
				Rules: []review.Rule{
					{Path: agentsMD, Content: "Run make check.\n"},
					{Path: envFile, Excluded: true},
				},
				Diff:         diffText,
				Excluded:     []string{"config/.env", "certs/server.pem"},
				Dropped:      []string{"testdata/big.json"},
				MaxDiffBytes: 1024,
				Gaps: []run.Gap{
					{Code: review.GapDiffTruncated, Items: []string{"testdata/big.json"}},
					{Code: review.GapFilesExcluded, Items: []string{"config/.env", "certs/server.pem", envFile}},
				},
			},
		},
		{
			"checks",
			review.Package{
				Run:      runID,
				N:        1,
				Commit:   commitID,
				Base:     baseID,
				Coverage: review.CoverageDiffOnly,
				Task:     "# Retry failed orders\n",
				Rules:    []review.Rule{},
				Diff:     diffText,
				Checks: []review.Check{
					{Name: "test", State: review.CheckFresh, Output: "ok  \texample.com/orders\t0.01s\n"},
					{Name: checkLint, State: review.CheckFresh, ExitCode: 1, Output: "a.go:3:1: unused (unused)\n"},
					{Name: checkVet, State: review.CheckStale, ExitCode: 2},
					{Name: checkBuild, State: review.CheckMissing},
				},
				Gaps: []run.Gap{
					{Code: review.GapChecksMissing, Items: []string{checkBuild}},
					{Code: review.GapChecksFailed, Items: []string{checkLint}},
					{Code: review.GapChecksStale, Items: []string{checkVet}},
				},
			},
		},
	}
}

// previousCase is the package of a second review.
func previousCase() renderCase {
	return renderCase{
		"previous",
		review.Package{
			Run:      runID,
			N:        2,
			Commit:   commitID,
			Base:     baseID,
			Coverage: review.CoverageCriteria,
			Task:     "# Retry failed orders\n\n## Acceptance criteria\n\n- retried 3 times\n",
			Rules:    []review.Rule{},
			Diff:     diffText,
			Previous: []review.Previous{
				{Key: keyF1, Severity: "major", Title: "no retry", Rationale: "orders fail once and stop",
					Criterion: "retried 3 times", At: vcs.Location{File: "a.txt", Line: 3, State: vcs.LineUnchanged}},
				{Key: keyF2, Severity: "minor", Title: "magic number", Rationale: "3 is inline",
					At: vcs.Location{File: "a.txt", Line: 1, State: vcs.LineChanged}},
				{Key: "R1-F3", Severity: "nit", Title: "typo", Rationale: "```` in a comment",
					At: vcs.Location{File: "b.txt", State: vcs.LineUnchanged}},
				{Key: "R1-F4", Severity: sevBlocker, Title: "secret", Rationale: "key committed",
					At: vcs.Location{State: vcs.LineGone}},
			},
		},
	}
}

func renderGolden(t *testing.T, tc renderCase) {
	t.Helper()
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

func TestLoadRules(t *testing.T) {
	root := t.TempDir()
	write(t, root, agentsMD, "agents\n")
	write(t, root, plaitMD, "plait rules\n")
	if err := os.Mkdir(filepath.Join(root, docsDir), 0o750); err != nil {
		t.Fatal(err)
	}

	rules, err := review.LoadRules(root, []string{agentsMD, claudeMD, plaitMD, "./AGENTS.md"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(rules) != 2 || rules[0] != (review.Rule{Path: agentsMD, Content: "agents\n"}) ||
		rules[1] != (review.Rule{Path: plaitMD, Content: "plait rules\n"}) {
		t.Errorf("rules = %+v", rules)
	}

	rules, err = review.LoadRules(root, []string{claudeMD}, nil)
	if err != nil || len(rules) != 0 {
		t.Errorf("no rule files: rules = %+v, err = %v", rules, err)
	}

	for _, p := range []string{"../outside.md", "/etc/passwd", ""} {
		_, err = review.LoadRules(root, []string{p}, nil)
		wantInvalid(t, err, "rule_outside_repo")
	}
	_, err = review.LoadRules(root, []string{docsDir}, nil)
	wantInvalid(t, err, "rule_unreadable")

	// docs is a directory: reading it would fail, so success means the denied path was not read.
	rules, err = review.LoadRules(root, []string{docsDir, agentsMD, ".plait/missing.md"},
		[]string{docsDir, ".plait/**"})
	if err != nil {
		t.Fatal(err)
	}
	if len(rules) != 2 || rules[0] != (review.Rule{Path: docsDir, Excluded: true}) || rules[1].Path != agentsMD {
		t.Errorf("rules with deny globs = %+v", rules)
	}
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
	cfg := config.Config{Review: config.Review{Rules: []string{agentsMD, claudeMD}}}

	if _, _, err := s.New("order-retry", ""); err != nil {
		t.Fatal(err)
	}
	_, err := review.Export(t.Context(), s, repo, cfg)
	wantInvalid(t, err, "no_task")

	if _, err = task.Save(s, task.Input{Text: []byte("# Retry\n\n## Acceptance criteria\n\n- 3 times\n"),
		Source: task.DefaultSource}); err != nil {
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
		if _, err := task.Save(s, task.Input{Text: []byte(text), Source: task.DefaultSource}); err != nil {
			t.Fatal(err)
		}
	}
	save("# Retry\n")
	first, err := review.Export(t.Context(), s, repo, config.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if first.Coverage != review.CoverageDiffOnly {
		t.Errorf("coverage = %s, want diff_only", first.Coverage)
	}
	save("# Retry\n\n## Acceptance criteria\n\n- 3 times\n")
	second, err := review.Export(t.Context(), s, repo, config.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if second.Snapshot != 2 || second.Commit == first.Commit || second.Coverage != review.CoverageCriteria {
		t.Errorf("export after a new task = %+v, want snapshot 2 with another commit and criteria", second)
	}
	_, err = review.Import(t.Context(), s, repo, answer(first.Commit, review.CoverageCriteria), "-")
	wantInvalid(t, err, "snapshot_mismatch")
}

func TestExportScope(t *testing.T) {
	dir := newRepo(t)
	write(t, dir, envFile, "TOKEN=old\n")
	git(t, dir, "add", "-A")
	git(t, dir, "commit", "-q", "-m", "env")
	git(t, dir, "checkout", "-q", "-b", "feature")
	write(t, dir, "a.txt", "a changed\n")
	write(t, dir, envFile, "TOKEN=SECRET-MARKER\n")
	write(t, dir, "keys/server.pem", "PEM-MARKER\n")
	write(t, dir, "big.json", strings.Repeat("{\"big\": true}\n", 200))

	s := run.Open(dir)
	repo := vcs.Repo{Root: dir}
	if _, _, err := s.New("order-retry", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := task.Save(s, task.Input{Text: []byte("# Retry\n"), Source: task.DefaultSource}); err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{
		Review: config.Review{Rules: []string{envFile}, MaxDiffBytes: 1024},
		Audit:  config.Audit{DenyGlobs: []string{envFile, pemGlob}},
	}
	res, err := review.Export(t.Context(), s, repo, cfg)
	if err != nil {
		t.Fatal(err)
	}
	pkg := read(t, filepath.Join(dir, res.Package))
	for _, leak := range []string{"SECRET-MARKER", "TOKEN=old", "PEM-MARKER", `{"big": true}`} {
		if strings.Contains(pkg, leak) {
			t.Errorf("package leaks %q", leak)
		}
	}
	for _, want := range []string{"+a changed", "`big.json`: dropped", "`.env`: excluded by audit.deny_globs",
		"`keys/server.pem`: excluded"} {
		if !strings.Contains(pkg, want) {
			t.Errorf("package lacks %q", want)
		}
	}
	wantGaps := []run.Gap{
		{Code: review.GapDiffTruncated, Items: []string{"big.json"}},
		{Code: review.GapFilesExcluded, Items: []string{envFile, "keys/server.pem"}},
	}
	if !reflect.DeepEqual(res.Gaps, wantGaps) {
		t.Errorf("gaps = %+v, want %+v", res.Gaps, wantGaps)
	}
	st, err := s.Current()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(st.Snapshots[0].CoverageGaps, wantGaps) {
		t.Errorf("state gaps = %+v", st.Snapshots[0].CoverageGaps)
	}

	cfg.Audit.DenyGlobs = []string{badGlob}
	_, err = review.Export(t.Context(), s, repo, cfg)
	wantInvalid(t, err, "config_invalid")
}

func TestExportChecks(t *testing.T) {
	dir := newRepo(t)
	git(t, dir, "checkout", "-q", "-b", "feature")
	write(t, dir, "a.txt", "a changed\n")
	s := run.Open(dir)
	repo := vcs.Repo{Root: dir}
	if _, _, err := s.New("order-retry", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := task.Save(s, task.Input{Text: []byte("# Retry\n"), Source: task.DefaultSource}); err != nil {
		t.Fatal(err)
	}
	save := func(name string, code int, out string) {
		t.Helper()
		in := check.Input{Name: name, ExitCode: code, Output: []byte(out)}
		if _, err := check.Save(t.Context(), s, repo, in); err != nil {
			t.Fatal(err)
		}
	}
	save(checkLint, 1, "STALE-LINT-MARKER\n")
	write(t, dir, "a.txt", "a changed again\n")
	save("test", 0, "FRESH-TEST-MARKER\n")
	save(checkVet, 2, "FRESH-VET-MARKER\n")

	cfg := config.Config{Review: config.Review{Checks: []string{"test", checkLint, checkBuild}}}
	res, err := review.Export(t.Context(), s, repo, cfg)
	if err != nil {
		t.Fatal(err)
	}
	wantGaps := []run.Gap{
		{Code: review.GapChecksMissing, Items: []string{checkBuild}},
		{Code: review.GapChecksFailed, Items: []string{checkVet}},
		{Code: review.GapChecksStale, Items: []string{checkLint}},
	}
	if !reflect.DeepEqual(res.Gaps, wantGaps) {
		t.Errorf("gaps = %+v, want %+v", res.Gaps, wantGaps)
	}
	pkg := read(t, filepath.Join(dir, res.Package))
	for _, want := range []string{"### test: fresh, exit code 0", "FRESH-TEST-MARKER", "### lint: stale, exit code 1",
		"### build: missing\n", "### vet: fresh, exit code 2", "FRESH-VET-MARKER"} {
		if !strings.Contains(pkg, want) {
			t.Errorf("package lacks %q", want)
		}
	}
	if strings.Contains(pkg, "STALE-LINT-MARKER") {
		t.Error("package shows the output of a stale check")
	}
	if i, j := strings.Index(pkg, "### lint"), strings.Index(pkg, "### vet"); i > j {
		t.Error("saved checks outside review.checks come before the expected ones")
	}

	again, err := review.Export(t.Context(), s, repo, cfg)
	if err != nil || again.Changed {
		t.Errorf("repeat export = %+v, %v, want unchanged", again, err)
	}
	save(checkBuild, 0, "built\n")
	third, err := review.Export(t.Context(), s, repo, cfg)
	if err != nil || !third.Changed || third.Snapshot != 2 {
		t.Errorf("export after a new check = %+v, %v, want snapshot 2", third, err)
	}

	write(t, dir, filepath.Join(".plait/runs", res.Run, check.Dir, "test.json"), "{broken")
	_, err = review.Export(t.Context(), s, repo, cfg)
	wantInvalid(t, err, "check_unreadable")
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
