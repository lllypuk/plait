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

// statuses lists the finding statuses in the order of the report: what awaits a decision first.
func statuses() []string {
	return []string{StatusNew, StatusOpen, StatusRegionChanged, StatusOutdated, StatusFixConfirmed}
}

// Report is the imported review of a run as shown to the developer.
type Report struct {
	Run           string    `json:"run"`
	Snapshot      int       `json:"snapshot"`
	Commit        string    `json:"commit"`
	Base          string    `json:"base"`
	Coverage      string    `json:"coverage"`
	CoverageLabel string    `json:"coverage_label"`
	CoverageGaps  []run.Gap `json:"coverage_gaps"`
	Stale         bool      `json:"stale"`
	Review        string    `json:"review"`
	Counts        Counts    `json:"counts"`
	Blocking      int       `json:"blocking"`
	Findings      []Tracked `json:"findings"`
	Step          run.Step  `json:"step"`
}

// Counts is the number of findings per status.
type Counts struct {
	New           int `json:"new"`
	Open          int `json:"open"`
	RegionChanged int `json:"region_changed"`
	Outdated      int `json:"outdated"`
	FixConfirmed  int `json:"fix_confirmed"`
}

// Group is the findings of one status, for the text report.
type Group struct {
	Status   string
	Findings []Tracked
}

// NewReport orders the findings by status, then by severity, keeping the comparison order inside one.
func NewReport(st *run.State, snap run.Snapshot, rev run.Review, tracked []Tracked, review string, stale bool) Report {
	sts, sevs := statuses(), severities()
	findings := slices.Clone(tracked)
	slices.SortStableFunc(findings, func(x, y Tracked) int {
		if d := slices.Index(sts, x.Status) - slices.Index(sts, y.Status); d != 0 {
			return d
		}
		return slices.Index(sevs, x.Severity) - slices.Index(sevs, y.Severity)
	})
	var c Counts
	for _, f := range findings {
		switch f.Status {
		case StatusNew:
			c.New++
		case StatusOpen:
			c.Open++
		case StatusRegionChanged:
			c.RegionChanged++
		case StatusOutdated:
			c.Outdated++
		case StatusFixConfirmed:
			c.FixConfirmed++
		}
	}
	if findings == nil {
		findings = []Tracked{}
	}
	gaps := append(slices.Clone(snap.CoverageGaps), rev.CoverageGaps...)
	if gaps == nil {
		gaps = []run.Gap{}
	}
	return Report{
		Run: st.ID, Snapshot: snap.N, Commit: snap.Commit, Base: snap.Base, Coverage: rev.Coverage,
		CoverageLabel: CoverageLabel(rev.Coverage, gaps), CoverageGaps: gaps, Stale: stale, Review: review,
		Counts: c, Blocking: Blocking(findings), Findings: findings, Step: st.Step,
	}
}

// Blocking counts the unresolved blocker and major findings: they send the change back for another round.
func Blocking(tracked []Tracked) int {
	n := 0
	for _, t := range tracked {
		if t.Carried() && (t.Severity == "blocker" || t.Severity == "major") {
			n++
		}
	}
	return n
}

// CoverageLabel names the coverage in words and lists what the review could not see.
func CoverageLabel(coverage string, gaps []run.Gap) string {
	label := LabelDiffOnly
	if coverage == CoverageCriteria {
		label = LabelCriteria
	}
	if len(gaps) == 0 {
		return label
	}
	words := make([]string, 0, len(gaps))
	for _, g := range gaps {
		words = append(words, GapText(g))
	}
	return label + ", incomplete: " + strings.Join(words, "; ")
}

// GapText describes one coverage gap in words.
func GapText(g run.Gap) string {
	n := len(g.Items)
	switch g.Code {
	case GapDiffTruncated:
		return "diff truncated (" + plural(n, "file") + ")"
	case GapFilesExcluded:
		return "files excluded by audit.deny_globs (" + plural(n, "file") + ")"
	case GapChecksMissing:
		return "checks not saved: " + strings.Join(g.Items, ", ")
	case GapChecksFailed:
		return "checks failed: " + strings.Join(g.Items, ", ")
	case GapChecksStale:
		return "checks saved on another tree: " + strings.Join(g.Items, ", ")
	case GapCriteriaUnchecked:
		return "criteria the reviewer could not check (" + strconv.Itoa(n) + ")"
	}
	return g.Code + " (" + strconv.Itoa(n) + ")"
}

// gapItems lists the paths and criteria of a gap; check names are already in its text.
func gapItems(g run.Gap) []string {
	switch g.Code {
	case GapChecksMissing, GapChecksFailed, GapChecksStale:
		return nil
	case GapCriteriaUnchecked:
		return g.Items
	}
	out := make([]string, 0, len(g.Items))
	for _, it := range g.Items {
		out = append(out, "`"+it+"`")
	}
	return out
}

func plural(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return strconv.Itoa(n) + " " + noun + "s"
}

// Groups splits the findings by status, skipping empty ones.
func (r Report) Groups() []Group {
	var gs []Group
	for _, s := range statuses() {
		var list []Tracked
		for _, f := range r.Findings {
			if f.Status == s {
				list = append(list, f)
			}
		}
		if len(list) > 0 {
			gs = append(gs, Group{Status: s, Findings: list})
		}
	}
	return gs
}

// Heading names a status group in the text report.
func (g Group) Heading() string {
	switch g.Status {
	case StatusNew:
		return "New"
	case StatusOpen:
		return "Open"
	case StatusRegionChanged:
		return "Region changed, recheck"
	case StatusOutdated:
		return "Code gone, recheck"
	case StatusFixConfirmed:
		return "Fix confirmed"
	}
	return g.Status
}

// RenderReport writes the report as Markdown for a human.
func RenderReport(r Report) ([]byte, error) {
	tmpl, err := template.New("report").Funcs(template.FuncMap{
		"gap":    GapText,
		"items":  gapItems,
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
	tf, err := readTracked(trackedPath(s.RunDir(st.ID), rev.N))
	if err != nil {
		return Report{}, err
	}
	if tf.Snapshot != snap.Commit {
		tp := filepath.ToSlash(filepath.Join(filepath.Dir(rel), "findings-"+strconv.Itoa(rev.N)+".json"))
		return Report{}, output.NeedsHuman("findings_corrupt",
			fmt.Sprintf("%s compares snapshot %s, not snapshot %d %s", tp, tf.Snapshot, snap.N, snap.Commit),
			"stop and ask a human; to recover they remove "+tp+" and call plait review import again")
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
	return NewReport(st, snap, rev, tf.Findings, rel, tree != snap.Tree), nil
}

// Where renders the place of the finding in the snapshot of its last review.
func (t Tracked) Where() string {
	at := "`" + t.File + "`"
	if t.Line > 0 {
		at = "`" + t.File + ":" + strconv.Itoa(t.Line) + "`"
	}
	switch t.State {
	case vcs.LineChanged:
		return at + ", region changed"
	case vcs.LineGone:
		return at + ", gone"
	case vcs.LineUnchanged:
	}
	return at
}

// Note is the reviewer's note on the finding in its last review.
func (t Tracked) Note() string {
	if len(t.History) == 0 {
		return ""
	}
	return t.History[len(t.History)-1].Note
}
