package run_test

import (
	"errors"
	"slices"
	"testing"

	"github.com/lllypuk/plait/internal/output"
	"github.com/lllypuk/plait/internal/run"
	"github.com/lllypuk/plait/internal/skills"
)

const (
	cmdExport   = "review export"
	cmdImport   = "review import"
	cmdReport   = "review report"
	cmdCheck    = "check save"
	cmdDescribe = "mr describe"
)

func TestNext(t *testing.T) {
	const id = "2026-11-05-order-retry"
	dir := ".plait/runs/" + id
	snaps := []run.Snapshot{{N: 1}, {N: 2}}
	reviews := []run.Review{{N: 2}}
	blocking := []run.Review{{N: 2, Blocking: 1}}
	tests := []struct {
		name       string
		st         run.State
		cmd        string
		schema     string
		inputs     []string
		mustAllow  string
		forbidCmds []string
		guide      string
	}{
		{
			name:       "new",
			st:         run.State{Step: run.StepNew},
			cmd:        "plait ctx save < task.md",
			inputs:     []string{},
			mustAllow:  "ctx save",
			forbidCmds: []string{cmdExport, cmdImport, cmdReport, cmdCheck},
			guide:      skills.StageCtx,
		},
		{
			name:       "task saved",
			st:         run.State{Step: run.StepTaskSaved},
			cmd:        "plait review export",
			inputs:     []string{dir + "/task.md"},
			mustAllow:  cmdExport,
			forbidCmds: []string{cmdImport, cmdReport},
			guide:      skills.StageReview,
		},
		{
			name:       "exported",
			st:         run.State{Step: run.StepExported, Snapshots: snaps},
			cmd:        "plait review import <file|->",
			schema:     run.FindingsSchema,
			inputs:     []string{dir + "/review-2/package.md"},
			mustAllow:  cmdImport,
			forbidCmds: []string{cmdReport},
			guide:      skills.StageReview,
		},
		{
			name:       "imported",
			st:         run.State{Step: run.StepImported, Snapshots: snaps, Reviews: reviews},
			cmd:        "plait review report",
			inputs:     []string{dir + "/review-2.json"},
			mustAllow:  cmdReport,
			forbidCmds: []string{cmdDescribe},
			guide:      skills.StageReview,
		},
		{
			name:      "reported, blocker or major open",
			st:        run.State{Step: run.StepReported, Snapshots: snaps, Reviews: blocking},
			cmd:       "plait review export",
			inputs:    []string{dir + "/findings-2.json"},
			mustAllow: cmdExport,
			guide:     skills.StageFix,
		},
		{
			name:      "reported, nothing blocking",
			st:        run.State{Step: run.StepReported, Snapshots: snaps, Reviews: reviews},
			cmd:       "plait mr describe",
			inputs:    []string{dir + "/findings-2.json"},
			mustAllow: cmdDescribe,
			guide:     skills.StageMR,
		},
		{
			name:      "described",
			st:        run.State{Step: run.StepDescribed, Snapshots: snaps, Reviews: blocking},
			cmd:       "plait mr describe",
			inputs:    []string{dir + "/mr.md"},
			mustAllow: cmdExport,
			guide:     skills.StageMR,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.st.ID = id
			in, err := run.Next(&tt.st)
			if err != nil {
				t.Fatal(err)
			}
			if in.Run != id || in.Step != tt.st.Step || in.Do == "" || in.Output.Cmd != tt.cmd ||
				in.Output.Schema != tt.schema {
				t.Errorf("instruction = %+v, want cmd %q schema %q", in, tt.cmd, tt.schema)
			}
			if !slices.Equal(in.Inputs, tt.inputs) {
				t.Errorf("inputs = %q, want %q", in.Inputs, tt.inputs)
			}
			want := []string{tt.mustAllow, "next"}
			if tt.st.Step != run.StepNew {
				want = append(want, cmdCheck)
			}
			checkAllowed(t, in.Allowed, want, tt.forbidCmds)
			checkGuide(t, in.Guide, tt.guide)
		})
	}
}

func TestNextBrokenState(t *testing.T) {
	for name, st := range map[string]run.State{
		"unknown step":          {ID: "r", Step: "bogus"},
		"exported, no snapshot": {ID: "r", Step: run.StepExported},
		"imported, no review":   {ID: "r", Step: run.StepImported},
		"described, no review":  {ID: "r", Step: run.StepDescribed},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := run.Next(&st)
			e, ok := errors.AsType[*output.Error](err)
			if !ok || e.Kind != output.KindNeedsHuman || e.Fix == "" {
				t.Fatalf("err = %#v, want NeedsHuman with fix", err)
			}
		})
	}
}

func TestCurrentWithoutRun(t *testing.T) {
	_, err := newStore(t).Current()
	wantInvalid(t, err, "no_current_run")
	if e, _ := errors.AsType[*output.Error](err); e.Fix != "start one with plait run new <slug>" {
		t.Errorf("fix = %q", e.Fix)
	}
}

func checkGuide(t *testing.T, got, stage string) {
	t.Helper()
	if got != "plait guide "+stage {
		t.Errorf("guide = %q, want plait guide %s", got, stage)
	}
	if _, err := skills.Guide(stage); err != nil {
		t.Errorf("guide %s: %v", stage, err)
	}
}

func checkAllowed(t *testing.T, allowed, want, forbid []string) {
	t.Helper()
	for _, c := range want {
		if !slices.Contains(allowed, c) {
			t.Errorf("allowed = %q lacks %q", allowed, c)
		}
	}
	for _, c := range forbid {
		if slices.Contains(allowed, c) {
			t.Errorf("allowed = %q contains %q before its step", allowed, c)
		}
	}
}
