package review_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/lllypuk/plait/internal/config"
	"github.com/lllypuk/plait/internal/output"
	"github.com/lllypuk/plait/internal/review"
	"github.com/lllypuk/plait/internal/run"
	"github.com/lllypuk/plait/internal/vcs"
)

const (
	cmpFile = "c.go"
	ownKey  = "R2-X"
	aTxt    = "a.txt"
)

func carried(at vcs.Location) review.Previous {
	return review.Previous{
		Tracked: review.Tracked{
			Key: keyF1, Severity: sevBlocker, File: cmpFile, Line: 10, State: vcs.LineUnchanged, Title: "No retry",
			Rationale: "r", Status: review.StatusNew, History: []review.Event{{Review: 1, Status: review.StatusNew}},
		},
		At: at,
	}
}

type compareCase struct {
	name     string
	at       vcs.Location
	verdicts []review.Verdict
	status   string
	line     int
	// own is "line:title" of a new finding in cmpFile; merged says it merges into the carried one.
	own    string
	merged bool
}

func TestCompare(t *testing.T) {
	same := vcs.Location{File: cmpFile, Line: 12, State: vcs.LineUnchanged}
	changed := vcs.Location{File: cmpFile, Line: 11, State: vcs.LineChanged}
	gone := vcs.Location{State: vcs.LineGone}
	fixed := []review.Verdict{{Key: keyF1, Verdict: review.VerdictFixed, Note: "n"}}
	stillOpen := []review.Verdict{{Key: keyF1, Verdict: review.VerdictStillOpen, Note: "n"}}
	notChecked := []review.Verdict{{Key: keyF1, Verdict: review.VerdictNotChecked}}
	cases := []compareCase{
		{"fixed, line kept", same, fixed, review.StatusFixConfirmed, 12, "", false},
		{"fixed, line gone", gone, fixed, review.StatusFixConfirmed, 10, "", false},
		{"still open, region changed", changed, stillOpen, review.StatusOpen, 11, "", false},
		{"no verdict, line kept", same, nil, review.StatusOpen, 12, "", false},
		{"not checked, line kept", same, notChecked, review.StatusOpen, 12, "", false},
		{"not checked, region changed", changed, notChecked, review.StatusRegionChanged, 11, "", false},
		{"no verdict, region changed", changed, nil, review.StatusRegionChanged, 11, "", false},
		{"no verdict, line gone is outdated, not fixed", gone, nil, review.StatusOutdated, 10, "", false},
		{"not checked, line gone", gone, notChecked, review.StatusOutdated, 10, "", false},
		{"duplicate of a changed region", changed, nil, review.StatusOpen, 11, "11:no RETRY", true},
		{"duplicate overrides fixed", same, fixed, review.StatusOpen, 12, "12:No retry", true},
		{"same title on another line", same, nil, review.StatusOpen, 12, "10:No retry", false},
		{"other title on the line", same, nil, review.StatusOpen, 12, "12:Retry storm", false},
		{"gone line merges nothing", gone, nil, review.StatusOutdated, 10, "10:No retry", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var own []review.Finding
			if tc.own != "" {
				at, title, _ := strings.Cut(tc.own, ":")
				line, err := strconv.Atoi(at)
				if err != nil {
					t.Fatal(err)
				}
				own = append(own, review.Finding{ID: "X", Severity: "minor", File: cmpFile, Line: line,
					Title: title, Rationale: "r"})
			}
			checkCompare(t, tc, review.Compare(2, []review.Previous{carried(tc.at)}, tc.verdicts, own))
		})
	}
}

func checkCompare(t *testing.T, tc compareCase, got []review.Tracked) {
	t.Helper()
	p := got[0]
	if p.Key != keyF1 || p.Status != tc.status || p.File != cmpFile || p.Line != tc.line || p.State != tc.at.State {
		t.Errorf("previous = %+v, want status %s at line %d", p, tc.status, tc.line)
	}
	want := review.Event{Review: 2, Status: tc.status}
	if len(tc.verdicts) > 0 {
		want.Verdict, want.Note = tc.verdicts[0].Verdict, tc.verdicts[0].Note
	}
	if tc.merged {
		want.Duplicate = ownKey
	}
	if len(p.History) != 2 || p.History[0].Status != review.StatusNew || p.History[1] != want {
		t.Errorf("history = %+v, want last %+v", p.History, want)
	}
	switch {
	case tc.merged && len(got) != 1:
		t.Errorf("findings = %+v, want the new one merged", got)
	case !tc.merged && tc.own != "" && (len(got) != 2 || got[1].Key != ownKey || got[1].Status != review.StatusNew ||
		!reflect.DeepEqual(got[1].History, []review.Event{{Review: 2, Status: review.StatusNew}})):
		t.Errorf("findings = %+v, want the new one kept", got)
	}
}

func TestCompareKeepsHistoryOfInput(t *testing.T) {
	p := carried(vcs.Location{File: cmpFile, Line: 10, State: vcs.LineUnchanged})
	p.History = slices.Grow(p.History, 4)
	two := review.Compare(2, []review.Previous{p}, nil, nil)[0].History
	review.Compare(3, []review.Previous{p}, nil, nil)
	if len(p.History) != 1 || two[1].Review != 2 {
		t.Errorf("input history = %+v, review 2 history = %+v", p.History, two)
	}
}

func TestImportTracked(t *testing.T) {
	dir, s, repo, first := exported(t)
	const f3 = `{"id":"F3","severity":"minor","file":"b.txt","line":1,"title":"t3","rationale":"r3"}`
	ans := answer(first.Commit, review.CoverageCriteria, findingJSON, wholeFileJSON)
	if _, err := review.Import(t.Context(), s, repo, ans, "-"); err != nil {
		t.Fatal(err)
	}
	st, err := s.Current()
	if err != nil {
		t.Fatal(err)
	}
	path1 := filepath.Join(s.RunDir(st.ID), "findings-1.json")
	one := tracked(t, path1)
	if one.Review != 1 || one.Snapshot != first.Commit || one.Run != st.ID ||
		!slices.Equal(statuses(one), []string{keyF1 + ":new", keyF2 + ":new"}) {
		t.Errorf("findings-1 = %+v", one)
	}
	before := read(t, path1)
	res, err := review.Import(t.Context(), s, repo, ans, "-")
	if err != nil || res.Changed || read(t, path1) != before {
		t.Errorf("import again = %+v, err = %v, findings-1 changed", res, err)
	}

	write(t, dir, "b.txt", "b\n")
	second := exportOK(t, s, repo)
	dup := `{"id":"F1","severity":"major","file":"a.txt","line":1,"title":"T","rationale":"again"}`
	if _, err = review.Import(t.Context(), s, repo, bytes.Replace(
		withPrevious(second.Commit, `"previous":[{"key":"R1-F2","verdict":"fixed","note":"done"}]`),
		[]byte(`"findings":[]`), []byte(`"findings":[`+dup+`,`+f3+`]`), 1), "-"); err != nil {
		t.Fatal(err)
	}
	two := tracked(t, filepath.Join(s.RunDir(st.ID), "findings-2.json"))
	if !slices.Equal(statuses(two), []string{keyF1 + ":open", keyF2 + ":fix_confirmed", "R2-F3:new"}) ||
		two.Findings[0].History[1].Duplicate != "R2-F1" ||
		two.Findings[0].History[1].Verdict != review.VerdictNotChecked {
		t.Errorf("findings-2 = %+v", two)
	}

	git(t, dir, "rm", "-q", "--cached", aTxt)
	if err = os.Remove(filepath.Join(dir, aTxt)); err != nil {
		t.Fatal(err)
	}
	third := exportOK(t, s, repo)
	if st, err = s.Current(); err != nil {
		t.Fatal(err)
	}
	if keys := st.Snapshots[2].Previous; !slices.Equal(keys, []string{keyF1, "R2-F3"}) {
		t.Errorf("snapshot 3 previous = %v, want the fixed finding dropped", keys)
	}
	if _, err = review.Import(t.Context(), s, repo, answer(third.Commit, review.CoverageCriteria), "-"); err != nil {
		t.Fatal(err)
	}
	three := tracked(t, filepath.Join(s.RunDir(st.ID), "findings-3.json"))
	if !slices.Equal(statuses(three), []string{keyF1 + ":outdated", "R2-F3:open"}) ||
		three.Findings[0].File != aTxt || len(three.Findings[0].History) != 3 {
		t.Errorf("findings-3 = %+v", three)
	}
}

func TestExportTrackedBroken(t *testing.T) {
	cases := []struct{ name, content, code string }{
		{"missing", "", "findings_missing"},
		{"not json", "{", "findings_corrupt"},
		{"other schema", `{"schema":"x/v1"}`, "findings_corrupt"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir, s, repo, first := exported(t)
			if _, err := review.Import(t.Context(), s, repo,
				answer(first.Commit, review.CoverageCriteria, findingJSON), "-"); err != nil {
				t.Fatal(err)
			}
			st, err := s.Current()
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(s.RunDir(st.ID), "findings-1.json")
			if tc.content == "" {
				err = os.Remove(path)
			} else {
				err = os.WriteFile(path, []byte(tc.content), 0o600)
			}
			if err != nil {
				t.Fatal(err)
			}
			write(t, dir, "b.txt", "b\n")
			_, err = review.Export(t.Context(), s, repo, config.Config{})
			if e, ok := errors.AsType[*output.Error](err); !ok || e.Kind != output.KindNeedsHuman || e.Code != tc.code {
				t.Errorf("err = %v, want needs-human %s", err, tc.code)
			}
		})
	}
}

func exportOK(t *testing.T, s *run.Store, repo vcs.Repo) review.ExportResult {
	t.Helper()
	exp, err := review.Export(t.Context(), s, repo, config.Config{})
	if err != nil {
		t.Fatal(err)
	}
	return exp
}

// tracked reads a findings file and checks it against plait-findings/v1.
func tracked(t *testing.T, path string) review.TrackedFile {
	t.Helper()
	data := read(t, path)
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader([]byte(data)))
	if err != nil {
		t.Fatal(err)
	}
	c := jsonschema.NewCompiler()
	sch, err := c.Compile(filepath.Join("schemas", "plait-findings.v1.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err = sch.Validate(doc); err != nil {
		t.Errorf("%s does not match plait-findings/v1: %v", path, err)
	}
	var f review.TrackedFile
	if err = json.Unmarshal([]byte(data), &f); err != nil {
		t.Fatal(err)
	}
	return f
}

func statuses(f review.TrackedFile) []string {
	out := make([]string, 0, len(f.Findings))
	for _, t := range f.Findings {
		out = append(out, t.Key+":"+t.Status)
	}
	return out
}
