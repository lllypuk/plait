package review_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/lllypuk/plait/internal/review"
	"github.com/lllypuk/plait/internal/vcs"
)

func TestMatchGlob(t *testing.T) {
	cases := []struct {
		pattern, name string
		want          bool
	}{
		{envFile, envFile, true},
		{envFile, "app/.env", false},
		{"**/.env", envFile, true},
		{"**/.env", "app/.env", true},
		{"**/.env.*", "svc/api/.env.local", true},
		{".env.*", ".env.local", true},
		{".env.*", envFile, false},
		{pemGlob, "server.pem", true},
		{pemGlob, "a/b/server.pem", true},
		{pemGlob, "a/server.pem.txt", false},
		{"*.pem", "a/server.pem", false},
		{nestedDirGlob, "secrets/db.yaml", true},
		{nestedDirGlob, "app/secrets/x/db.yaml", true},
		{nestedDirGlob, "app/secrets.yaml", false},
		{"**/id_rsa*", "home/.ssh/id_rsa.pub", true},
		{"docs/*.md", "docs/a.md", true},
		{"docs/*.md", "docs/sub/a.md", false},
		{"docs/**", "docs/sub/a.md", true},
		{"/AGENTS.md", "AGENTS.md", true},
		{badGlob, badGlob, false},
	}
	for _, c := range cases {
		if got := review.MatchGlob(c.pattern, c.name); got != c.want {
			t.Errorf("MatchGlob(%q, %q) = %v, want %v", c.pattern, c.name, got, c.want)
		}
	}
}

func TestCheckGlobs(t *testing.T) {
	if err := review.CheckGlobs([]string{envFile, pemGlob, "a/[bc]/**"}); err != nil {
		t.Fatal(err)
	}
	for _, g := range []string{badGlob, "ok/[x/**", `x\`} {
		wantInvalid(t, review.CheckGlobs([]string{g}), "config_invalid")
	}
}

func fileDiff(path string, size int) vcs.FileDiff {
	head := "diff --git a/" + path + " b/" + path + "\n"
	return vcs.FileDiff{Path: path, Diff: head + strings.Repeat("+", max(0, size-len(head)-1)) + "\n"}
}

func TestScopeDiff(t *testing.T) {
	small, mid, big := fileDiff("small.go", 100), fileDiff("mid.go", 300), fileDiff(bigGo, 600)
	files := []vcs.FileDiff{big, small, mid}
	all := big.Diff + small.Diff + mid.Diff

	cases := []struct {
		name     string
		files    []vcs.FileDiff
		deny     []string
		max      int
		diff     string
		excluded []string
		dropped  []string
	}{
		{"within limit", files, nil, 2000, all, nil, nil},
		{"exactly the limit", files, nil, 1000, all, nil, nil},
		{"no limit", files, nil, 0, all, nil, nil},
		{"largest dropped first", files, nil, 500, small.Diff + mid.Diff, nil, []string{bigGo}},
		{"several dropped", files, nil, 350, small.Diff, nil, []string{bigGo, "mid.go"}},
		{"one file over the limit", []vcs.FileDiff{big}, nil, 100, "", nil, []string{bigGo}},
		{"denied before the limit", files, []string{bigGo}, 400, small.Diff + mid.Diff, []string{bigGo}, nil},
		{
			"rename from a denied path", []vcs.FileDiff{{Path: "conf.txt", OldPath: envFile, Diff: "x"}, small},
			[]string{envFile}, 0, small.Diff, []string{"conf.txt"}, nil,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			sc := review.ScopeDiff(c.files, c.deny, c.max)
			if sc.Diff != c.diff {
				t.Errorf("diff = %q, want %q", sc.Diff, c.diff)
			}
			if !slices.Equal(sc.Excluded, c.excluded) && len(sc.Excluded)+len(c.excluded) > 0 {
				t.Errorf("excluded = %v, want %v", sc.Excluded, c.excluded)
			}
			if !slices.Equal(sc.Dropped, c.dropped) && len(sc.Dropped)+len(c.dropped) > 0 {
				t.Errorf("dropped = %v, want %v", sc.Dropped, c.dropped)
			}
		})
	}
}
