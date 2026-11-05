// Package review builds the review package, imports the answer and reports it.
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
	"strconv"
	"strings"
	"text/template"

	"github.com/lllypuk/plait/internal/config"
	"github.com/lllypuk/plait/internal/output"
	"github.com/lllypuk/plait/internal/run"
	"github.com/lllypuk/plait/internal/task"
	"github.com/lllypuk/plait/internal/vcs"
)

// Coverage labels: what the reviewer checked the diff against.
const (
	CoverageCriteria = "criteria"
	CoverageDiffOnly = "diff_only"
)

const (
	packageFile = "package.md"
	minFence    = 3
	dirPerm     = 0o750
	rulesFix    = "list existing files relative to the repository root in review.rules of .plait/config.yaml"
)

var (
	//go:embed schemas/review-findings.v1.json
	findingsSchema []byte
	//go:embed templates/package.md.tmpl
	packageTmpl string
)

// Rule is a repository rule file quoted in the package.
type Rule struct {
	Path    string
	Content string
}

// Package is the content of package.md.
type Package struct {
	Run      string
	N        int
	Commit   string
	Base     string
	Coverage string
	Task     string
	Rules    []Rule
	Diff     string
}

// ExportResult describes what review export did.
type ExportResult struct {
	Run      string   `json:"run"`
	Snapshot int      `json:"snapshot"`
	Commit   string   `json:"commit"`
	Base     string   `json:"base"`
	Coverage string   `json:"coverage"`
	Package  string   `json:"package"`
	Changed  bool     `json:"changed"`
	Step     run.Step `json:"step"`
}

// Render fills package.md; it depends only on p, so the same input gives the same bytes.
func Render(p Package) ([]byte, error) {
	tmpl, err := template.New("package").Funcs(template.FuncMap{"fence": fence}).Parse(packageTmpl)
	if err != nil {
		return nil, output.Internal(fmt.Errorf("parse package template: %w", err))
	}
	data := struct {
		Package

		Schema     string
		SchemaJSON string
	}{p, run.FindingsSchema, string(findingsSchema)}
	var b bytes.Buffer
	if err = tmpl.Execute(&b, data); err != nil {
		return nil, output.Internal(fmt.Errorf("render package: %w", err))
	}
	return b.Bytes(), nil
}

// LoadRules reads the rule files that exist, in the configured order; a path must stay inside root.
func LoadRules(root string, paths []string) ([]Rule, error) {
	r, err := os.OpenRoot(root)
	if err != nil {
		return nil, output.Internal(fmt.Errorf("open repository root: %w", err))
	}
	defer func() { _ = r.Close() }()
	rules := []Rule{}
	seen := map[string]bool{}
	for _, p := range paths {
		if !filepath.IsLocal(p) {
			return nil, output.Invalid("rule_outside_repo",
				fmt.Sprintf("review rule %q is not a path inside the repository", p), rulesFix)
		}
		p = filepath.ToSlash(filepath.Clean(p))
		if seen[p] {
			continue
		}
		seen[p] = true
		b, readErr := r.ReadFile(p)
		if errors.Is(readErr, fs.ErrNotExist) {
			continue
		}
		if readErr != nil {
			return nil, output.Invalid("rule_unreadable", fmt.Sprintf("review rule %s: %v", p, readErr), rulesFix)
		}
		rules = append(rules, Rule{Path: p, Content: string(b)})
	}
	return rules, nil
}

// Export snapshots the working copy and writes the package; an unchanged working copy, task and rules keep the last snapshot.
func Export(ctx context.Context, s *run.Store, repo vcs.Repo, cfg config.Review) (ExportResult, error) {
	st, err := s.Current()
	if err != nil {
		return ExportResult{}, err
	}
	runDir := s.RunDir(st.ID)
	taskMD, err := os.ReadFile(filepath.Join(runDir, task.FileName))
	if st.Task == nil || st.Step == run.StepNew || errors.Is(err, fs.ErrNotExist) {
		return ExportResult{}, output.Invalid("no_task", fmt.Sprintf("run %s has no saved task", st.ID),
			"save the task with its acceptance criteria first: plait ctx save < task.md")
	}
	if err != nil {
		return ExportResult{}, output.Internal(fmt.Errorf("read task: %w", err))
	}
	rules, err := LoadRules(repo.Root, cfg.Rules)
	if err != nil {
		return ExportResult{}, err
	}
	snap, err := repo.Snapshot(ctx, cfg.Base)
	if err != nil {
		return ExportResult{}, err
	}
	diff, err := repo.Diff(ctx, snap.Base, snap.Commit)
	if err != nil {
		return ExportResult{}, err
	}
	p := Package{
		Run: st.ID, Commit: snap.Commit, Base: snap.Base,
		Coverage: coverage(st.Task), Task: string(taskMD), Rules: rules, Diff: diff,
	}

	n, md, reused, err := chooseSnapshot(st, runDir, p)
	if err != nil {
		return ExportResult{}, err
	}
	if err = repo.SetRef(ctx, vcs.SnapshotRef(st.ID, n), snap.Commit); err != nil {
		return ExportResult{}, err
	}
	path := filepath.Join(runDir, "review-"+strconv.Itoa(n), packageFile)
	if !reused {
		if err = writePackage(path, md); err != nil {
			return ExportResult{}, err
		}
		st.Snapshots = append(st.Snapshots, run.Snapshot{
			N: n, Commit: snap.Commit, Tree: snap.Tree, Base: snap.Base, Head: snap.Head, CreatedAt: s.Now().UTC(),
		})
	}
	if !reused || st.Step == run.StepTaskSaved {
		st.Step = run.StepExported
		if err = s.Save(st); err != nil {
			return ExportResult{}, err
		}
	}
	if rel, relErr := filepath.Rel(repo.Root, path); relErr == nil {
		path = rel
	}
	return ExportResult{
		Run: st.ID, Snapshot: n, Commit: snap.Commit, Base: snap.Base, Coverage: p.Coverage,
		Package: filepath.ToSlash(path), Changed: !reused, Step: st.Step,
	}, nil
}

// chooseSnapshot keeps the last snapshot when its package.md is byte-equal, so a written package never changes.
func chooseSnapshot(st *run.State, runDir string, p Package) (int, []byte, bool, error) {
	p.N = 1
	if len(st.Snapshots) > 0 {
		last := st.Snapshots[len(st.Snapshots)-1]
		p.N = last.N + 1
		if last.Commit == p.Commit && last.Base == p.Base {
			md, same, err := samePackage(runDir, p, last.N)
			if err != nil || same {
				return last.N, md, same, err
			}
		}
	}
	md, err := Render(p)
	return p.N, md, false, err
}

func samePackage(runDir string, p Package, n int) ([]byte, bool, error) {
	p.N = n
	md, err := Render(p)
	if err != nil {
		return nil, false, err
	}
	old, err := os.ReadFile(filepath.Join(runDir, "review-"+strconv.Itoa(n), packageFile))
	return md, err == nil && bytes.Equal(old, md), nil
}

func writePackage(path string, md []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), dirPerm); err != nil {
		return output.Internal(fmt.Errorf("create package dir: %w", err))
	}
	if err := run.WriteFile(path, md); err != nil {
		return output.Internal(err)
	}
	return nil
}

func coverage(t *run.Task) string {
	if t != nil && t.HasCriteria {
		return CoverageCriteria
	}
	return CoverageDiffOnly
}

// fence wraps s in a code fence longer than any backtick run inside it.
func fence(s, lang string) string {
	longest, cur := 0, 0
	for _, r := range s {
		if r == '`' {
			cur++
			longest = max(longest, cur)
		} else {
			cur = 0
		}
	}
	f := strings.Repeat("`", max(minFence, longest+1))
	return f + lang + "\n" + strings.TrimSuffix(s, "\n") + "\n" + f
}
