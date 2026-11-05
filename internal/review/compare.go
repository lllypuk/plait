package review

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/lllypuk/plait/internal/output"
	"github.com/lllypuk/plait/internal/run"
	"github.com/lllypuk/plait/internal/vcs"
)

// TrackedSchema names the format of findings-<n>.json.
const TrackedSchema = "plait-findings/v1"

// Statuses of a finding in a review.
const (
	StatusNew           = "new"
	StatusFixConfirmed  = "fix_confirmed"
	StatusOpen          = "open"
	StatusRegionChanged = "region_changed"
	StatusOutdated      = "outdated"
)

// Tracked is a finding with its status in one review; for a gone line File and Line are its last known place.
type Tracked struct {
	Key       string        `json:"key"`
	Severity  string        `json:"severity"`
	File      string        `json:"file"`
	Line      int           `json:"line"`
	State     vcs.LineState `json:"state"`
	Title     string        `json:"title"`
	Rationale string        `json:"rationale"`
	Criterion string        `json:"criterion,omitempty"`
	Status    string        `json:"status"`
	History   []Event       `json:"history"`
}

// Event is the status a finding got in one review; Duplicate is the key of the merged new finding.
type Event struct {
	Review    int    `json:"review"`
	Status    string `json:"status"`
	Verdict   string `json:"verdict,omitempty"`
	Note      string `json:"note,omitempty"`
	Duplicate string `json:"duplicate,omitempty"`
}

// TrackedFile is findings-<n>.json.
type TrackedFile struct {
	Schema   string    `json:"schema"`
	Run      string    `json:"run"`
	Review   int       `json:"review"`
	Snapshot string    `json:"snapshot"`
	Findings []Tracked `json:"findings"`
}

// Carried reports whether the finding goes to the next review for a verdict.
func (t Tracked) Carried() bool { return t.Status != StatusFixConfirmed }

// Compare sets the status of every finding in review n: prev are the carried findings placed in snapshot n.
// A new finding at the place and with the title of a carried one is merged into it.
func Compare(n int, prev []Previous, verdicts []Verdict, own []Finding) []Tracked {
	byKey := make(map[string]Verdict, len(verdicts))
	for _, v := range verdicts {
		byKey[v.Key] = v
	}
	out := make([]Tracked, 0, len(prev)+len(own))
	for _, p := range prev {
		v := byKey[p.Key]
		t := p.Tracked
		if p.At.State != vcs.LineGone {
			t.File, t.Line = p.At.File, p.At.Line
		}
		t.State, t.Status = p.At.State, status(v.Verdict, p.At.State)
		t.History = append(append([]Event(nil), p.History...),
			Event{Review: n, Status: t.Status, Verdict: v.Verdict, Note: v.Note})
		out = append(out, t)
	}
	carried := len(out)
	for _, f := range own {
		key := Key(n, f.ID)
		if i := duplicateOf(out[:carried], f); i >= 0 {
			ev := &out[i].History[len(out[i].History)-1]
			ev.Duplicate = key
			out[i].Status, ev.Status = StatusOpen, StatusOpen
			continue
		}
		out = append(out, Tracked{
			Key: key, Severity: f.Severity, File: f.File, Line: f.Line, State: vcs.LineUnchanged,
			Title: f.Title, Rationale: f.Rationale, Criterion: f.Criterion, Status: StatusNew,
			History: []Event{{Review: n, Status: StatusNew}},
		})
	}
	return out
}

func status(verdict string, state vcs.LineState) string {
	switch {
	case verdict == VerdictFixed:
		return StatusFixConfirmed
	case verdict == VerdictStillOpen:
		return StatusOpen
	case state == vcs.LineChanged:
		return StatusRegionChanged
	case state == vcs.LineGone:
		return StatusOutdated
	}
	return StatusOpen
}

func duplicateOf(carried []Tracked, f Finding) int {
	for i, t := range carried {
		if t.State != vcs.LineGone && t.File == f.File && t.Line == f.Line && strings.EqualFold(t.Title, f.Title) {
			return i
		}
	}
	return -1
}

func trackedPath(runDir string, n int) string {
	return filepath.Join(runDir, "findings-"+strconv.Itoa(n)+".json")
}

func encodeTracked(st *run.State, snap run.Snapshot, tracked []Tracked) ([]byte, error) {
	b, err := json.MarshalIndent(TrackedFile{
		Schema: TrackedSchema, Run: st.ID, Review: snap.N, Snapshot: snap.Commit, Findings: tracked,
	}, "", "  ")
	if err != nil {
		return nil, output.Internal(fmt.Errorf("encode findings: %w", err))
	}
	return append(b, '\n'), nil
}

// writeTracked replaces path only when its content differs, so importing the same answer leaves the file alone.
func writeTracked(path string, b []byte) error {
	old, err := os.ReadFile(filepath.Clean(path))
	if err == nil && bytes.Equal(old, b) {
		return nil
	}
	if err = run.WriteFile(path, b); err != nil {
		return output.Internal(err)
	}
	return nil
}

// LoadTracked reads findings-<n>.json of a run directory.
func LoadTracked(runDir string, n int) (TrackedFile, error) {
	return readTracked(trackedPath(runDir, n))
}

func readTracked(path string) (TrackedFile, error) {
	b, err := os.ReadFile(filepath.Clean(path))
	if errors.Is(err, fs.ErrNotExist) {
		return TrackedFile{}, output.NeedsHuman("findings_missing", "compared findings "+path+" are missing",
			"stop and ask a human to restore "+path+" or import that review again")
	}
	if err != nil {
		return TrackedFile{}, output.Internal(fmt.Errorf("read %s: %w", path, err))
	}
	var f TrackedFile
	if err = json.Unmarshal(b, &f); err == nil && f.Schema != TrackedSchema {
		err = fmt.Errorf("schema is %q", f.Schema)
	}
	if err != nil {
		return TrackedFile{}, output.NeedsHuman("findings_corrupt",
			fmt.Sprintf("%s is not a %s document: %v", path, TrackedSchema, err),
			"stop and ask a human to repair or remove "+path)
	}
	return f, nil
}
