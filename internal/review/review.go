// Package review builds the review package, imports the answer and reports it.
package review

import (
	"bytes"
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"text/template"

	"github.com/lllypuk/plait/internal/audit"
	"github.com/lllypuk/plait/internal/check"
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

// Rule is a repository rule file quoted in the package; an Excluded rule matched audit.deny_globs and was not read.
type Rule struct {
	Path     string
	Content  string
	Excluded bool
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
	// Excluded diff files matched audit.deny_globs; Dropped ones did not fit MaxDiffBytes.
	Excluded     []string
	Dropped      []string
	MaxDiffBytes int
	Checks       []Check
	Gaps         []run.Gap
	Previous     []Previous
	files        []vcs.FileDiff
}

// ExportResult describes what review export did.
type ExportResult struct {
	Run      string    `json:"run"`
	Snapshot int       `json:"snapshot"`
	Commit   string    `json:"commit"`
	Base     string    `json:"base"`
	Coverage string    `json:"coverage"`
	Package  string    `json:"package"`
	Changed  bool      `json:"changed"`
	Step     run.Step  `json:"step"`
	Gaps     []run.Gap `json:"coverage_gaps"`
	// PackageSHA256, Context and Excluded go to the audit log, not to the agent.
	PackageSHA256 string           `json:"-"`
	Context       []audit.File     `json:"-"`
	Excluded      []audit.Excluded `json:"-"`
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
// A file under deny globs is listed as excluded and never read.
func LoadRules(root string, paths, deny []string) ([]Rule, error) {
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
		if Denied(deny, p) {
			if _, statErr := r.Lstat(p); statErr == nil {
				rules = append(rules, Rule{Path: p, Excluded: true})
			}
			continue
		}
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
func Export(ctx context.Context, s *run.Store, repo vcs.Repo, cfg config.Config) (ExportResult, error) {
	if err := CheckGlobs(cfg.Audit.DenyGlobs); err != nil {
		return ExportResult{}, err
	}
	unlock, err := s.Lock()
	if err != nil {
		return ExportResult{}, err
	}
	defer unlock()
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
	p, snap, err := buildPackage(ctx, repo, cfg, runDir)
	if err != nil {
		return ExportResult{}, err
	}
	p.Run, p.Coverage, p.Task = st.ID, coverage(st.Task), string(taskMD)
	if p.Previous, err = previousFindings(ctx, repo, st, runDir, snap.Tree, math.MaxInt); err != nil {
		return ExportResult{}, err
	}
	if snap.Commit, err = commitPackage(ctx, repo, snap, p); err != nil {
		return ExportResult{}, err
	}
	p.Commit = snap.Commit

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
			CoverageGaps: p.Gaps, Previous: previousKeys(p.Previous),
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
	res := ExportResult{
		Run: st.ID, Snapshot: n, Commit: snap.Commit, Base: snap.Base, Coverage: p.Coverage, Gaps: p.Gaps,
		Package: filepath.ToSlash(path), Changed: !reused, Step: st.Step, PackageSHA256: audit.SHA256(md),
	}
	taskPath := filepath.ToSlash(filepath.Join(".plait", "runs", st.ID, task.FileName))
	res.Context, res.Excluded = auditLists(taskPath, taskMD, p)
	return res, nil
}

// auditLists names what the package quoted, by hash, and what it left out.
func auditLists(taskPath string, taskMD []byte, p Package) ([]audit.File, []audit.Excluded) {
	read := []audit.File{{Path: taskPath, SHA256: audit.SHA256(taskMD)}}
	left := []audit.Excluded{}
	for _, r := range p.Rules {
		if r.Excluded {
			left = append(left, audit.Excluded{Path: r.Path, Reason: audit.ReasonDenyGlob})
			continue
		}
		read = append(read, audit.File{Path: r.Path, SHA256: audit.SHA256([]byte(r.Content))})
	}
	for _, f := range p.files {
		read = append(read, audit.File{Path: f.Path, SHA256: audit.SHA256([]byte(f.Diff))})
	}
	for _, path := range p.Excluded {
		left = append(left, audit.Excluded{Path: path, Reason: audit.ReasonDenyGlob})
	}
	for _, path := range p.Dropped {
		left = append(left, audit.Excluded{Path: path, Reason: audit.ReasonDiffLimit})
	}
	return read, left
}

// buildPackage snapshots the working copy and collects the rules, diff and checks of the package.
func buildPackage(ctx context.Context, repo vcs.Repo, cfg config.Config, runDir string) (Package, vcs.Snapshot, error) {
	rules, err := LoadRules(repo.Root, cfg.Review.Rules, cfg.Audit.DenyGlobs)
	if err != nil {
		return Package{}, vcs.Snapshot{}, err
	}
	snap, err := repo.Snapshot(ctx, cfg.Review.Base)
	if err != nil {
		return Package{}, vcs.Snapshot{}, err
	}
	files, err := repo.DiffFiles(ctx, snap.Base, snap.Tree)
	if err != nil {
		return Package{}, vcs.Snapshot{}, err
	}
	saved, err := check.List(runDir)
	if err != nil {
		return Package{}, vcs.Snapshot{}, err
	}
	sc := ScopeDiff(files, cfg.Audit.DenyGlobs, cfg.Review.MaxDiffBytes)
	checks := Checks(cfg.Review.Checks, saved, snap.Tree)
	return Package{
		Base: snap.Base, Rules: rules, Diff: sc.Diff, Excluded: sc.Excluded, Dropped: sc.Dropped,
		MaxDiffBytes: cfg.Review.MaxDiffBytes, Checks: checks, Gaps: append(gaps(sc, rules), checkGaps(checks)...),
		files: sc.Files,
	}, snap, nil
}

// commitPackage stamps the snapshot commit with a digest of the package, so the commit id an answer names
// tells apart packages of one tree with another task, rules or template.
func commitPackage(ctx context.Context, repo vcs.Repo, snap vcs.Snapshot, p Package) (string, error) {
	draft, err := Render(p)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(draft)
	return repo.Commit(ctx, snap, "package "+hex.EncodeToString(sum[:]))
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
