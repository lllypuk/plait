package run

import (
	"fmt"
	"path"
	"strconv"

	"github.com/lllypuk/plait/internal/output"
	"github.com/lllypuk/plait/internal/skills"
)

// FindingsSchema is the schema of a review answer the agent imports.
const FindingsSchema = "review-findings/v1"

// Commands the agent may call, as listed in Instruction.Allowed.
const (
	cmdCtxSave      = "ctx save"
	cmdCheckSave    = "check save"
	cmdReviewExport = "review export"
	cmdReviewImport = "review import"
	cmdReviewReport = "review report"
	cmdMRDescribe   = "mr describe"
)

// Instruction is the answer of plait next: what the agent does now and with which command it hands the result back.
type Instruction struct {
	Run     string   `json:"run"`
	Step    Step     `json:"step"`
	Do      string   `json:"do"`
	Inputs  []string `json:"inputs"`
	Output  Target   `json:"output"`
	Allowed []string `json:"allowed"`
	Guide   string   `json:"guide"`
}

// Target is the command that records the step result.
type Target struct {
	Cmd    string `json:"cmd"`
	Schema string `json:"schema,omitempty"`
}

// Next derives the instruction from the run step; paths are relative to the repository root.
func Next(st *State) (Instruction, error) {
	dir := path.Join(".plait", "runs", st.ID)
	in := Instruction{Run: st.ID, Step: st.Step, Inputs: []string{}}
	switch st.Step {
	case StepNew:
		in.Do = "Save the task description with its acceptance criteria as Markdown on stdin. " +
			"Do not invent criteria: if the task has none, save it without them."
		in.Output = Target{Cmd: "plait ctx save < task.md"}
		in.Allowed = []string{cmdCtxSave}
	case StepTaskSaved:
		in.Do = "Export the review package: plait snapshots the working copy and writes package.md for a fresh session."
		in.Inputs = []string{path.Join(dir, "task.md")}
		in.Output = Target{Cmd: "plait review export"}
		in.Allowed = []string{cmdReviewExport, cmdCtxSave, cmdCheckSave}
	case StepExported:
		n, err := lastSnapshot(st)
		if err != nil {
			return Instruction{}, err
		}
		in.Do = "Ask the developer to open a new agent session, give it package.md and save its JSON answer to a file; " +
			"do not review the package in this session. Then import the answer."
		in.Inputs = []string{path.Join(dir, "review-"+n, "package.md")}
		in.Output = Target{Cmd: "plait review import <file|->", Schema: FindingsSchema}
		in.Allowed = []string{cmdReviewImport, cmdReviewExport, cmdCheckSave}
	case StepImported, StepReported:
		n, err := lastReview(st)
		if err != nil {
			return Instruction{}, err
		}
		in.Allowed = []string{cmdReviewReport, cmdReviewExport, cmdReviewImport, cmdCheckSave}
		rev := st.Reviews[len(st.Reviews)-1]
		switch {
		case st.Step == StepImported:
			in.Do = "Show the review report to the developer."
			in.Inputs = []string{path.Join(dir, "review-"+n+".json")}
			in.Output = Target{Cmd: "plait review report"}
		case rev.Blocking > 0:
			in.Do = fmt.Sprintf("The review left %d unresolved blocker or major findings. "+
				"Fix the ones the developer accepts, then export a new snapshot for another review round.", rev.Blocking)
			in.Inputs = []string{path.Join(dir, "findings-"+n+".json")}
			in.Output = Target{Cmd: "plait review export"}
			in.Allowed = append(in.Allowed, cmdMRDescribe)
		default:
			in.Do = "No blocker or major findings are left: describe the merge request. " +
				"To fix the remaining findings first, fix them and export a new snapshot instead."
			in.Inputs = []string{path.Join(dir, "findings-"+n+".json")}
			in.Output = Target{Cmd: "plait mr describe"}
			in.Allowed = append(in.Allowed, cmdMRDescribe)
		}
	case StepDescribed:
		if err := described(st, &in, dir); err != nil {
			return Instruction{}, err
		}
	default:
		return Instruction{}, BrokenState(st, fmt.Sprintf("unknown step %q", st.Step))
	}
	in.Allowed = append(in.Allowed, "next", "guide", "run list", "run resume", "run new")
	in.Guide = "plait guide " + guideStage(st)
	return in, nil
}

// guideStage runs after the step switch, which has rejected a reported step without reviews.
func guideStage(st *State) string {
	switch st.Step {
	case StepNew:
		return skills.StageCtx
	case StepReported:
		if st.Reviews[len(st.Reviews)-1].Blocking > 0 {
			return skills.StageFix
		}
		return skills.StageMR
	case StepDescribed:
		return skills.StageMR
	case StepTaskSaved, StepExported, StepImported:
	}
	return skills.StageReview
}

func described(st *State, in *Instruction, dir string) error {
	if _, err := lastReview(st); err != nil {
		return err
	}
	in.Do = "The merge request description is ready: show its title and body to the developer, " +
		"who creates the merge request; plait does not. After further changes, export a new snapshot for review."
	in.Inputs = []string{path.Join(dir, "mr.md")}
	in.Output = Target{Cmd: "plait mr describe"}
	in.Allowed = []string{cmdMRDescribe, cmdReviewReport, cmdReviewExport, cmdReviewImport, cmdCheckSave}
	return nil
}

func lastSnapshot(st *State) (string, error) {
	if len(st.Snapshots) == 0 {
		return "", BrokenState(st, "step "+string(st.Step)+" without a snapshot")
	}
	return strconv.Itoa(st.Snapshots[len(st.Snapshots)-1].N), nil
}

func lastReview(st *State) (string, error) {
	if len(st.Reviews) == 0 {
		return "", BrokenState(st, "step "+string(st.Step)+" without an imported review")
	}
	return strconv.Itoa(st.Reviews[len(st.Reviews)-1].N), nil
}

// BrokenState reports a state.json plait cannot act on; only a human may repair it.
func BrokenState(st *State, why string) error {
	file := path.Join(".plait", "runs", st.ID, stateFile)
	return output.NeedsHuman("state_corrupt", fmt.Sprintf("%s: %s", file, why),
		"stop and ask a human to repair "+file+" or start over with "+newRunFix)
}
