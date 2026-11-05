package review

import (
	"slices"

	"github.com/lllypuk/plait/internal/check"
	"github.com/lllypuk/plait/internal/run"
)

// Check states: fresh was saved for the snapshot tree, stale for another one, missing was expected but never saved.
const (
	CheckFresh   = "fresh"
	CheckStale   = "stale"
	CheckMissing = "missing"
)

// Coverage gap codes for checks.
const (
	GapChecksMissing = "checks_missing"
	GapChecksFailed  = "checks_failed"
	GapChecksStale   = "checks_stale"
)

// Check is a check result as the package shows it; Output is set only for a fresh one.
type Check struct {
	Name     string
	State    string
	ExitCode int
	Output   string
}

// Checks lists the expected checks in config order, then the other saved ones by name.
func Checks(expected []string, saved []check.Record, tree string) []Check {
	byName := map[string]check.Record{}
	for _, r := range saved {
		byName[r.Name] = r
	}
	out := []Check{}
	add := func(name string) {
		if slices.ContainsFunc(out, func(c Check) bool { return c.Name == name }) {
			return
		}
		r, ok := byName[name]
		switch {
		case !ok:
			out = append(out, Check{Name: name, State: CheckMissing})
		case r.Tree == tree:
			out = append(out, Check{Name: name, State: CheckFresh, ExitCode: r.ExitCode, Output: r.OutputTail})
		default:
			out = append(out, Check{Name: name, State: CheckStale, ExitCode: r.ExitCode})
		}
	}
	for _, name := range expected {
		add(name)
	}
	for _, r := range saved {
		add(r.Name)
	}
	return out
}

// checkGaps reports missing, failed and stale checks; a stale failure says nothing about the snapshot, so it is only stale.
func checkGaps(checks []Check) []run.Gap {
	var missing, failed, stale []string
	for _, c := range checks {
		switch {
		case c.State == CheckMissing:
			missing = append(missing, c.Name)
		case c.State == CheckStale:
			stale = append(stale, c.Name)
		case c.ExitCode != 0:
			failed = append(failed, c.Name)
		}
	}
	out := []run.Gap{}
	for _, g := range []run.Gap{
		{Code: GapChecksMissing, Items: missing},
		{Code: GapChecksFailed, Items: failed},
		{Code: GapChecksStale, Items: stale},
	} {
		if len(g.Items) > 0 {
			out = append(out, g)
		}
	}
	return out
}
