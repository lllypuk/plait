package review

import (
	"cmp"
	"fmt"
	"path"
	"slices"
	"strings"

	"github.com/lllypuk/plait/internal/output"
	"github.com/lllypuk/plait/internal/run"
	"github.com/lllypuk/plait/internal/vcs"
)

// Coverage gap codes: what the review could not see.
const (
	GapDiffTruncated = "diff_truncated"
	GapFilesExcluded = "files_excluded"
)

const globsFix = "fix the patterns in audit.deny_globs of .plait/config.yaml"

// MatchGlob reports whether a slash path from the repository root matches pattern;
// path.Match applies per segment and "**" spans any number of segments.
func MatchGlob(pattern, name string) bool {
	return matchSegments(strings.Split(strings.Trim(pattern, "/"), "/"), strings.Split(name, "/"))
}

func matchSegments(pat, name []string) bool {
	for len(pat) > 0 {
		if pat[0] == "**" {
			for i := range len(name) + 1 {
				if matchSegments(pat[1:], name[i:]) {
					return true
				}
			}
			return false
		}
		if len(name) == 0 {
			return false
		}
		if ok, err := path.Match(pat[0], name[0]); err != nil || !ok {
			return false
		}
		pat, name = pat[1:], name[1:]
	}
	return len(name) == 0
}

// Denied reports whether name matches any of globs.
func Denied(globs []string, name string) bool {
	return slices.ContainsFunc(globs, func(g string) bool { return MatchGlob(g, name) })
}

// CheckGlobs rejects malformed patterns, which would otherwise match nothing and leak the files they meant to hide.
func CheckGlobs(globs []string) error {
	for _, g := range globs {
		for seg := range strings.SplitSeq(strings.Trim(g, "/"), "/") {
			if _, err := path.Match(seg, ""); err != nil {
				return output.Invalid("config_invalid", fmt.Sprintf("audit.deny_globs: bad pattern %q", g), globsFix)
			}
		}
	}
	return nil
}

// Scope is the part of the snapshot diff that goes into the package.
type Scope struct {
	Diff     string
	Files    []vcs.FileDiff
	Excluded []string
	Dropped  []string
}

// ScopeDiff leaves out files under deny globs, then drops whole files, largest first, until the diff fits maxBytes;
// maxBytes <= 0 means no limit.
func ScopeDiff(files []vcs.FileDiff, deny []string, maxBytes int) Scope {
	sc := Scope{Excluded: []string{}, Dropped: []string{}}
	kept := make([]vcs.FileDiff, 0, len(files))
	total := 0
	for _, f := range files {
		if Denied(deny, f.Path) || (f.OldPath != "" && Denied(deny, f.OldPath)) {
			sc.Excluded = append(sc.Excluded, f.Path)
			continue
		}
		kept = append(kept, f)
		total += len(f.Diff)
	}
	dropped := map[string]bool{}
	if maxBytes > 0 && total > maxBytes {
		bySize := slices.Clone(kept)
		slices.SortStableFunc(bySize, func(a, b vcs.FileDiff) int {
			return cmp.Or(cmp.Compare(len(b.Diff), len(a.Diff)), cmp.Compare(a.Path, b.Path))
		})
		for _, f := range bySize {
			if total <= maxBytes {
				break
			}
			dropped[f.Path] = true
			total -= len(f.Diff)
		}
	}
	var b strings.Builder
	for _, f := range kept {
		if dropped[f.Path] {
			sc.Dropped = append(sc.Dropped, f.Path)
			continue
		}
		sc.Files = append(sc.Files, f)
		b.WriteString(f.Diff)
	}
	sc.Diff = b.String()
	return sc
}

// gaps lists what the package leaves out, in a fixed order of codes.
func gaps(sc Scope, rules []Rule) []run.Gap {
	excluded := slices.Clone(sc.Excluded)
	for _, r := range rules {
		if r.Excluded && !slices.Contains(excluded, r.Path) {
			excluded = append(excluded, r.Path)
		}
	}
	out := []run.Gap{}
	if len(sc.Dropped) > 0 {
		out = append(out, run.Gap{Code: GapDiffTruncated, Items: sc.Dropped})
	}
	if len(excluded) > 0 {
		out = append(out, run.Gap{Code: GapFilesExcluded, Items: excluded})
	}
	return out
}
