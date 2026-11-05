package review_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/lllypuk/plait/internal/output"
	"github.com/lllypuk/plait/internal/review"
	"github.com/lllypuk/plait/internal/run"
)

func TestReportGolden(t *testing.T) {
	snap := run.Snapshot{N: 2, Commit: commitID, Base: baseID}
	cases := []struct {
		name  string
		a     review.Answer
		stale bool
	}{
		{"criteria", review.Answer{Snapshot: commitID, Coverage: review.CoverageCriteria, Findings: []review.Finding{
			{ID: "F1", Severity: "nit", File: "a.go", Line: 3, Title: "typo in name",
				Rationale: "retires, not retries"},
			{ID: "F2", Severity: "blocker", File: "b.go", Line: 10, Title: "no retry",
				Rationale: "the order fails at once\nafter the first error", Criterion: "retried 3 times"},
			{ID: "F3", Severity: "major", File: "b.go", Line: 12, Title: "error ignored", Rationale: "err is dropped"},
			{ID: "F4", Severity: "blocker", File: "c.go", Line: 1, Title: "panic", Rationale: "nil map"},
		}}, false},
		{"diff_only-stale-empty", review.Answer{Snapshot: commitID, Coverage: review.CoverageDiffOnly}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st := &run.State{ID: "PROJ-7-order-retry", Step: run.StepReported}
			r := review.NewReport(tc.a, snap, st, ".plait/runs/PROJ-7-order-retry/review-2.json", tc.stale)
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
	if r.Stale || r.Step != run.StepReported || r.CoverageLabel != review.LabelCriteria || r.Counts.Major != 1 ||
		r.Review != ".plait/runs/"+exp.Run+"/review-1.json" {
		t.Errorf("report = %+v", r)
	}
	if st, _ := s.Current(); st.Step != run.StepReported {
		t.Errorf("step = %s, want reported", st.Step)
	}

	write(t, dir, "a.txt", "a changed after export\n")
	if r, err = review.BuildReport(t.Context(), s, repo); err != nil || !r.Stale || r.Step != run.StepReported {
		t.Errorf("report after a change = %+v, err = %v, want stale", r, err)
	}

	path := filepath.Join(dir, r.Review)
	write(t, dir, r.Review, string(answer(commitID, review.CoverageCriteria)))
	_, err = review.BuildReport(t.Context(), s, repo)
	e, isErr := errors.AsType[*output.Error](err)
	if !isErr || e.Kind != output.KindNeedsHuman || e.Code != "review_corrupt" {
		t.Errorf("foreign answer: err = %#v, want NeedsHuman review_corrupt", err)
	}
	if err = os.Remove(path); err != nil {
		t.Fatal(err)
	}
	_, err = review.BuildReport(t.Context(), s, repo)
	wantInvalid(t, err, "review_missing")
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
