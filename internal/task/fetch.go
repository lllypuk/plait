package task

import (
	"errors"
	"fmt"

	"github.com/lllypuk/plait/internal/config"
	"github.com/lllypuk/plait/internal/output"
	"github.com/lllypuk/plait/internal/run"
)

// JiraModeMCP is the only jira.mode of v0: the agent fetches the task itself.
const JiraModeMCP = "mcp"

// Fetch tells the agent how to fetch the tracker task key and save it into the current run.
func Fetch(s *run.Store, key string, j config.Jira) (run.Instruction, error) {
	if !run.ValidKey(key) {
		return run.Instruction{}, output.Invalid("invalid_key", fmt.Sprintf("task key %q must look like PROJ-123", key),
			"plait ctx PROJ-123 with the key of the Jira issue")
	}
	if j.Mode != JiraModeMCP {
		return run.Instruction{}, output.Invalid("jira_mode_unsupported",
			fmt.Sprintf("jira.mode %q is not supported", j.Mode),
			"set jira.mode: mcp in .plait/config.yaml, or save the task yourself: plait ctx save "+key+" < task.md")
	}
	st, err := s.Current()
	if err != nil {
		if e, ok := errors.AsType[*output.Error](err); ok && e.Code == "no_current_run" {
			return run.Instruction{}, output.Invalid(e.Code, e.Message, "plait run new <slug> --key "+key)
		}
		return run.Instruction{}, err
	}
	runKey := run.KeyOf(st.ID)
	if runKey == "" && st.Task != nil {
		runKey = st.Task.Key
	}
	if runKey != "" && runKey != key {
		return run.Instruction{}, output.Invalid("key_mismatch",
			fmt.Sprintf("the current run %s is for %s, not %s", st.ID, runKey, key),
			"plait ctx "+runKey+", or start a run for the other task: plait run new <slug> --key "+key)
	}
	access := "any Jira access you have"
	if j.MCPTool != "" {
		access = "the MCP tool " + j.MCPTool
	}
	return run.Instruction{
		Run:  st.ID,
		Step: st.Step,
		Do: fmt.Sprintf("Fetch Jira issue %s with %s. Save its summary, description, acceptance criteria "+
			"and the comments that change the scope as Markdown on stdin. "+
			"If Jira is not reachable, ask the developer to describe the task and save it with --source local. "+
			"Do not invent criteria.", key, access),
		Inputs:  []string{},
		Output:  run.Target{Cmd: "plait ctx save " + key + " --source jira < task.md"},
		Allowed: []string{"ctx save", "next", "guide"},
		Guide:   "plait guide ctx",
	}, nil
}
