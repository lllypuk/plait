package review

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"golang.org/x/text/language"
	"golang.org/x/text/message"

	"github.com/lllypuk/plait/internal/output"
	"github.com/lllypuk/plait/internal/run"
	"github.com/lllypuk/plait/internal/vcs"
)

const (
	schemaID         = "https://github.com/lllypuk/plait/schemas/review-findings.v1.json"
	maxSchemaReports = 5
)

var compiledSchema = sync.OnceValues(func() (*jsonschema.Schema, error) { //nolint:gochecknoglobals // compiled once
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(findingsSchema))
	if err != nil {
		return nil, err
	}
	c := jsonschema.NewCompiler()
	if err = c.AddResource(schemaID, doc); err != nil {
		return nil, err
	}
	return c.Compile(schemaID)
})

// Answer is a review result in review-findings/v1.
type Answer struct {
	Snapshot string    `json:"snapshot"`
	Coverage string    `json:"coverage"`
	Findings []Finding `json:"findings"`
}

// Finding is one defect the reviewer reported.
type Finding struct {
	ID        string `json:"id"`
	Severity  string `json:"severity"`
	File      string `json:"file"`
	Line      int    `json:"line"`
	Title     string `json:"title"`
	Rationale string `json:"rationale"`
	Criterion string `json:"criterion,omitempty"`
}

// ImportResult describes what review import did.
type ImportResult struct {
	Run      string   `json:"run"`
	Snapshot int      `json:"snapshot"`
	Commit   string   `json:"commit"`
	Coverage string   `json:"coverage"`
	Findings int      `json:"findings"`
	Review   string   `json:"review"`
	Stale    bool     `json:"stale"`
	Changed  bool     `json:"changed"`
	Step     run.Step `json:"step"`
}

// ParseAnswer validates data against review-findings/v1; name is how the agent passed the input.
func ParseAnswer(data []byte, name string) (Answer, error) {
	retry := "then call plait review import " + name + " again"
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(data))
	if err != nil {
		return Answer{}, output.Invalid("invalid_json", fmt.Sprintf("the answer is not a JSON document: %v", err),
			"save only the JSON object of "+run.FindingsSchema+", without Markdown fences or prose, "+retry)
	}
	sch, err := compiledSchema()
	if err != nil {
		return Answer{}, output.Internal(fmt.Errorf("compile %s: %w", run.FindingsSchema, err))
	}
	if err = sch.Validate(doc); err != nil {
		ve, ok := errors.AsType[*jsonschema.ValidationError](err)
		if !ok {
			return Answer{}, output.Internal(err)
		}
		return Answer{}, schemaError(ve, retry)
	}
	var a Answer
	if err = json.Unmarshal(data, &a); err != nil {
		return Answer{}, output.Invalid("invalid_answer", fmt.Sprintf("the answer does not decode: %v", err),
			"write line as a whole number, "+retry)
	}
	seen := map[string]bool{}
	for i, f := range a.Findings {
		if seen[f.ID] {
			at := "/findings/" + strconv.Itoa(i) + "/id"
			return Answer{}, output.Invalid("schema_violation",
				fmt.Sprintf("the answer does not match %s: at %s: duplicate id %q", run.FindingsSchema, at, f.ID),
				"give every finding a unique id: fix "+at+", "+retry)
		}
		seen[f.ID] = true
	}
	return a, nil
}

// Import binds the answer to the last snapshot of the current run and saves it as review-<n>.json.
func Import(ctx context.Context, s *run.Store, repo vcs.Repo, data []byte, name string) (ImportResult, error) {
	unlock, err := s.Lock()
	if err != nil {
		return ImportResult{}, err
	}
	defer unlock()
	st, err := s.Current()
	if err != nil {
		return ImportResult{}, err
	}
	if len(st.Snapshots) == 0 || st.Step == run.StepNew || st.Step == run.StepTaskSaved {
		return ImportResult{}, output.Invalid("not_exported",
			fmt.Sprintf("run %s has no review package for its current task", st.ID),
			"export it with plait review export, review package.md in a fresh session, then import that answer")
	}
	snap := st.Snapshots[len(st.Snapshots)-1]
	runDir := s.RunDir(st.ID)
	pkg := filepath.ToSlash(filepath.Join(".plait", "runs", st.ID, "review-"+strconv.Itoa(snap.N), packageFile))
	a, err := ParseAnswer(data, name)
	if err != nil {
		return ImportResult{}, err
	}
	if a.Snapshot != snap.Commit {
		return ImportResult{}, output.Invalid("snapshot_mismatch",
			fmt.Sprintf("the answer reviews snapshot %s, but the last snapshot %d of run %s is %s",
				a.Snapshot, snap.N, st.ID, snap.Commit),
			"review "+pkg+" in a fresh session and import that answer: its snapshot must be "+snap.Commit)
	}
	if want := coverage(st.Task); a.Coverage != want {
		return ImportResult{}, output.Invalid("coverage_mismatch",
			fmt.Sprintf("the answer claims coverage %s, but package %s asks for %s", a.Coverage, pkg, want),
			"set coverage to "+want+" as package.md says, then call plait review import "+name+" again")
	}
	tree, err := repo.CurrentTree(ctx)
	if err != nil {
		return ImportResult{}, err
	}
	path := filepath.Join(runDir, "review-"+strconv.Itoa(snap.N)+".json")
	changed, err := saveAnswer(s, st, snap, a, path)
	if err != nil {
		return ImportResult{}, err
	}
	if rel, relErr := filepath.Rel(repo.Root, path); relErr == nil {
		path = rel
	}
	return ImportResult{
		Run: st.ID, Snapshot: snap.N, Commit: snap.Commit, Coverage: a.Coverage, Findings: len(a.Findings),
		Review: filepath.ToSlash(path), Stale: tree != snap.Tree, Changed: changed, Step: st.Step,
	}, nil
}

// saveAnswer writes the answer before state.json, so a crash between them is repaired by importing again.
func saveAnswer(s *run.Store, st *run.State, snap run.Snapshot, a Answer, path string) (bool, error) {
	b, err := json.MarshalIndent(a, "", "  ")
	if err != nil {
		return false, output.Internal(fmt.Errorf("encode answer: %w", err))
	}
	b = append(b, '\n')
	i := slices.IndexFunc(st.Reviews, func(r run.Review) bool { return r.N == snap.N })
	old, err := os.ReadFile(filepath.Clean(path))
	switch {
	case err != nil && !errors.Is(err, fs.ErrNotExist):
		return false, output.Internal(fmt.Errorf("read %s: %w", path, err))
	case err == nil && i >= 0 && bytes.Equal(old, b):
		return false, nil
	case err == nil && i >= 0:
		return false, output.NeedsHuman("review_exists",
			fmt.Sprintf("snapshot %d already has a different imported review %s", snap.N, path),
			"stop and ask a human which answer to keep; to replace it they remove "+path+
				" and call plait review import again")
	}
	if err = run.WriteFile(path, b); err != nil {
		return false, output.Internal(err)
	}
	rec := run.Review{N: snap.N, Snapshot: snap.Commit, ImportedAt: s.Now().UTC(), Coverage: a.Coverage}
	if i >= 0 {
		st.Reviews[i] = rec
	} else {
		st.Reviews = append(st.Reviews, rec)
	}
	st.Step = run.StepImported
	if err = s.Save(st); err != nil {
		return false, err
	}
	return true, nil
}

func schemaError(ve *jsonschema.ValidationError, retry string) *output.Error {
	p := message.NewPrinter(language.English)
	type leaf struct{ at, msg string }
	var leaves []leaf
	var walk func(e *jsonschema.ValidationError)
	walk = func(e *jsonschema.ValidationError) {
		if len(e.Causes) == 0 {
			leaves = append(leaves, leaf{pointer(e.InstanceLocation), e.ErrorKind.LocalizedString(p)})
			return
		}
		for _, c := range e.Causes {
			walk(c)
		}
	}
	walk(ve)
	// Causes follow map order inside the validator; sorting keeps the message stable between runs.
	slices.SortFunc(leaves, func(a, b leaf) int { return strings.Compare(a.at+a.msg, b.at+b.msg) })
	msgs := make([]string, 0, maxSchemaReports)
	for _, l := range leaves[:min(len(leaves), maxSchemaReports)] {
		msgs = append(msgs, "at "+l.at+": "+l.msg)
	}
	return output.Invalid("schema_violation",
		fmt.Sprintf("the answer does not match %s: %s", run.FindingsSchema, strings.Join(msgs, "; ")),
		"fix "+leaves[0].at+" as the schema in package.md requires, "+retry)
}

// pointer renders an instance location as a JSON Pointer; the document root is "/".
func pointer(loc []string) string {
	if len(loc) == 0 {
		return "/"
	}
	r := strings.NewReplacer("~", "~0", "/", "~1")
	var b strings.Builder
	for _, s := range loc {
		b.WriteString("/" + r.Replace(s))
	}
	return b.String()
}
