package cli_test

import (
	"bytes"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lllypuk/plait/internal/cli"
)

const (
	flowTask = "# Retry failed orders\n\nRetry orders that failed.\n\n## Acceptance criteria\n\n- retried 3 times\n"
	runID    = "PROJ-1-retry"

	unchangedJSON = `"changed":false`
	nextCmd       = "next"
)

type flowCase struct {
	cmd    string
	stdin  string
	noIn   bool
	code   int
	want   string
	asJSON string
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

func writeFile(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

// flowRepo is a repo on branch feature with a staged and an untracked change against main.
func flowRepo(t *testing.T) string {
	t.Helper()
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	dir := t.TempDir()
	git(t, dir, "init", "-q", "-b", "main")
	git(t, dir, "config", "user.email", "dev@example.com")
	git(t, dir, "config", "user.name", "dev")
	writeFile(t, dir, "retry.go", "package retry\n")
	git(t, dir, "add", "-A")
	git(t, dir, "commit", "-q", "-m", "init")
	git(t, dir, "checkout", "-q", "-b", "feature")
	writeFile(t, dir, "retry.go", "package retry\n\nfunc Retry() {}\n")
	git(t, dir, "add", "retry.go")
	writeFile(t, dir, "retry_test.go", "package retry\n")
	writeFile(t, dir, "task.md", flowTask)
	return dir
}

func runFlow(t *testing.T, dir string, tty bool, c flowCase) {
	t.Helper()
	var out, errOut bytes.Buffer
	var stdin io.Reader
	if !c.noIn {
		stdin = strings.NewReader(c.stdin)
	}
	args := strings.Fields(c.cmd)
	if !tty {
		args = append(args, "--json")
	}
	code := cli.Run(cli.Options{
		Version:    "v1",
		Args:       args,
		Dir:        dir,
		Stdin:      stdin,
		Stdout:     &out,
		Stderr:     &errOut,
		IsTerminal: func(io.Writer) bool { return tty },
	})
	if code != c.code {
		t.Fatalf("plait %s: code = %d, want %d\nstdout: %s\nstderr: %s", c.cmd, code, c.code, &out, &errOut)
	}
	want, got := c.want, out.String()
	if !tty {
		want = c.asJSON
	} else if code != 0 {
		got = errOut.String()
	}
	if !strings.Contains(got, want) {
		t.Errorf("plait %s: output %q, want substring %q", c.cmd, got, want)
	}
}

// TestFlow drives the whole slice in-process in both output modes and checks the developer's index survives it.
func TestFlow(t *testing.T) {
	for _, tty := range []bool{true, false} {
		t.Run(map[bool]string{true: "text", false: "json"}[tty], func(t *testing.T) {
			dir := flowRepo(t)
			index := git(t, dir, "ls-files", "--stage")
			for _, c := range []flowCase{
				{cmd: "run list", want: "no runs", asJSON: `"runs":[]`},
				{cmd: nextCmd, code: 2, want: "fix: start one with plait run new", asJSON: `"code":"no_current_run"`},
				{cmd: "run new retry --key PROJ-1", want: "created run " + runID,
					asJSON: `"run":"` + runID + `","step":"new","dir":".plait/runs/` + runID + `","created":true`},
				{cmd: "run new retry --key PROJ-1", want: "resumed existing", asJSON: `"created":false`},
				{cmd: "run resume " + runID, want: "current run: " + runID, asJSON: `"run":"` + runID},
				{cmd: "run resume nope", code: 2, want: "fix: see existing runs", asJSON: `"code":"run_not_found"`},
				{cmd: "run list", want: "* " + runID + "  new", asJSON: `"current":true`},
				{cmd: nextCmd, want: "then: plait ctx save < task.md", asJSON: `"step":"new"`},

				{cmd: "ctx save", noIn: true, code: 2, want: "fix: pipe it", asJSON: `"code":"no_task_input"`},
				{cmd: "ctx save --file missing.md", code: 2, want: "fix: write the task",
					asJSON: `"code":"task_file_not_found"`},
				{cmd: "ctx save", stdin: flowTask, want: "(acceptance criteria present)\nnext: plait review export",
					asJSON: `"has_criteria":true`},
				{cmd: "ctx save --file task.md", want: "unchanged", asJSON: unchangedJSON},
				{cmd: nextCmd, want: "input: .plait/runs/" + runID + "/task.md", asJSON: `"step":"task_saved"`},

				{cmd: "review export --base nope", code: 2, want: "fix: pass --base", asJSON: `"code":"base_not_found"`},
				{cmd: "review export", want: "exported snapshot 1", asJSON: `"snapshot":1`},
				{cmd: "review export --base main", want: "unchanged snapshot 1", asJSON: unchangedJSON},
				{cmd: nextCmd, want: "(schema review-findings/v1)", asJSON: `"step":"exported"`},
				{cmd: "review report", code: 2, want: "fix: review the exported package.md", asJSON: `"code":"no_review"`},
			} {
				runFlow(t, dir, tty, c)
			}

			commit := git(t, dir, "rev-parse", "refs/plait/snapshots/"+runID+"/1")
			answer := `{"snapshot":"` + commit + `","coverage":"criteria","findings":[{"id":"F1","severity":"major",` +
				`"file":"retry.go","line":3,"title":"no retry","rationale":"Retry does nothing","criterion":"retried 3 times"}]}`
			writeFile(t, dir, "answer.json", answer)
			for _, c := range []flowCase{
				{cmd: "review import -", code: 2, want: "fix: pipe it", asJSON: `"code":"no_answer_input"`},
				{cmd: "review import missing.json", code: 2, want: "fix: save the fresh session",
					asJSON: `"code":"answer_not_found"`},
				{cmd: "review import answer.json", want: "imported .plait/runs/" + runID, asJSON: `"findings":1,`},
				{cmd: "review import -", stdin: answer, want: "unchanged", asJSON: unchangedJSON},
				{cmd: nextCmd, want: "then: plait review report", asJSON: `"step":"imported"`},
				{cmd: "review report", want: "no retry", asJSON: `"step":"reported"`},
			} {
				runFlow(t, dir, tty, c)
			}

			if got := git(t, dir, "ls-files", "--stage"); got != index {
				t.Errorf("index changed:\n%s\nwas:\n%s", got, index)
			}
		})
	}
}
