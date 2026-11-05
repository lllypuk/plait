package review_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/lllypuk/plait/internal/output"
	"github.com/lllypuk/plait/internal/review"
	"github.com/lllypuk/plait/internal/run"
	"github.com/lllypuk/plait/internal/vcs"
)

func TestReportGolden(t *testing.T) {
	first := review.Compare(1, nil, nil, []review.Finding{
		{ID: "F1", Severity: "nit", File: "a.go", Line: 3, Title: "typo in name", Rationale: "retires, not retries"},
		{ID: "F2", Severity: "blocker", File: "b.go", Line: 10, Title: "no retry",
			Rationale: "the order fails at once\nafter the first error", Criterion: criterion},
		{ID: "F3", Severity: sevMajor, File: "b.go", Line: 12, Title: "error ignored", Rationale: "err is dropped"},
		{ID: "F4", Severity: "blocker", File: "c.go", Line: 1, Title: "panic", Rationale: "nil map"},
	})
	at := func(file string, line int, state vcs.LineState) vcs.Location {
		return vcs.Location{File: file, Line: line, State: state}
	}
	second := review.Compare(2, []review.Previous{
		{Tracked: first[0], At: at("a.go", 3, vcs.LineUnchanged)},
		{Tracked: first[1], At: at("b.go", 14, vcs.LineUnchanged)},
		{Tracked: first[2], At: at("b.go", 15, vcs.LineChanged)},
		{Tracked: first[3], At: at("", 0, vcs.LineGone)},
	}, []review.Verdict{
		{Key: "R1-F1", Verdict: review.VerdictStillOpen, Note: "still misspelt\nin the log line"},
		{Key: "R1-F2", Verdict: review.VerdictFixed, Note: "retried in a loop now"},
		{Key: "R1-F3", Verdict: review.VerdictNotChecked},
	}, []review.Finding{
		{ID: "F1", Severity: sevMajor, File: "d.go", Line: 0, Title: "no tests", Rationale: "the retry is untested"},
	})
	gaps := []run.Gap{
		{Code: review.GapDiffTruncated, Items: []string{"gen/big.go", "gen/huge.go"}},
		{Code: review.GapFilesExcluded, Items: []string{".env"}},
		{Code: review.GapChecksMissing, Items: []string{checkTest}},
		{Code: review.GapChecksFailed, Items: []string{"lint"}},
		{Code: review.GapChecksStale, Items: []string{"vet"}},
	}
	unchecked := []run.Gap{{Code: review.GapCriteriaUnchecked, Items: []string{criterion}}}
	cases := []struct {
		name    string
		snap    run.Snapshot
		rev     run.Review
		tracked []review.Tracked
		stale   bool
	}{
		{"first", run.Snapshot{N: 1, Commit: commitID, Base: baseID},
			run.Review{N: 1, Coverage: review.CoverageCriteria}, first, false},
		{"second", run.Snapshot{N: 2, Commit: commitID, Base: baseID, CoverageGaps: gaps},
			run.Review{N: 2, Coverage: review.CoverageCriteria, CoverageGaps: unchecked}, second, false},
		{"diff_only-stale-empty", run.Snapshot{N: 2, Commit: commitID, Base: baseID},
			run.Review{N: 2, Coverage: review.CoverageDiffOnly}, nil, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st := &run.State{ID: "PROJ-7-order-retry", Step: run.StepReported}
			path := ".plait/runs/PROJ-7-order-retry/review-" + strconv.Itoa(tc.snap.N) + ".json"
			r := review.NewReport(st, tc.snap, tc.rev, tc.tracked, path, tc.stale)
			md, err := review.RenderReport(r)
			if err != nil {
				t.Fatal(err)
			}
			js, err := json.MarshalIndent(r, "", "  ")
			if err != nil {
				t.Fatal(err)
			}
			golden(t, "report-"+tc.name+".md.golden", md)
			golden(t, "report-"+tc.name+".json.golden", append(js, '\n'))
		})
	}
}

func TestBuildReport(t *testing.T) {
	dir, s, repo, exp := exported(t)
	_, err := review.BuildReport(t.Context(), s, repo)
	wantInvalid(t, err, "no_review")

	ok := answer(exp.Commit, review.CoverageCriteria, findingJSON)
	if _, err = review.Import(t.Context(), s, repo, ok, "-"); err != nil {
		t.Fatal(err)
	}
	r, err := review.BuildReport(t.Context(), s, repo)
	if err != nil {
		t.Fatal(err)
	}
	if r.Stale || r.Step != run.StepReported || r.CoverageLabel != review.LabelCriteria || r.Counts.New != 1 ||
		r.Blocking != 1 || r.Findings[0].Key != "R1-F1" || r.Review != ".plait/runs/"+exp.Run+"/review-1.json" {
		t.Errorf("report = %+v", r)
	}
	if st, _ := s.Current(); st.Step != run.StepReported || st.Reviews[0].Blocking != 1 {
		t.Errorf("state = %+v, want reported with 1 blocking", st)
	}

	write(t, dir, "a.txt", "a changed after export\n")
	if r, err = review.BuildReport(t.Context(), s, repo); err != nil || !r.Stale || r.Step != run.StepReported {
		t.Errorf("report after a change = %+v, err = %v, want stale", r, err)
	}

	compared := strings.Replace(r.Review, "review-1.json", "findings-1.json", 1)
	write(t, dir, compared, `{"schema":"`+review.TrackedSchema+`","snapshot":"`+commitID+`","findings":[]}`)
	_, err = review.BuildReport(t.Context(), s, repo)
	wantNeedsHuman(t, err, "findings_corrupt")
	if err = os.Remove(filepath.Join(dir, compared)); err != nil {
		t.Fatal(err)
	}
	_, err = review.BuildReport(t.Context(), s, repo)
	wantNeedsHuman(t, err, "findings_missing")

	path := filepath.Join(dir, r.Review)
	write(t, dir, r.Review, string(answer(commitID, review.CoverageCriteria)))
	_, err = review.BuildReport(t.Context(), s, repo)
	wantNeedsHuman(t, err, "review_corrupt")
	if err = os.Remove(path); err != nil {
		t.Fatal(err)
	}
	_, err = review.BuildReport(t.Context(), s, repo)
	wantInvalid(t, err, "review_missing")
}

func wantNeedsHuman(t *testing.T, err error, code string) {
	t.Helper()
	if e, ok := errors.AsType[*output.Error](err); !ok || e.Kind != output.KindNeedsHuman || e.Code != code {
		t.Errorf("err = %#v, want NeedsHuman %s", err, code)
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
