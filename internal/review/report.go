package review

import (
	"bytes"
	"context"
	_ "embed"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"text/template"

	"github.com/lllypuk/plait/internal/output"
	"github.com/lllypuk/plait/internal/run"
	"github.com/lllypuk/plait/internal/vcs"
)

// Coverage labels in words: the report must not let a diff-only review pass for a check against the task.
const (
	LabelCriteria = "checked against the task's acceptance criteria"
	LabelDiffOnly = "only the diff was checked: the task has no acceptance criteria"
)

//go:embed templates/report.md.tmpl
var reportTmpl string

// severities lists the schema enum from the most to the least severe.
func severities() []string { return []string{"blocker", "major", "minor", "nit"} }

// Report is the imported review of a run as shown to the developer.
type Report struct {
	Run           string    `json:"run"`
	Snapshot      int       `json:"snapshot"`
	Commit        string    `json:"commit"`
	Base          string    `json:"base"`
	Coverage      string    `json:"coverage"`
	CoverageLabel string    `json:"coverage_label"`
	Stale         bool      `json:"stale"`
	Review        string    `json:"review"`
	Counts        Counts    `json:"counts"`
	Findings      []Finding `json:"findings"`
	Step          run.Step  `json:"step"`
}

// Counts is the number of findings per severity.
type Counts struct {
	Blocker int `json:"blocker"`
	Major   int `json:"major"`
	Minor   int `json:"minor"`
	Nit     int `json:"nit"`
}

// Group is the findings of one severity, for the text report.
type Group struct {
	Severity string
	Findings []Finding
}

// NewReport orders the findings by severity, keeping the reviewer's order inside one severity.
func NewReport(a Answer, snap run.Snapshot, st *run.State, review string, stale bool) Report {
	order := severities()
	findings := slices.Clone(a.Findings)
	slices.SortStableFunc(findings, func(x, y Finding) int {
		return slices.Index(order, x.Severity) - slices.Index(order, y.Severity)
	})
	var c Counts
	for _, f := range findings {
		switch f.Severity {
		case "blocker":
			c.Blocker++
		case "major":
			c.Major++
		case "minor":
			c.Minor++
		case "nit":
			c.Nit++
		}
	}
	label := LabelDiffOnly
	if a.Coverage == CoverageCriteria {
		label = LabelCriteria
	}
	if findings == nil {
		findings = []Finding{}
	}
	return Report{
		Run: st.ID, Snapshot: snap.N, Commit: snap.Commit, Base: snap.Base, Coverage: a.Coverage,
		CoverageLabel: label, Stale: stale, Review: review, Counts: c, Findings: findings, Step: st.Step,
	}
}

// Groups splits the findings by severity, skipping empty ones.
func (r Report) Groups() []Group {
	var gs []Group
	for _, sev := range severities() {
		var list []Finding
		for _, f := range r.Findings {
			if f.Severity == sev {
				list = append(list, f)
			}
		}
		if len(list) > 0 {
			gs = append(gs, Group{Severity: sev, Findings: list})
		}
	}
	return gs
}

// RenderReport writes the report as Markdown for a human.
func RenderReport(r Report) ([]byte, error) {
	tmpl, err := template.New("report").Funcs(template.FuncMap{
		"title":  func(s string) string { return strings.ToUpper(s[:1]) + s[1:] },
		"indent": func(s string) string { return strings.ReplaceAll(strings.TrimSpace(s), "\n", "\n  ") },
	}).Parse(reportTmpl)
	if err != nil {
		return nil, output.Internal(fmt.Errorf("parse report template: %w", err))
	}
	var b bytes.Buffer
	if err = tmpl.Execute(&b, r); err != nil {
		return nil, output.Internal(fmt.Errorf("render report: %w", err))
	}
	return b.Bytes(), nil
}

// BuildReport reads the last imported review of the current run and moves the run from imported to reported.
func BuildReport(ctx context.Context, s *run.Store, repo vcs.Repo) (Report, error) {
	unlock, err := s.Lock()
	if err != nil {
		return Report{}, err
	}
	defer unlock()
	st, err := s.Current()
	if err != nil {
		return Report{}, err
	}
	if len(st.Reviews) == 0 {
		fix := "export the package with plait review export, review package.md in a fresh session, " +
			"then plait review import <file>"
		if st.Step == run.StepExported {
			fix = "review the exported package.md in a fresh session, then plait review import <file>"
		}
		return Report{}, output.Invalid("no_review", fmt.Sprintf("run %s has no imported review", st.ID), fix)
	}
	rev := st.Reviews[len(st.Reviews)-1]
	i := slices.IndexFunc(st.Snapshots, func(sn run.Snapshot) bool { return sn.N == rev.N })
	if i < 0 {
		return Report{}, run.BrokenState(st, "review "+strconv.Itoa(rev.N)+" without its snapshot")
	}
	snap := st.Snapshots[i]
	path := filepath.Join(s.RunDir(st.ID), "review-"+strconv.Itoa(rev.N)+".json")
	rel := filepath.ToSlash(path)
	if r, relErr := filepath.Rel(repo.Root, path); relErr == nil {
		rel = filepath.ToSlash(r)
	}
	data, err := os.ReadFile(filepath.Clean(path))
	if errors.Is(err, fs.ErrNotExist) {
		return Report{}, output.Invalid("review_missing", fmt.Sprintf("imported review %s is missing", rel),
			"import the fresh session's answer again: plait review import <file>")
	}
	if err != nil {
		return Report{}, output.Internal(fmt.Errorf("read %s: %w", rel, err))
	}
	a, err := ParseAnswer(data, rel)
	if err == nil && a.Snapshot != snap.Commit {
		err = fmt.Errorf("it reviews %s, not snapshot %d %s", a.Snapshot, snap.N, snap.Commit)
	}
	if err != nil {
		return Report{}, output.NeedsHuman("review_corrupt", fmt.Sprintf("imported review %s: %v", rel, err),
			"stop and ask a human; to recover they remove "+rel+" and call plait review import again")
	}
	tree, err := repo.CurrentTree(ctx)
	if err != nil {
		return Report{}, err
	}
	if st.Step == run.StepImported {
		st.Step = run.StepReported
		if err = s.Save(st); err != nil {
			return Report{}, err
		}
	}
	return NewReport(a, snap, st, rel, tree != snap.Tree), nil
}
