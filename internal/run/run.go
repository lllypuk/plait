// Package run stores plait runs under .plait/runs and tracks the current one.
package run

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/lllypuk/plait/internal/output"
)

// StateSchema versions state.json.
const StateSchema = "plait-state/v1"

const (
	stateFile   = "state.json"
	currentFile = "current"
	dirPerm     = 0o750
	maxSlugLen  = 60
)

const newRunFix = "plait run new <slug>"

var (
	slugRe = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)
	keyRe  = regexp.MustCompile(`^[A-Z][A-Z0-9]*-[0-9]+$`)
	idRe   = regexp.MustCompile(`^[A-Za-z0-9]+(-[A-Za-z0-9]+)*$`)
)

// Step is the position of a run in the review slice.
type Step string

// Steps in order.
const (
	StepNew       Step = "new"
	StepTaskSaved Step = "task_saved"
	StepExported  Step = "exported"
	StepImported  Step = "imported"
	StepReported  Step = "reported"
	StepDescribed Step = "described"
)

// State is the content of state.json.
type State struct {
	Schema    string     `json:"schema"`
	ID        string     `json:"id"`
	CreatedAt time.Time  `json:"created_at"`
	Task      *Task      `json:"task,omitempty"`
	Snapshots []Snapshot `json:"snapshots"`
	Reviews   []Review   `json:"reviews"`
	Step      Step       `json:"step"`
}

// Task describes the saved task.md.
type Task struct {
	Source      string `json:"source"`
	Key         string `json:"key,omitempty"`
	HasCriteria bool   `json:"has_criteria"`
}

// Snapshot is a working-copy snapshot under refs/plait/snapshots.
type Snapshot struct {
	N            int       `json:"n"`
	Commit       string    `json:"commit"`
	Tree         string    `json:"tree"`
	Base         string    `json:"base"`
	Head         string    `json:"head"`
	CreatedAt    time.Time `json:"created_at"`
	CoverageGaps []Gap     `json:"coverage_gaps,omitempty"`
	// Previous are the keys of earlier findings the package asks the reviewer to recheck.
	Previous []string `json:"previous,omitempty"`
}

// Gap is a part of the change the review could not see: Code names the kind, Items the paths or names.
type Gap struct {
	Code  string   `json:"code"`
	Items []string `json:"items"`
}

// Review is an imported review result.
type Review struct {
	N            int       `json:"n"`
	Snapshot     string    `json:"snapshot"`
	ImportedAt   time.Time `json:"imported_at"`
	Coverage     string    `json:"coverage"`
	CoverageGaps []Gap     `json:"coverage_gaps,omitempty"`
	// Blocking is the number of unresolved blocker and major findings after the comparison.
	Blocking int `json:"blocking"`
}

// Summary is one line of run list.
type Summary struct {
	ID        string    `json:"id"`
	Step      Step      `json:"step"`
	CreatedAt time.Time `json:"created_at"`
	Current   bool      `json:"current"`
}

// Store is the .plait directory of one repository.
type Store struct {
	Dir string
	Now func() time.Time
}

// Open returns the store of the repository at root.
func Open(root string) *Store {
	return &Store{Dir: filepath.Join(root, ".plait"), Now: time.Now}
}

// New creates a run and makes it current; the same slug and key on the same day return the existing run.
func (s *Store) New(slug, key string) (*State, bool, error) {
	id, err := s.newID(slug, key)
	if err != nil {
		return nil, false, err
	}
	st, err := s.read(id)
	created := errors.Is(err, fs.ErrNotExist)
	switch {
	case created:
		st = &State{
			Schema:    StateSchema,
			ID:        id,
			CreatedAt: s.Now().UTC(),
			Snapshots: []Snapshot{},
			Reviews:   []Review{},
			Step:      StepNew,
		}
		if err = s.Save(st); err != nil {
			return nil, false, err
		}
	case err != nil:
		return nil, false, err
	}
	if err = s.setCurrent(id); err != nil {
		return nil, false, err
	}
	return st, created, nil
}

// Resume makes an existing run current.
func (s *Store) Resume(id string) (*State, error) {
	st, err := s.Load(id)
	if err != nil {
		return nil, err
	}
	if err = s.setCurrent(id); err != nil {
		return nil, err
	}
	return st, nil
}

// Load reads the state of run id.
func (s *Store) Load(id string) (*State, error) {
	st, err := s.read(id)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, output.Invalid("run_not_found", fmt.Sprintf("run %q does not exist", id),
			"see existing runs with plait run list, then plait run resume <id> or "+newRunFix)
	}
	return st, err
}

// Current loads the current run.
func (s *Store) Current() (*State, error) {
	id, err := s.currentID()
	if err != nil {
		return nil, err
	}
	if id == "" {
		return nil, output.Invalid("no_current_run", "no current run", "start one with "+newRunFix)
	}
	return s.Load(id)
}

// List returns all runs sorted by id.
func (s *Store) List() ([]Summary, error) {
	entries, err := os.ReadDir(s.runsDir())
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, output.Internal(fmt.Errorf("list runs: %w", err))
	}
	current, err := s.currentID()
	if err != nil {
		return nil, err
	}
	runs := []Summary{}
	for _, e := range entries {
		if !e.IsDir() || !idRe.MatchString(e.Name()) {
			continue
		}
		st, readErr := s.read(e.Name())
		if errors.Is(readErr, fs.ErrNotExist) {
			continue
		}
		if readErr != nil {
			return nil, readErr
		}
		runs = append(runs, Summary{ID: st.ID, Step: st.Step, CreatedAt: st.CreatedAt, Current: st.ID == current})
	}
	slices.SortFunc(runs, func(a, b Summary) int { return strings.Compare(a.ID, b.ID) })
	return runs, nil
}

// Save writes state.json atomically.
func (s *Store) Save(st *State) error {
	dir := s.RunDir(st.ID)
	if err := os.MkdirAll(dir, dirPerm); err != nil {
		return output.Internal(fmt.Errorf("create run dir: %w", err))
	}
	b, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return output.Internal(fmt.Errorf("encode state: %w", err))
	}
	if err = WriteFile(filepath.Join(dir, stateFile), append(b, '\n')); err != nil {
		return output.Internal(err)
	}
	return nil
}

// RunDir is the artifact directory of run id.
func (s *Store) RunDir(id string) string {
	return filepath.Join(s.runsDir(), id)
}

func (s *Store) runsDir() string {
	return filepath.Join(s.Dir, "runs")
}

func (s *Store) newID(slug, key string) (string, error) {
	if len(slug) > maxSlugLen || !slugRe.MatchString(slug) {
		return "", output.Invalid(
			"invalid_slug",
			fmt.Sprintf(
				"slug %q must be lowercase letters, digits and single hyphens, up to %d chars",
				slug,
				maxSlugLen,
			),
			"plait run new <slug>, e.g. plait run new order-retry",
		)
	}
	if key == "" {
		return s.Now().Format(time.DateOnly) + "-" + slug, nil
	}
	if !ValidKey(key) {
		return "", output.Invalid("invalid_key", fmt.Sprintf("task key %q must look like PROJ-123", key),
			"plait run new <slug> --key PROJ-123, or drop --key")
	}
	return key + "-" + slug, nil
}

// ValidKey reports whether key looks like a tracker key such as PROJ-123.
func ValidKey(key string) bool {
	return keyRe.MatchString(key)
}

// read returns an error wrapping fs.ErrNotExist when run id has no state.json.
func (s *Store) read(id string) (*State, error) {
	if !idRe.MatchString(id) {
		return nil, fmt.Errorf("run id %q: %w", id, fs.ErrNotExist)
	}
	path := filepath.Join(s.RunDir(id), stateFile)
	b, err := os.ReadFile(filepath.Clean(path))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	if err != nil {
		return nil, output.Internal(fmt.Errorf("read %s: %w", path, err))
	}
	var st State
	if err = json.Unmarshal(b, &st); err != nil {
		return nil, output.NeedsHuman("state_corrupt", fmt.Sprintf("%s is not valid JSON: %v", path, err),
			"stop and ask a human to repair or remove "+path)
	}
	if st.Schema != StateSchema || st.ID != id {
		return nil, output.NeedsHuman("state_corrupt",
			fmt.Sprintf("%s has schema %q and id %q, want %s and %s", path, st.Schema, st.ID, StateSchema, id),
			"stop and ask a human to repair or remove "+path)
	}
	return &st, nil
}

func (s *Store) currentID() (string, error) {
	b, err := os.ReadFile(filepath.Join(s.Dir, currentFile))
	if errors.Is(err, fs.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", output.Internal(fmt.Errorf("read current run: %w", err))
	}
	return strings.TrimSpace(string(b)), nil
}

func (s *Store) setCurrent(id string) error {
	if err := os.MkdirAll(s.Dir, dirPerm); err != nil {
		return output.Internal(fmt.Errorf("create %s: %w", s.Dir, err))
	}
	if err := WriteFile(filepath.Join(s.Dir, currentFile), []byte(id+"\n")); err != nil {
		return output.Internal(err)
	}
	return nil
}
