package review_test

import (
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/lllypuk/plait/internal/config"
	"github.com/lllypuk/plait/internal/output"
	"github.com/lllypuk/plait/internal/review"
	"github.com/lllypuk/plait/internal/run"
)

const wholeFileJSON = `{"id":"F2","severity":"minor","file":"a.txt","line":0,"title":"t2","rationale":"r2"}`

func withPrevious(commit, extra string) []byte {
	return []byte(`{"snapshot":"` + commit + `","coverage":"criteria","findings":[],` + extra + `}`)
}

func TestParseAnswerPrevious(t *testing.T) {
	const violation = "schema_violation"
	cases := []struct {
		name, extra, at string
	}{
		{"fixed without note", `"previous":[{"key":"R1-F1","verdict":"fixed"}]`, "at /previous/0: "},
		{"still open without note", `"previous":[{"key":"R1-F1","verdict":"still_open"}]`, "at /previous/0: "},
		{"bad verdict", `"previous":[{"key":"R1-F1","verdict":"done","note":"n"}]`, "at /previous/0/verdict: "},
		{"bad key", `"previous":[{"key":"F1","verdict":"not_checked"}]`, "at /previous/0/key: "},
		{"duplicate key", `"previous":[{"key":"R1-F1","verdict":"not_checked"},` +
			`{"key":"R1-F1","verdict":"fixed","note":"n"}]`, "at /previous/1/key: "},
		{"empty criterion", `"unchecked_criteria":[""]`, "at /unchecked_criteria/0: "},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := review.ParseAnswer(withPrevious(commitID, tc.extra), "-")
			wantInvalid(t, err, violation)
			if e, _ := errors.AsType[*output.Error](err); !strings.Contains(e.Message, tc.at) {
				t.Errorf("error = %q, want location %q", e.Message, tc.at)
			}
		})
	}

	a, err := review.ParseAnswer(withPrevious(commitID, `"previous":[{"key":"R1-F1","verdict":"not_checked"},`+
		`{"key":"R12-a.b","verdict":"fixed","note":"guarded"}],"unchecked_criteria":["3 times"]`), "-")
	if err != nil || len(a.Previous) != 2 || a.Previous[1].Note != "guarded" ||
		!slices.Equal(a.UncheckedCriteria, []string{"3 times"}) {
		t.Errorf("answer = %+v, err = %v", a, err)
	}
}

func TestPreviousRound(t *testing.T) {
	dir, s, repo, first := exported(t)
	_, err := review.Import(t.Context(), s, repo, withPrevious(first.Commit,
		`"previous":[{"key":"R1-F1","verdict":"not_checked"}]`), "-")
	wantInvalid(t, err, "unknown_previous")
	if _, err = review.Import(t.Context(), s, repo,
		answer(first.Commit, review.CoverageCriteria, findingJSON, wholeFileJSON), "-"); err != nil {
		t.Fatal(err)
	}

	write(t, dir, "b.txt", "b\n")
	second, err := review.Export(t.Context(), s, repo, config.Config{})
	if err != nil {
		t.Fatal(err)
	}
	pkg := read(t, filepath.Join(dir, second.Package))
	for _, want := range []string{"## Previous findings", "### `R1-F1`, major\n\nIn this snapshot: `a.txt:1`",
		"### `R1-F2`, minor\n\nIn this snapshot: `a.txt`, whole file"} {
		if !strings.Contains(pkg, want) {
			t.Errorf("package 2 lacks %q:\n%s", want, pkg)
		}
	}
	st, err := s.Current()
	if err != nil {
		t.Fatal(err)
	}
	if second.Snapshot != 2 || !slices.Equal(st.Snapshots[1].Previous, []string{keyF1, keyF2}) {
		t.Errorf("export = %+v, snapshots = %+v", second, st.Snapshots)
	}

	for _, key := range []string{"R1-F9", "R2-F1"} {
		_, err = review.Import(t.Context(), s, repo, withPrevious(second.Commit,
			`"previous":[{"key":"`+key+`","verdict":"not_checked"}]`), "answer.json")
		wantInvalid(t, err, "unknown_previous")
		if e, _ := errors.AsType[*output.Error](err); !strings.Contains(e.Fix, "R1-F1, R1-F2") {
			t.Errorf("fix = %q, want the listed keys", e.Fix)
		}
	}

	res, err := review.Import(t.Context(), s, repo, withPrevious(second.Commit,
		`"previous":[{"key":"R1-F2","verdict":"fixed","note":"gone"}],"unchecked_criteria":["3 times"]`), "-")
	if err != nil {
		t.Fatal(err)
	}
	saved, err := review.ParseAnswer([]byte(read(t, filepath.Join(dir, res.Review))), "-")
	if err != nil {
		t.Fatal(err)
	}
	want := []review.Verdict{
		{Key: keyF1, Verdict: review.VerdictNotChecked},
		{Key: keyF2, Verdict: review.VerdictFixed, Note: "gone"},
	}
	if !slices.Equal(saved.Previous, want) {
		t.Errorf("saved previous = %+v, want %+v", saved.Previous, want)
	}
	if st, err = s.Current(); err != nil {
		t.Fatal(err)
	}
	gaps := st.Reviews[1].CoverageGaps
	if len(gaps) != 1 || gaps[0].Code != review.GapCriteriaUnchecked ||
		!slices.Equal(gaps[0].Items, []string{"3 times"}) {
		t.Errorf("review 2 gaps = %+v", gaps)
	}
}

func TestExportNoFindingsKeepsSnapshot(t *testing.T) {
	_, s, repo, first := exported(t)
	if _, err := review.Import(t.Context(), s, repo, answer(first.Commit, review.CoverageCriteria), "-"); err != nil {
		t.Fatal(err)
	}
	again, err := review.Export(t.Context(), s, repo, config.Config{})
	if err != nil || again.Snapshot != 1 || again.Changed || again.Step != run.StepImported {
		t.Errorf("export after an empty review = %+v, err = %v, want snapshot 1 kept", again, err)
	}
}
