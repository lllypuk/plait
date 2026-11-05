// Package mr describes the merge request of a run from its task, review and checks; it creates nothing.
package mr

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

	"github.com/lllypuk/plait/internal/check"
	"github.com/lllypuk/plait/internal/config"
	"github.com/lllypuk/plait/internal/output"
	"github.com/lllypuk/plait/internal/review"
	"github.com/lllypuk/plait/internal/run"
	"github.com/lllypuk/plait/internal/task"
	"github.com/lllypuk/plait/internal/vcs"
)

// FileName is the description inside the run directory.
const FileName = "mr.md"

// Label marks the merge request as prepared with an agent.
const Label = "ai-assisted"

const reviewFix = "plait review export, review package.md in a fresh session, then plait review import <file>"

//go:embed templates/mr.md.tmpl
var builtin string

// Input is what mr describe receives.
type Input struct {
	Summary    string
	AllowStale bool
	Version    string
}

// Result describes what mr describe did.
type Result struct {
	Run      string   `json:"run"`
	Snapshot int      `json:"snapshot"`
	Title    string   `json:"title"`
	Body     string   `json:"body"`
	Labels   []string `json:"labels"`
	Path     string   `json:"path"`
	Stale    bool     `json:"stale"`
	Changed  bool     `json:"changed"`
	Step     run.Step `json:"step"`
}

// Data is what the built-in template and mr.template render.
type Data struct {
	Title    string
	Key      string
	Task     string
	Summary  string
	Criteria []Criterion
	Review   Review
	Checks   []review.Check
	Stale    bool
	Labels   []string
	Version  string
}

// Criterion is an acceptance criterion; Flagged when an unresolved finding quotes it. plait never marks one as met.
type Criterion struct {
	Text    string
	Flagged bool
}

// Review is the last imported review; Blocking are its unresolved blocker and major findings.
type Review struct {
	N             int
	Commit        string
	Base          string
	Coverage      string
	CoverageLabel string
	Gaps          []run.Gap
	Counts        review.Counts
	Blocking      []review.Tracked
}

// NewData collects the template data; taskMD is task.md of the run.
func NewData(st *run.State, snap run.Snapshot, rev run.Review, tracked []review.Tracked,
	taskMD []byte, checks []review.Check, in Input, stale bool,
) Data {
	r := review.NewReport(st, snap, rev, tracked, "", stale)
	key := ""
	if st.Task != nil {
		key = st.Task.Key
	}
	title, items := task.Read(taskMD)
	summary := title
	if key != "" {
		summary = strings.TrimPrefix(title, key+": ")
		title = key + ": " + summary
	}
	var blocking []review.Tracked
	for _, f := range r.Findings {
		if f.Carried() && (f.Severity == "blocker" || f.Severity == "major") {
			blocking = append(blocking, f)
		}
	}
	criteria := make([]Criterion, 0, len(items))
	for _, it := range items {
		flagged := slices.ContainsFunc(r.Findings, func(f review.Tracked) bool {
			return f.Carried() && quotes(f.Criterion, it)
		})
		criteria = append(criteria, Criterion{Text: it, Flagged: flagged})
	}
	return Data{
		Title: title, Key: key, Task: summary, Summary: strings.TrimSpace(in.Summary), Criteria: criteria,
		Review: Review{
			N: r.Snapshot, Commit: r.Commit, Base: r.Base, Coverage: r.Coverage, CoverageLabel: r.CoverageLabel,
			Gaps: r.CoverageGaps, Counts: r.Counts, Blocking: blocking,
		},
		Checks: checks, Stale: stale, Labels: []string{Label}, Version: in.Version,
	}
}

// quotes matches a finding's quote of a criterion loosely: the reviewer may quote a part or reflow it.
func quotes(quote, criterion string) bool {
	norm := func(s string) string { return strings.ToLower(strings.Join(strings.Fields(s), " ")) }
	q, c := norm(quote), norm(criterion)
	return q != "" && c != "" && (strings.Contains(c, q) || strings.Contains(q, c))
}

// Template parses the built-in template, or the repository file name when it is set.
func Template(root, name string) (*template.Template, error) {
	if name == "" {
		t, err := parse("mr.md", builtin)
		if err != nil {
			return nil, output.Internal(fmt.Errorf("parse mr template: %w", err))
		}
		return t, nil
	}
	if !filepath.IsLocal(name) {
		return nil, output.Invalid("mr_template_outside_repo",
			fmt.Sprintf("mr.template %q is not a path inside the repository", name),
			"set mr.template in .plait/config.yaml to a path relative to the repository root, "+
				"or to \"\" for the built-in one")
	}
	r, err := os.OpenRoot(root)
	if err != nil {
		return nil, output.Internal(fmt.Errorf("open repository root: %w", err))
	}
	defer func() { _ = r.Close() }()
	b, err := r.ReadFile(filepath.Clean(name))
	if err != nil {
		return nil, templateErr(name, err)
	}
	t, err := parse(name, string(b))
	if err != nil {
		return nil, templateErr(name, err)
	}
	return t, nil
}

func parse(name, text string) (*template.Template, error) {
	return template.New(name).Funcs(template.FuncMap{"join": strings.Join}).Parse(text)
}

func templateErr(name string, err error) error {
	return output.Invalid(
		"mr_template_invalid",
		fmt.Sprintf("mr.template %s: %v", name, err),
		"fix "+name+" or set mr.template to \"\" in .plait/config.yaml for the built-in template, then plait mr describe",
	)
}

// Render fills the description body; custom names the template file in errors, empty for the built-in one.
func Render(t *template.Template, d Data, custom string) (string, error) {
	var b bytes.Buffer
	if err := t.Execute(&b, d); err != nil {
		if custom != "" {
			return "", templateErr(custom, err)
		}
		return "", output.Internal(fmt.Errorf("render mr description: %w", err))
	}
	return b.String(), nil
}

// Describe writes mr.md of the current run from its last imported review and moves the run to described.
func Describe(ctx context.Context, s *run.Store, repo vcs.Repo, cfg config.Config, in Input) (Result, error) {
	tmpl, err := Template(repo.Root, cfg.MR.Template)
	if err != nil {
		return Result{}, err
	}
	unlock, err := s.Lock()
	if err != nil {
		return Result{}, err
	}
	defer unlock()
	st, err := s.Current()
	if err != nil {
		return Result{}, err
	}
	snap, rev, err := reviewed(st)
	if err != nil {
		return Result{}, err
	}
	runDir := s.RunDir(st.ID)
	tf, err := review.LoadTracked(runDir, rev.N)
	if err != nil {
		return Result{}, err
	}
	if tf.Snapshot != snap.Commit {
		tp := filepath.ToSlash(filepath.Join(".plait", "runs", st.ID, "findings-"+strconv.Itoa(rev.N)+".json"))
		return Result{}, output.NeedsHuman("findings_corrupt",
			fmt.Sprintf("%s compares snapshot %s, not snapshot %d %s", tp, tf.Snapshot, snap.N, snap.Commit),
			"stop and ask a human; to recover they remove "+tp+" and call plait review import again")
	}
	tree, err := repo.CurrentTree(ctx)
	if err != nil {
		return Result{}, err
	}
	stale := tree != snap.Tree
	if stale && !in.AllowStale {
		return Result{}, output.Invalid(
			"snapshot_stale",
			fmt.Sprintf("the working copy changed after the reviewed snapshot %d of run %s", snap.N, st.ID),
			"review the current code: "+reviewFix+"; or describe the reviewed snapshot anyway: plait mr describe --allow-stale",
		)
	}
	taskMD, err := os.ReadFile(filepath.Join(runDir, task.FileName))
	if errors.Is(err, fs.ErrNotExist) {
		return Result{}, run.BrokenState(st, "step "+string(st.Step)+" without task.md")
	}
	if err != nil {
		return Result{}, output.Internal(fmt.Errorf("read task: %w", err))
	}
	saved, err := check.List(runDir)
	if err != nil {
		return Result{}, err
	}
	d := NewData(st, snap, rev, tf.Findings, taskMD, review.Checks(cfg.Review.Checks, saved, snap.Tree), in, stale)
	body, err := Render(tmpl, d, cfg.MR.Template)
	if err != nil {
		return Result{}, err
	}
	path := filepath.Join(runDir, FileName)
	changed, err := write(path, []byte("# "+d.Title+"\n\n"+body))
	if err != nil {
		return Result{}, err
	}
	if st.Step != run.StepDescribed {
		st.Step, changed = run.StepDescribed, true
		if err = s.Save(st); err != nil {
			return Result{}, err
		}
	}
	if rel, relErr := filepath.Rel(repo.Root, path); relErr == nil {
		path = rel
	}
	return Result{
		Run: st.ID, Snapshot: snap.N, Title: d.Title, Body: body, Labels: d.Labels, Path: filepath.ToSlash(path),
		Stale: stale, Changed: changed, Step: st.Step,
	}, nil
}

// reviewed returns the last imported review and its snapshot, when no newer task or snapshot awaits review.
func reviewed(st *run.State) (run.Snapshot, run.Review, error) {
	if !slices.Contains([]run.Step{run.StepImported, run.StepReported, run.StepDescribed}, st.Step) {
		msg, fix := fmt.Sprintf("run %s has no imported review of its current task", st.ID), reviewFix
		if st.Step == run.StepExported && len(st.Reviews) > 0 {
			msg = fmt.Sprintf("the last snapshot of run %s is not reviewed yet", st.ID)
			fix = "review the exported package.md in a fresh session, then plait review import <file>"
		}
		return run.Snapshot{}, run.Review{}, output.Invalid("no_review", msg, fix)
	}
	if len(st.Reviews) == 0 {
		return run.Snapshot{}, run.Review{}, run.BrokenState(st, "step "+string(st.Step)+" without an imported review")
	}
	rev := st.Reviews[len(st.Reviews)-1]
	i := slices.IndexFunc(st.Snapshots, func(sn run.Snapshot) bool { return sn.N == rev.N })
	if i < 0 {
		return run.Snapshot{}, run.Review{}, run.BrokenState(st, "review "+strconv.Itoa(rev.N)+" without its snapshot")
	}
	return st.Snapshots[i], rev, nil
}

// write replaces path only when its content differs.
func write(path string, b []byte) (bool, error) {
	old, err := os.ReadFile(filepath.Clean(path))
	if err == nil && bytes.Equal(old, b) {
		return false, nil
	}
	if err = run.WriteFile(path, b); err != nil {
		return false, output.Internal(err)
	}
	return true, nil
}
