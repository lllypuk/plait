package vcs_test

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/lllypuk/plait/internal/output"
	"github.com/lllypuk/plait/internal/vcs"
)

func gitEnv(t *testing.T) {
	t.Helper()
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
}

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	argv := append([]string{"-C", dir}, args...)
	cmd := exec.CommandContext(t.Context(), "git", argv...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func write(t *testing.T, dir, name, content string) {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

// newRepo creates a repo on branch with one commit of a.txt, b.txt and .gitignore.
func newRepo(t *testing.T, branch string) string {
	t.Helper()
	gitEnv(t)
	dir := t.TempDir()
	git(t, dir, "init", "-q", "-b", branch)
	git(t, dir, "config", "user.email", "dev@example.com")
	git(t, dir, "config", "user.name", "dev")
	write(t, dir, "a.txt", "a\n")
	write(t, dir, "b.txt", "b\n")
	write(t, dir, ".gitignore", "*.log\n")
	commit(t, dir, "init")
	return dir
}

func commit(t *testing.T, dir, msg string) string {
	t.Helper()
	git(t, dir, "add", "-A")
	git(t, dir, "commit", "-q", "--allow-empty", "-m", msg)
	return git(t, dir, "rev-parse", "HEAD")
}

func wantKind(t *testing.T, err error, code string) {
	t.Helper()
	e, ok := errors.AsType[*output.Error](err)
	if !ok || e.Kind != output.KindInvalid || e.Code != code || e.Fix == "" {
		t.Fatalf("err = %#v, want Invalid %q with fix", err, code)
	}
}

func TestSnapshotContent(t *testing.T) {
	dir := newRepo(t, "main")
	git(t, dir, "checkout", "-q", "-b", "feature")
	write(t, dir, "a.txt", "a changed\n")
	write(t, dir, "new.txt", "new\n")
	write(t, dir, "debug.log", "ignored\n")
	write(t, dir, ".plait/runs/x/state.json", "{}\n")
	if err := os.Remove(filepath.Join(dir, "b.txt")); err != nil {
		t.Fatal(err)
	}
	write(t, dir, "staged.txt", "staged\n")
	git(t, dir, "add", "staged.txt")
	cachedBefore := git(t, dir, "diff", "--cached")
	statusBefore := git(t, dir, "status", "--porcelain")

	r := vcs.Repo{Root: dir}
	ref := vcs.SnapshotRef("2026-11-05-x", 1)
	s, err := r.Snapshot(t.Context(), "")
	if err != nil {
		t.Fatal(err)
	}
	if s.Commit, err = r.Commit(t.Context(), s, "package x"); err != nil {
		t.Fatal(err)
	}
	if err = r.SetRef(t.Context(), ref, s.Commit); err != nil {
		t.Fatal(err)
	}

	files := strings.Fields(git(t, dir, "ls-tree", "-r", "--name-only", s.Commit))
	if want := []string{".gitignore", "a.txt", "new.txt", "staged.txt"}; !slices.Equal(files, want) {
		t.Errorf("files = %v, want %v", files, want)
	}
	if got := git(t, dir, "show", s.Commit+":a.txt"); got != "a changed" {
		t.Errorf("a.txt = %q", got)
	}
	head := git(t, dir, "rev-parse", "HEAD")
	if s.Head != head || git(t, dir, "rev-parse", s.Commit+"^") != head {
		t.Errorf("parent/head = %s, want %s", s.Head, head)
	}
	if s.Base != head {
		t.Errorf("base = %s, want merge-base %s", s.Base, head)
	}
	if got := git(t, dir, "rev-parse", ref); got != s.Commit {
		t.Errorf("ref = %s, want %s", got, s.Commit)
	}
	if got := git(t, dir, "rev-parse", s.Commit+"^{tree}"); got != s.Tree {
		t.Errorf("tree = %s, want %s", s.Tree, got)
	}
	if got := git(t, dir, "diff", "--cached"); got != cachedBefore {
		t.Errorf("index changed:\n%s\nwas:\n%s", got, cachedBefore)
	}
	if got := git(t, dir, "status", "--porcelain"); got != statusBefore {
		t.Errorf("status changed:\n%s\nwas:\n%s", got, statusBefore)
	}

	diffs, err := r.DiffFiles(t.Context(), s.Base, s.Commit)
	if err != nil {
		t.Fatal(err)
	}
	diff := joinDiffs(diffs)
	for _, want := range []string{"+a changed", "b/new.txt", "a/b.txt", "b/staged.txt"} {
		if !strings.Contains(diff, want) {
			t.Errorf("diff lacks %q:\n%s", want, diff)
		}
	}
	if strings.Contains(diff, ".plait") || strings.Contains(diff, "debug.log") {
		t.Errorf("diff has excluded files:\n%s", diff)
	}
}

func joinDiffs(files []vcs.FileDiff) string {
	var b strings.Builder
	for _, f := range files {
		b.WriteString(f.Diff)
	}
	return b.String()
}

func TestDiffFiles(t *testing.T) {
	dir := newRepo(t, "main")
	write(t, dir, "old name.txt", strings.Repeat("same line\n", 20))
	write(t, dir, "x.txt", "x\n")
	base := commit(t, dir, "more")
	git(t, dir, "mv", "old name.txt", "new name.txt")
	write(t, dir, "[x].txt", "glob\n")
	write(t, dir, "x.txt", "x changed\n")
	if err := os.Remove(filepath.Join(dir, "b.txt")); err != nil {
		t.Fatal(err)
	}
	head := commit(t, dir, "change")

	files, err := vcs.Repo{Root: dir}.DiffFiles(t.Context(), base, head)
	if err != nil {
		t.Fatal(err)
	}
	var paths []string
	for _, f := range files {
		paths = append(paths, f.OldPath+">"+f.Path)
	}
	joined := joinDiffs(files)
	want := []string{">[x].txt", ">b.txt", "old name.txt>new name.txt", ">x.txt"}
	if !slices.Equal(paths, want) {
		t.Errorf("files = %q, want %q", paths, want)
	}
	if full := git(t, dir, "diff", "-M", base, head); strings.TrimSpace(joined) != full {
		t.Errorf("joined per-file diff differs from git diff -M:\n%s\n---\n%s", joined, full)
	}
	if !strings.Contains(files[2].Diff, "rename from old name.txt") || strings.Contains(files[0].Diff, "x changed") {
		t.Errorf("per-file diffs mixed up: %+v", files)
	}

	same, err := vcs.Repo{Root: dir}.DiffFiles(t.Context(), head, head)
	if err != nil || len(same) != 0 {
		t.Errorf("same commit: files = %+v, err = %v", same, err)
	}
}

func TestSnapshotRepeatAndChangedAfter(t *testing.T) {
	dir := newRepo(t, "main")
	git(t, dir, "checkout", "-q", "-b", "feature")
	write(t, dir, "a.txt", "v1\n")
	r := vcs.Repo{Root: dir}

	s1, err := r.Snapshot(t.Context(), "")
	if err != nil {
		t.Fatal(err)
	}
	s2, err := r.Snapshot(t.Context(), "")
	if err != nil {
		t.Fatal(err)
	}
	if s1 != s2 {
		t.Errorf("repeat snapshot = %+v, want %+v", s2, s1)
	}
	c1, err := r.Commit(t.Context(), s1, "package x")
	if err != nil {
		t.Fatal(err)
	}
	if c2, _ := r.Commit(t.Context(), s2, "package x"); c2 != c1 {
		t.Errorf("repeat commit = %s, want %s", c2, c1)
	}
	if other, _ := r.Commit(t.Context(), s1, "package y"); other == c1 {
		t.Error("another note reused the commit")
	}
	tree, err := r.CurrentTree(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if tree != s1.Tree {
		t.Errorf("unchanged working copy: tree %s != snapshot %s", tree, s1.Tree)
	}

	write(t, dir, ".plait/current", "run\n")
	if tree, _ = r.CurrentTree(t.Context()); tree != s1.Tree {
		t.Error(".plait change counted as a working-copy change")
	}

	write(t, dir, "a.txt", "v2\n")
	if tree, _ = r.CurrentTree(t.Context()); tree == s1.Tree {
		t.Error("changed working copy: tree equals snapshot")
	}
	s3, err := r.Snapshot(t.Context(), "")
	if err != nil {
		t.Fatal(err)
	}
	if s3.Tree == s1.Tree {
		t.Error("snapshot after change reused the tree")
	}
}

func TestSnapshotEmptyDiff(t *testing.T) {
	dir := newRepo(t, "main")
	write(t, dir, ".plait/state.json", "{}\n")
	write(t, dir, "x.log", "ignored\n")
	_, err := vcs.Repo{Root: dir}.Snapshot(t.Context(), "")
	wantKind(t, err, "empty_diff")
	if out := git(t, dir, "for-each-ref", "refs/plait"); out != "" {
		t.Errorf("ref written for empty diff: %s", out)
	}
}

func TestResolveBaseSkipsUnrelatedOriginHEAD(t *testing.T) {
	dir := newRepo(t, "main")
	want := git(t, dir, "rev-parse", "HEAD")
	git(t, dir, "checkout", "-q", "--orphan", "foreign")
	commit(t, dir, "foreign")
	git(t, dir, "update-ref", "refs/remotes/origin/foreign", "HEAD")
	git(t, dir, "symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/origin/foreign")
	git(t, dir, "checkout", "-q", "main")
	git(t, dir, "checkout", "-q", "-b", "feature")
	commit(t, dir, "on feature")

	got, err := vcs.Repo{Root: dir}.ResolveBase(t.Context(), "")
	if err != nil || got != want {
		t.Errorf("base = %s, %v; want %s", got, err, want)
	}
}

func TestResolveBase(t *testing.T) {
	tests := []struct {
		name       string
		branch     string
		setup      func(t *testing.T, dir string) string
		configured string
		want       string
		wantCode   string
	}{
		{
			name: "origin/HEAD before main",
			setup: func(t *testing.T, dir string) string {
				t.Helper()
				a := git(t, dir, "rev-parse", "HEAD")
				git(t, dir, "update-ref", "refs/remotes/origin/main", a)
				git(t, dir, "symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/origin/main")
				commit(t, dir, "on main")
				git(t, dir, "checkout", "-q", "-b", "feature")
				return a
			},
		},
		{
			name: "main",
			setup: func(t *testing.T, dir string) string {
				t.Helper()
				b := commit(t, dir, "on main")
				git(t, dir, "checkout", "-q", "-b", "feature")
				commit(t, dir, "on feature")
				return b
			},
		},
		{
			name:   "master",
			branch: "master",
			setup: func(t *testing.T, dir string) string {
				t.Helper()
				b := git(t, dir, "rev-parse", "HEAD")
				git(t, dir, "checkout", "-q", "-b", "feature")
				commit(t, dir, "on feature")
				return b
			},
		},
		{
			name:       "configured wins",
			configured: "develop",
			setup: func(t *testing.T, dir string) string {
				t.Helper()
				git(t, dir, "branch", "develop")
				d := git(t, dir, "rev-parse", "HEAD")
				commit(t, dir, "on main")
				return d
			},
		},
		{
			name:       "configured missing",
			configured: "develop",
			wantCode:   "base_not_found",
		},
		{
			name:     "no candidate",
			branch:   "trunk",
			wantCode: "base_not_found",
		},
		{
			name:       "unrelated history",
			configured: "other",
			setup: func(t *testing.T, dir string) string {
				t.Helper()
				git(t, dir, "checkout", "-q", "--orphan", "other")
				commit(t, dir, "orphan")
				git(t, dir, "checkout", "-q", "main")
				return ""
			},
			wantCode: "no_merge_base",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			branch := tt.branch
			if branch == "" {
				branch = "main"
			}
			dir := newRepo(t, branch)
			var want string
			if tt.setup != nil {
				want = tt.setup(t, dir)
			}
			got, err := vcs.Repo{Root: dir}.ResolveBase(t.Context(), tt.configured)
			if tt.wantCode != "" {
				wantKind(t, err, tt.wantCode)
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got != want {
				t.Errorf("base = %s, want %s", got, want)
			}
		})
	}
}

func TestNoHead(t *testing.T) {
	gitEnv(t)
	dir := t.TempDir()
	r := vcs.Repo{Root: dir}
	_, err := r.CurrentTree(t.Context())
	wantKind(t, err, "no_head")
	git(t, dir, "init", "-q", "-b", "main")
	_, err = r.Snapshot(t.Context(), "")
	wantKind(t, err, "no_head")
}
