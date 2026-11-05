// Package vcs snapshots the working copy into git commits under refs/plait.
package vcs

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/lllypuk/plait/internal/output"
)

const (
	plaitDir      = ".plait"
	snapshotsRef  = "refs/plait/snapshots"
	snapshotMsg   = "plait snapshot"
	identityName  = "plait"
	identityEmail = "plait@localhost"
	numstatFields = 3
	baseFix       = "pass --base <branch> to plait review export or set review.base in .plait/config.yaml"
)

// hunkHeader matches "@@ -a[,b] +c[,d] @@"; an omitted count is 1.
var hunkHeader = regexp.MustCompile(`^@@ -(\d+)(?:,(\d+))? \+(\d+)(?:,(\d+))? @@`)

// exactPathspecs match only the selected files: a bare pathspec also matches a same-named directory on the other
// side of the diff, so the other changed files under a selected path are excluded one by one.
func exactPathspecs(selected, changed []string) []string {
	var specs []string
	for _, p := range selected {
		specs = append(specs, ":(literal)"+p)
	}
	for _, c := range changed {
		if slices.Contains(selected, c) {
			continue
		}
		for _, p := range selected {
			if strings.HasPrefix(c, p+"/") {
				specs = append(specs, ":(exclude,literal)"+c)
				break
			}
		}
	}
	return specs
}

// Repo is a git repository rooted at Root.
type Repo struct {
	Root string
}

// Snapshot is the working copy on top of Head; Base is the merge-base the diff starts from.
type Snapshot struct {
	Commit string
	Tree   string
	Base   string
	Head   string
}

// SnapshotRef names the ref of snapshot n of run id.
func SnapshotRef(id string, n int) string {
	return snapshotsRef + "/" + id + "/" + strconv.Itoa(n)
}

// ResolveBase returns merge-base of HEAD and the configured base, or of the first of origin/HEAD, main, master related to HEAD.
func (r Repo) ResolveBase(ctx context.Context, configured string) (string, error) {
	if _, err := r.head(ctx); err != nil {
		return "", err
	}
	candidates := []string{"origin/HEAD", "main", "master"}
	if configured != "" {
		candidates = []string{configured}
	}
	var unrelated error
	for _, c := range candidates {
		if strings.HasPrefix(c, "-") || !r.exists(ctx, c) {
			continue
		}
		mb, err := r.line(ctx, nil, "merge-base", "HEAD", c)
		if err == nil {
			return mb, nil
		}
		if unrelated == nil {
			unrelated = output.Invalid("no_merge_base",
				fmt.Sprintf("HEAD and base %q have no common ancestor", c), baseFix)
		}
	}
	if unrelated != nil {
		return "", unrelated
	}
	if configured != "" {
		return "", output.Invalid("base_not_found", fmt.Sprintf("review base %q not found", configured), baseFix)
	}
	return "", output.Invalid("base_not_found", "none of origin/HEAD, main, master exists", baseFix)
}

// CurrentTree writes the working copy, untracked files included, to a tree without touching the developer's index.
func (r Repo) CurrentTree(ctx context.Context) (string, error) {
	if _, err := r.head(ctx); err != nil {
		return "", err
	}
	dir, err := os.MkdirTemp("", "plait-index-")
	if err != nil {
		return "", output.Internal(err)
	}
	defer func() { _ = os.RemoveAll(dir) }()
	env := []string{"GIT_INDEX_FILE=" + filepath.Join(dir, "index")}
	if _, err = r.git(ctx, env, "read-tree", "HEAD"); err != nil {
		return "", err
	}
	if _, err = r.git(ctx, env, "add", "-A", "--", ".", ":(exclude)"+plaitDir); err != nil {
		return "", err
	}
	return r.line(ctx, env, "write-tree")
}

// Snapshot writes the working copy as a tree on top of HEAD; Commit turns it into a commit.
func (r Repo) Snapshot(ctx context.Context, base string) (Snapshot, error) {
	mb, err := r.ResolveBase(ctx, base)
	if err != nil {
		return Snapshot{}, err
	}
	head, err := r.head(ctx)
	if err != nil {
		return Snapshot{}, err
	}
	tree, err := r.CurrentTree(ctx)
	if err != nil {
		return Snapshot{}, err
	}
	baseTree, err := r.line(ctx, nil, "rev-parse", mb+"^{tree}")
	if err != nil {
		return Snapshot{}, err
	}
	if tree == baseTree {
		return Snapshot{}, output.Invalid("empty_diff", "nothing to review: the working copy matches the base",
			"make the changes to review, then call plait review export again")
	}
	return Snapshot{Tree: tree, Base: mb, Head: head}, nil
}

// Commit commits s.Tree on s.Head without a ref; the same tree, head and note give the same commit.
func (r Repo) Commit(ctx context.Context, s Snapshot, note string) (string, error) {
	date, err := r.line(ctx, nil, "show", "-s", "--format=%ct", s.Head)
	if err != nil {
		return "", err
	}
	// Fixed identity and HEAD's date keep the commit id a function of tree, parent and note.
	env := []string{
		"GIT_AUTHOR_NAME=" + identityName, "GIT_AUTHOR_EMAIL=" + identityEmail,
		"GIT_COMMITTER_NAME=" + identityName, "GIT_COMMITTER_EMAIL=" + identityEmail,
		"GIT_AUTHOR_DATE=@" + date + " +0000", "GIT_COMMITTER_DATE=@" + date + " +0000",
	}
	return r.line(ctx, env, "commit-tree", "--no-gpg-sign", "-p", s.Head, "-m", snapshotMsg, "-m", note, s.Tree)
}

// SetRef points ref at commit, keeping the snapshot reachable for git gc.
func (r Repo) SetRef(ctx context.Context, ref, commit string) error {
	_, err := r.git(ctx, nil, "update-ref", ref, commit)
	return err
}

// FileDiff is the unified diff of one file; OldPath is set for a rename.
type FileDiff struct {
	Path    string
	OldPath string
	Diff    string
}

// DiffFiles returns the diff from base to a commit or tree file by file, in git's order, with renames detected.
func (r Repo) DiffFiles(ctx context.Context, base, to string) ([]FileDiff, error) {
	out, err := r.git(ctx, nil, "diff", "-M", "--numstat", "-z", base, to, "--")
	if err != nil {
		return nil, err
	}
	files, err := parseNumstat(out)
	if err != nil {
		return nil, err
	}
	var changed []string
	for _, f := range files {
		changed = append(changed, f.Path)
		if f.OldPath != "" {
			changed = append(changed, f.OldPath)
		}
	}
	for i, f := range files {
		paths := []string{f.Path}
		if f.OldPath != "" {
			paths = append(paths, f.OldPath)
		}
		args := append([]string{"diff", "--no-color", "--no-ext-diff", "-M", base, to, "--"},
			exactPathspecs(paths, changed)...)
		if files[i].Diff, err = r.git(ctx, nil, args...); err != nil {
			return nil, err
		}
	}
	return files, nil
}

// parseNumstat reads "added\tdeleted\tpath\0" records; a rename has an empty path followed by "old\0new\0".
func parseNumstat(out string) ([]FileDiff, error) {
	tokens := strings.Split(strings.TrimSuffix(out, "\x00"), "\x00")
	files := []FileDiff{}
	for i := 0; i < len(tokens) && tokens[i] != ""; i++ {
		fields := strings.SplitN(tokens[i], "\t", numstatFields)
		if len(fields) != numstatFields {
			return nil, output.Internal(fmt.Errorf("git diff --numstat: unexpected record %q", tokens[i]))
		}
		if fields[2] != "" {
			files = append(files, FileDiff{Path: fields[2]})
			continue
		}
		if i+2 >= len(tokens) {
			return nil, output.Internal(errors.New("git diff --numstat: truncated rename record"))
		}
		files = append(files, FileDiff{OldPath: tokens[i+1], Path: tokens[i+2]})
		i += 2
	}
	return files, nil
}

// LineState says what happened to a line between two snapshots.
type LineState string

// Line states of MapLine.
const (
	LineUnchanged LineState = "unchanged"
	LineChanged   LineState = "changed"
	LineGone      LineState = "gone"
)

// Location is where a line of an old snapshot is in a new one; Line 0 stands for the whole file.
// For LineChanged, Line is the start of the hunk that replaced it; for LineGone, File is empty.
type Location struct {
	File  string
	Line  int
	State LineState
}

// MapLine follows file:line from commit from to commit to, renames included.
func (r Repo) MapLine(ctx context.Context, from, to, file string, line int) (Location, error) {
	out, err := r.git(ctx, nil, "diff", "-M", "--name-status", "-z", from, to, "--")
	if err != nil {
		return Location{}, err
	}
	changes := parseNameStatus(out)
	c := findChange(changes, file)
	switch c.status {
	case "":
		return Location{File: file, Line: line, State: LineUnchanged}, nil
	case "D":
		return Location{State: LineGone}, nil
	case "R100":
		return Location{File: c.newPath, Line: line, State: LineUnchanged}, nil
	}
	paths := []string{c.newPath}
	if c.oldPath != c.newPath {
		paths = append(paths, c.oldPath)
	}
	args := append([]string{
		"diff", "-U0", "-M", "--text", "--no-color", "--no-ext-diff", "--no-textconv", from, to, "--",
	}, exactPathspecs(paths, changedPaths(changes))...)
	diff, err := r.git(ctx, nil, args...)
	if err != nil {
		return Location{}, err
	}
	loc := mapHunks(diff, line)
	loc.File = c.newPath
	return loc, nil
}

// change is a --name-status record; status is empty when the file did not change.
type change struct {
	status, oldPath, newPath string
}

// parseNameStatus reads --name-status -z records; a rename or copy carries "old\0new\0".
func parseNameStatus(out string) []change {
	tokens := strings.Split(strings.TrimSuffix(out, "\x00"), "\x00")
	var changes []change
	for i := 0; i+1 < len(tokens); i += 2 {
		st, path := tokens[i], tokens[i+1]
		if st != "" && (st[0] == 'R' || st[0] == 'C') {
			if i+2 >= len(tokens) {
				break
			}
			changes = append(changes, change{st, path, tokens[i+2]})
			i++
			continue
		}
		changes = append(changes, change{st, path, path})
	}
	return changes
}

// findChange returns the change whose old path is file; a copy leaves its source unchanged.
func findChange(changes []change, file string) change {
	for _, c := range changes {
		if c.oldPath == file && c.status != "" && c.status[0] != 'C' {
			return c
		}
	}
	return change{"", file, file}
}

func changedPaths(changes []change) []string {
	var paths []string
	for _, c := range changes {
		paths = append(paths, c.oldPath)
		if c.newPath != c.oldPath {
			paths = append(paths, c.newPath)
		}
	}
	return paths
}

// mapHunks shifts line by the -U0 hunks before it, or reports the hunk that covers it.
func mapHunks(diff string, line int) Location {
	offset, hunks := 0, 0
	for h := range strings.SplitSeq(diff, "\n") {
		m := hunkHeader.FindStringSubmatch(h)
		if m == nil {
			continue
		}
		hunks++
		if line == 0 {
			break
		}
		oldStart, oldCount := atoi(m[1]), countOf(m[2])
		newStart, newCount := atoi(m[3]), countOf(m[4])
		if oldCount == 0 {
			// A pure insertion goes after oldStart.
			if line <= oldStart {
				break
			}
			offset += newCount
			continue
		}
		if line < oldStart {
			break
		}
		if line < oldStart+oldCount {
			return Location{Line: max(newStart, 1), State: LineChanged}
		}
		offset += newCount - oldCount
	}
	if line == 0 && hunks > 0 {
		return Location{State: LineChanged}
	}
	return Location{Line: line + offset, State: LineUnchanged}
}

func countOf(s string) int {
	if s == "" {
		return 1
	}
	return atoi(s)
}

// atoi parses digits the hunk regexp already matched.
func atoi(s string) int {
	n, _ := strconv.Atoi(s)
	return n
}

func (r Repo) head(ctx context.Context) (string, error) {
	h, err := r.line(ctx, nil, "rev-parse", "--verify", "--quiet", "HEAD^{commit}")
	if err != nil {
		return "", output.Invalid("no_head", fmt.Sprintf("%s is not a git repository with a commit at HEAD", r.Root),
			"run plait inside a git repository with at least one commit")
	}
	return h, nil
}

func (r Repo) exists(ctx context.Context, rev string) bool {
	_, err := r.git(ctx, nil, "rev-parse", "--verify", "--quiet", rev+"^{commit}")
	return err == nil
}

func (r Repo) line(ctx context.Context, env []string, args ...string) (string, error) {
	out, err := r.git(ctx, env, args...)
	return strings.TrimSpace(out), err
}

func (r Repo) git(ctx context.Context, env []string, args ...string) (string, error) {
	argv := append([]string{"-C", r.Root}, args...)
	cmd := exec.CommandContext(ctx, "git", argv...)
	cmd.Env = append(os.Environ(), env...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", output.Internal(fmt.Errorf("git %s: %w: %s", args[0], err, strings.TrimSpace(stderr.String())))
	}
	return stdout.String(), nil
}
