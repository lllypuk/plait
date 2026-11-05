// Package vcs snapshots the working copy into git commits under refs/plait.
package vcs

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
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
	baseFix       = "pass --base <branch> to plait review export or set review.base in .plait/config.yaml"
)

// Repo is a git repository rooted at Root.
type Repo struct {
	Root string
}

// Snapshot is a commit of the working copy on top of Head; Base is the merge-base the diff starts from.
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

// Snapshot commits the working copy without a ref; the same tree on the same HEAD gives the same commit.
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
	date, err := r.line(ctx, nil, "show", "-s", "--format=%ct", head)
	if err != nil {
		return Snapshot{}, err
	}
	// Fixed identity and HEAD's date keep the commit id a function of tree and parent.
	env := []string{
		"GIT_AUTHOR_NAME=" + identityName, "GIT_AUTHOR_EMAIL=" + identityEmail,
		"GIT_COMMITTER_NAME=" + identityName, "GIT_COMMITTER_EMAIL=" + identityEmail,
		"GIT_AUTHOR_DATE=@" + date + " +0000", "GIT_COMMITTER_DATE=@" + date + " +0000",
	}
	commit, err := r.line(ctx, env, "commit-tree", "--no-gpg-sign", "-p", head, "-m", snapshotMsg, tree)
	if err != nil {
		return Snapshot{}, err
	}
	return Snapshot{Commit: commit, Tree: tree, Base: mb, Head: head}, nil
}

// SetRef points ref at commit, keeping the snapshot reachable for git gc.
func (r Repo) SetRef(ctx context.Context, ref, commit string) error {
	_, err := r.git(ctx, nil, "update-ref", ref, commit)
	return err
}

// Diff returns the unified diff from base to commit.
func (r Repo) Diff(ctx context.Context, base, commit string) (string, error) {
	return r.git(ctx, nil, "diff", "--no-color", "--no-ext-diff", base, commit, "--")
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
