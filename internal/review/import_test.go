package review_test

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/lllypuk/plait/internal/config"
	"github.com/lllypuk/plait/internal/output"
	"github.com/lllypuk/plait/internal/review"
	"github.com/lllypuk/plait/internal/run"
	"github.com/lllypuk/plait/internal/task"
	"github.com/lllypuk/plait/internal/vcs"
)

const findingJSON = `{"id":"F1","severity":"major","file":"a.txt","line":1,"title":"t","rationale":"r"}`

func answer(commit, coverage string, findings ...string) []byte {
	return fmt.Appendf(nil, `{"snapshot":%q,"coverage":%q,"findings":[%s]}`,
		commit, coverage, strings.Join(findings, ","))
}

func TestParseAnswer(t *testing.T) {
	const violation = "schema_violation"
	fractional := strings.Replace(findingJSON, `"line":1`, `"line":1.5`, 1)
	cases := []struct {
		name, data, code, at string
	}{
		{"valid", string(answer(commitID, "criteria", findingJSON)), "", ""},
		{"not json", "```json\n{}\n```", "invalid_json", ""},
		{"trailing data", `{} {}`, "invalid_json", ""},
		{"missing snapshot", `{"coverage":"criteria","findings":[]}`, violation, "at /: "},
		{"bad severity", string(answer(commitID, "criteria", strings.Replace(findingJSON, "major", "high", 1))),
			violation, "at /findings/0/severity: "},
		{"fractional line", string(answer(commitID, "criteria", fractional)),
			violation, "at /findings/0/line: "},
		{"extra field", `{"snapshot":"` + commitID + `","coverage":"criteria","findings":[],"x/y":1}`,
			violation, "at /: "},
		{"duplicate id", string(answer(commitID, "criteria", findingJSON, findingJSON)),
			violation, "at /findings/1/id: "},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a, err := review.ParseAnswer([]byte(tc.data), "answer.json")
			if tc.code == "" {
				if err != nil || a.Snapshot != commitID || len(a.Findings) != 1 || a.Findings[0].Line != 1 {
					t.Fatalf("answer = %+v, err = %v", a, err)
				}
				return
			}
			wantInvalid(t, err, tc.code)
			e, _ := errors.AsType[*output.Error](err)
			if !strings.Contains(e.Message, tc.at) || !strings.Contains(e.Fix, "plait review import answer.json") {
				t.Errorf("error = %q, fix = %q, want location %q", e.Message, e.Fix, tc.at)
			}
		})
	}
}

func TestImport(t *testing.T) {
	dir, s, repo, exp := exported(t)
	ok := answer(exp.Commit, review.CoverageCriteria, findingJSON)

	res, err := review.Import(t.Context(), s, repo, ok, "answer.json")
	if err != nil {
		t.Fatal(err)
	}
	if !res.Changed || res.Stale || res.Snapshot != 1 || res.Findings != 1 || res.Step != run.StepImported ||
		res.Review != ".plait/runs/"+exp.Run+"/review-1.json" {
		t.Errorf("import = %+v", res)
	}
	saved := read(t, filepath.Join(dir, res.Review))
	st, err := s.Current()
	if err != nil {
		t.Fatal(err)
	}
	if len(st.Reviews) != 1 || st.Reviews[0].N != 1 || st.Reviews[0].Snapshot != exp.Commit ||
		st.Reviews[0].Coverage != review.CoverageCriteria {
		t.Errorf("reviews = %+v", st.Reviews)
	}

	again, err := review.Import(t.Context(), s, repo, append(ok, '\n'), "-")
	if err != nil || again.Changed || again.Step != run.StepImported {
		t.Errorf("repeat import = %+v, err = %v, want unchanged", again, err)
	}
	if read(t, filepath.Join(dir, res.Review)) != saved {
		t.Error("repeat import rewrote the review")
	}

	_, err = review.Import(t.Context(), s, repo, answer(exp.Commit, review.CoverageCriteria), "answer.json")
	if e, isErr := errors.AsType[*output.Error](err); !isErr || e.Kind != output.KindNeedsHuman ||
		e.Code != "review_exists" {
		t.Errorf("different answer: err = %#v, want NeedsHuman review_exists", err)
	}

	_, err = review.Import(t.Context(), s, repo, answer(commitID, review.CoverageCriteria), "answer.json")
	wantInvalid(t, err, "snapshot_mismatch")
	_, err = review.Import(t.Context(), s, repo, answer(exp.Commit, review.CoverageDiffOnly), "answer.json")
	wantInvalid(t, err, "coverage_mismatch")
	_, err = review.Import(t.Context(), s, repo, []byte(`{}`), "answer.json")
	wantInvalid(t, err, "schema_violation")

	if err = os.Remove(filepath.Join(dir, res.Review)); err != nil {
		t.Fatal(err)
	}
	replaced, err := review.Import(t.Context(), s, repo, answer(exp.Commit, review.CoverageCriteria), "answer.json")
	if err != nil || !replaced.Changed || replaced.Findings != 0 {
		t.Errorf("import after removal = %+v, err = %v", replaced, err)
	}
	if st, err = s.Current(); err != nil || len(st.Reviews) != 1 {
		t.Errorf("reviews after replace = %+v, err = %v", st, err)
	}
}

func TestImportConcurrent(t *testing.T) {
	_, s, repo, exp := exported(t)
	answers := [][]byte{
		answer(exp.Commit, review.CoverageCriteria),
		answer(exp.Commit, review.CoverageCriteria, findingJSON),
	}
	errs := make([]error, len(answers))
	var wg sync.WaitGroup
	for i, a := range answers {
		wg.Go(func() { _, errs[i] = review.Import(t.Context(), s, repo, a, "-") })
	}
	wg.Wait()
	var ok, exists int
	for _, err := range errs {
		e, isErr := errors.AsType[*output.Error](err)
		switch {
		case err == nil:
			ok++
		case isErr && e.Code == "review_exists":
			exists++
		}
	}
	if ok != 1 || exists != 1 {
		t.Errorf("concurrent imports: errs = %v, want one success and one review_exists", errs)
	}
}

func TestImportStale(t *testing.T) {
	dir, s, repo, exp := exported(t)
	write(t, dir, "a.txt", "a changed after export\n")
	res, err := review.Import(t.Context(), s, repo, answer(exp.Commit, review.CoverageCriteria), "-")
	if err != nil || !res.Stale || !res.Changed {
		t.Errorf("import after a change = %+v, err = %v, want stale", res, err)
	}
}

func TestImportNotExported(t *testing.T) {
	dir := newRepo(t)
	s := run.Open(dir)
	if _, _, err := s.New("order-retry", ""); err != nil {
		t.Fatal(err)
	}
	_, err := review.Import(t.Context(), s, vcs.Repo{Root: dir}, answer(commitID, review.CoverageDiffOnly), "-")
	wantInvalid(t, err, "not_exported")
}

func exported(t *testing.T) (string, *run.Store, vcs.Repo, review.ExportResult) {
	t.Helper()
	dir := newRepo(t)
	git(t, dir, "checkout", "-q", "-b", "feature")
	write(t, dir, "a.txt", "a changed\n")
	s := run.Open(dir)
	repo := vcs.Repo{Root: dir}
	if _, _, err := s.New("order-retry", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := task.Save(s, task.Input{Text: []byte("# Retry\n\n## Acceptance criteria\n\n- 3 times\n"),
		Source: task.DefaultSource}); err != nil {
		t.Fatal(err)
	}
	exp, err := review.Export(t.Context(), s, repo, config.Config{})
	if err != nil {
		t.Fatal(err)
	}
	return dir, s, repo, exp
}
