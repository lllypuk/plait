package task_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/lllypuk/plait/internal/config"
	"github.com/lllypuk/plait/internal/run"
	"github.com/lllypuk/plait/internal/task"
)

const anyAccess = "any Jira access"

func TestFetch(t *testing.T) {
	tests := []struct {
		name, runKey, tool, want, notWant string
	}{
		{name: "mcp tool", runKey: key, tool: "jira_get_issue", want: "jira_get_issue", notWant: anyAccess},
		{name: "no tool", runKey: key, want: anyAccess},
		{name: "run without key", want: anyAccess},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := run.Open(t.TempDir())
			st, _, err := s.New("order-retry", tt.runKey)
			if err != nil {
				t.Fatal(err)
			}
			in, err := task.Fetch(s, key, config.Jira{Mode: task.JiraModeMCP, MCPTool: tt.tool})
			if err != nil {
				t.Fatalf("Fetch: %v", err)
			}
			if in.Run != st.ID || in.Step != run.StepNew {
				t.Errorf("run, step = %s, %s", in.Run, in.Step)
			}
			wantFetchInstruction(t, in, tt.want, tt.notWant)
		})
	}
}

func wantFetchInstruction(t *testing.T, in run.Instruction, want, notWant string) {
	t.Helper()
	for _, s := range []string{key, want, "--source local"} {
		if !strings.Contains(in.Do, s) {
			t.Errorf("do = %q, want %q", in.Do, s)
		}
	}
	if notWant != "" && strings.Contains(in.Do, notWant) {
		t.Errorf("do = %q, must not mention %q", in.Do, notWant)
	}
	if in.Output.Cmd != "plait ctx save "+key+" --source jira < task.md" || in.Guide != "plait guide ctx" {
		t.Errorf("output, guide = %q, %q", in.Output.Cmd, in.Guide)
	}
	if !slices.Contains(in.Allowed, "ctx save") || in.Inputs == nil {
		t.Errorf("allowed, inputs = %v, %v", in.Allowed, in.Inputs)
	}
}

func TestFetchRejects(t *testing.T) {
	mcp := config.Jira{Mode: task.JiraModeMCP}
	rest := config.Jira{Mode: "rest"}
	tests := []struct {
		name, runKey, key, code string
		noRun                   bool
		jira                    config.Jira
	}{
		{name: "bad key", runKey: key, key: "proj-7", jira: mcp, code: "invalid_key"},
		{name: "no run", noRun: true, key: key, jira: mcp, code: "no_current_run"},
		{name: "other run key", runKey: key, key: "PROJ-8", jira: mcp, code: "key_mismatch"},
		{name: "rest mode", runKey: key, key: key, jira: rest, code: "jira_mode_unsupported"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := run.Open(t.TempDir())
			if !tt.noRun {
				if _, _, err := s.New("order-retry", tt.runKey); err != nil {
					t.Fatal(err)
				}
			}
			_, err := task.Fetch(s, tt.key, tt.jira)
			wantInvalid(t, err, tt.code)
		})
	}
}
