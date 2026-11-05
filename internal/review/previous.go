package review

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/lllypuk/plait/internal/output"
	"github.com/lllypuk/plait/internal/run"
	"github.com/lllypuk/plait/internal/vcs"
)

// Verdicts on a previous finding.
const (
	VerdictFixed      = "fixed"
	VerdictStillOpen  = "still_open"
	VerdictNotChecked = "not_checked"
)

// GapCriteriaUnchecked lists the acceptance criteria the reviewer could not check from the diff.
const GapCriteriaUnchecked = "criteria_unchecked"

// Previous is a carried finding of an earlier review, as that review left it, placed At the new snapshot.
type Previous struct {
	Tracked

	At vcs.Location
}

// Where renders the place of the finding in the new snapshot.
func (p Previous) Where() string {
	switch {
	case p.At.State == vcs.LineGone:
		return "file deleted"
	case p.At.Line == 0 && p.At.State == vcs.LineChanged:
		return "`" + p.At.File + "`, file changed"
	case p.At.Line == 0:
		return "`" + p.At.File + "`, whole file"
	case p.At.State == vcs.LineChanged:
		return "`" + p.At.File + ":" + strconv.Itoa(p.At.Line) + "`, region changed"
	}
	return "`" + p.At.File + ":" + strconv.Itoa(p.At.Line) + "`"
}

// Text is the reviewer's wording of the finding, quoted in the package.
func (p Previous) Text() string {
	s := p.Title + "\n\n" + p.Rationale
	if p.Criterion != "" {
		s += "\n\nCriterion: " + p.Criterion
	}
	return s
}

// Key identifies finding id of review n in later reviews.
func Key(n int, id string) string {
	return "R" + strconv.Itoa(n) + "-" + id
}

// previousFindings places the carried findings of the last review imported before snapshot n in tree.
func previousFindings(
	ctx context.Context, repo vcs.Repo, st *run.State, runDir, tree string, n int,
) ([]Previous, error) {
	last := -1
	for i, r := range st.Reviews {
		if r.N < n && (last < 0 || r.N > st.Reviews[last].N) {
			last = i
		}
	}
	if last < 0 {
		return nil, nil
	}
	m := st.Reviews[last].N
	i := slices.IndexFunc(st.Snapshots, func(s run.Snapshot) bool { return s.N == m })
	if i < 0 {
		return nil, output.Internal(fmt.Errorf("review %d has no snapshot in run %s", m, st.ID))
	}
	f, err := readTracked(trackedPath(runDir, m))
	if err != nil {
		return nil, err
	}
	prev := make([]Previous, 0, len(f.Findings))
	for _, t := range f.Findings {
		if !t.Carried() {
			continue
		}
		at := vcs.Location{State: vcs.LineGone}
		if t.State != vcs.LineGone {
			if at, err = repo.MapLine(ctx, st.Snapshots[i].Tree, tree, t.File, t.Line); err != nil {
				return nil, err
			}
		}
		prev = append(prev, Previous{Tracked: t, At: at})
	}
	return prev, nil
}

// bindVerdicts rejects verdicts on keys the package did not list and returns one verdict per listed key,
// in package order; a key without a verdict is not_checked.
func bindVerdicts(got []Verdict, keys []string, pkg, name string) ([]Verdict, error) {
	byKey := make(map[string]Verdict, len(got))
	for _, v := range got {
		if !slices.Contains(keys, v.Key) {
			listed := "the package lists no previous findings, so drop previous"
			if len(keys) > 0 {
				listed = "give verdicts only for the keys under Previous findings in " + pkg + ": " +
					strings.Join(keys, ", ")
			}
			return nil, output.Invalid("unknown_previous",
				fmt.Sprintf("the answer gives a verdict on %s, which package %s does not list", v.Key, pkg),
				listed+", then call plait review import "+name+" again")
		}
		byKey[v.Key] = v
	}
	if len(keys) == 0 {
		return nil, nil
	}
	out := make([]Verdict, 0, len(keys))
	for _, k := range keys {
		v, ok := byKey[k]
		if !ok {
			v = Verdict{Key: k, Verdict: VerdictNotChecked}
		}
		out = append(out, v)
	}
	return out, nil
}

func previousKeys(prev []Previous) []string {
	keys := make([]string, 0, len(prev))
	for _, p := range prev {
		keys = append(keys, p.Key)
	}
	return keys
}
