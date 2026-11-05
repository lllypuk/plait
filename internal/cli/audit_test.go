package cli_test

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/lllypuk/plait/internal/audit"
	plaitrun "github.com/lllypuk/plait/internal/run"
)

const marker = "MARKER-7f3a-do-not-log"

// auditRepo is flowRepo with the marker in a diff file, a rule file and two files under deny globs.
func auditRepo(t *testing.T) string {
	t.Helper()
	dir := flowRepo(t)
	writeFile(t, dir, "retry.go", "package retry\n\n// "+marker+"\nfunc Retry() {}\n")
	writeFile(t, dir, ".env", "TOKEN="+marker+"\n")
	writeFile(t, dir, "AGENTS.md", "Rules: "+marker+"\n")
	for _, d := range []string{".plait", "secrets"} {
		if err := os.MkdirAll(filepath.Join(dir, d), 0o750); err != nil {
			t.Fatal(err)
		}
	}
	writeFile(t, dir, "secrets/rules.md", marker+"\n")
	writeFile(t, dir, ".plait/config.yaml", "review:\n  rules: [AGENTS.md, secrets/rules.md]\n")
	return dir
}

func readAudit(t *testing.T, dir string) []audit.Entry {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(dir, ".plait", "runs", runID, audit.FileName))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, []byte(marker)) {
		t.Errorf("audit log carries file content or check output:\n%s", raw)
	}
	var got []audit.Entry
	for line := range strings.Lines(string(raw)) {
		var e audit.Entry
		if err = json.Unmarshal([]byte(line), &e); err != nil {
			t.Fatalf("line %q: %v", line, err)
		}
		if e.Run != runID || e.Agent != "test-agent" || e.Version != "v1" || e.TS.IsZero() {
			t.Errorf("line %q: wrong run, agent, version or ts", line)
		}
		got = append(got, e)
	}
	return got
}

// TestAuditLog checks every run command leaves one line, refusals included, and no line carries content.
func TestAuditLog(t *testing.T) {
	t.Setenv("PLAIT_AGENT", "test-agent")
	dir := auditRepo(t)
	for _, c := range []flowCase{
		{cmd: runNewCmd, asJSON: `"created":true`},
		{cmd: ctxSaveCmd, noIn: true, code: 2, asJSON: `"code":"no_task_input"`},
		{cmd: ctxSaveCmd, stdin: flowTask + "\n" + marker + "\n", asJSON: `"has_criteria":true`},
		{cmd: "check save test", stdin: marker, code: 2, asJSON: `"code":"no_exit_code"`},
		{cmd: "check save test --exit-code 1", stdin: "FAIL " + marker + "\n", asJSON: `"exit_code":1`},
		{cmd: exportCmd, asJSON: `"snapshot":1`},
		{cmd: "review import missing.json", code: 2, asJSON: `"code":"answer_not_found"`},
	} {
		runFlow(t, dir, false, c)
	}
	commit := git(t, dir, "rev-parse", "refs/plait/snapshots/"+runID+"/1")
	answer := `{"snapshot":"` + commit + `","coverage":"criteria","findings":[]}`
	for _, c := range []flowCase{
		{cmd: importStdinCmd, stdin: answer, asJSON: `"findings":0`},
		{cmd: reportCmd, asJSON: `"step":"reported"`},
		{cmd: "mr describe", asJSON: `"step":"described"`},
	} {
		runFlow(t, dir, false, c)
	}

	got := readAudit(t, dir)
	type row struct {
		cmd      string
		from, to plaitrun.Step
		err      string
	}
	want := []row{
		{ctxSaveCmd, plaitrun.StepNew, plaitrun.StepNew, "no_task_input"},
		{ctxSaveCmd, plaitrun.StepNew, plaitrun.StepTaskSaved, ""},
		{"check save", plaitrun.StepTaskSaved, plaitrun.StepTaskSaved, "no_exit_code"},
		{"check save", plaitrun.StepTaskSaved, plaitrun.StepTaskSaved, ""},
		{exportCmd, plaitrun.StepTaskSaved, plaitrun.StepExported, ""},
		{"review import", plaitrun.StepExported, plaitrun.StepExported, "answer_not_found"},
		{"review import", plaitrun.StepExported, plaitrun.StepImported, ""},
		{reportCmd, plaitrun.StepImported, plaitrun.StepReported, ""},
		{"mr describe", plaitrun.StepReported, plaitrun.StepDescribed, ""},
	}
	rows := make([]row, len(got))
	for i, e := range got {
		rows[i] = row{e.Cmd, plaitrun.Step(e.StepFrom), plaitrun.Step(e.StepTo), e.Error}
	}
	if !slices.Equal(rows, want) {
		t.Fatalf("rows = %v\nwant %v", rows, want)
	}
	checkExportEntry(t, got[4])
	if imp := got[6]; imp.Snapshot != 1 || imp.AnswerSHA256 != audit.SHA256([]byte(answer)) {
		t.Errorf("import snapshot %d, answer sha %q", imp.Snapshot, imp.AnswerSHA256)
	}
	if got[8].Snapshot != 1 {
		t.Errorf("mr describe snapshot = %d", got[8].Snapshot)
	}
}

func checkExportEntry(t *testing.T, e audit.Entry) {
	t.Helper()
	if e.Snapshot != 1 || len(e.PackageSHA256) != 64 {
		t.Errorf("export snapshot %d, package sha %q", e.Snapshot, e.PackageSHA256)
	}
	read := make([]string, 0, len(e.Context))
	for _, f := range e.Context {
		if len(f.SHA256) != 64 {
			t.Errorf("context %s: sha %q", f.Path, f.SHA256)
		}
		read = append(read, f.Path)
	}
	for _, p := range []string{".plait/runs/" + runID + "/task.md", "AGENTS.md", "retry.go"} {
		if !slices.Contains(read, p) {
			t.Errorf("context %v lacks %s", read, p)
		}
	}
	for _, p := range []string{".env", "secrets/rules.md"} {
		if slices.Contains(read, p) {
			t.Errorf("context %v holds denied %s", read, p)
		}
		if !slices.Contains(e.Excluded, audit.Excluded{Path: p, Reason: audit.ReasonDenyGlob}) {
			t.Errorf("excluded %v lacks %s", e.Excluded, p)
		}
	}
}

func TestAuditNoRun(t *testing.T) {
	dir := flowRepo(t)
	runFlow(t, dir, false, flowCase{cmd: ctxSaveCmd, stdin: flowTask, code: 2, asJSON: `"code":"no_current_run"`})
	if _, err := os.Stat(filepath.Join(dir, ".plait", "runs")); !os.IsNotExist(err) {
		t.Errorf("a command without a run left .plait/runs: %v", err)
	}
}
