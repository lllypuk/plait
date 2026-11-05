package run_test

import (
	"errors"
	"slices"
	"testing"

	"github.com/lllypuk/plait/internal/output"
	"github.com/lllypuk/plait/internal/run"
)

const (
	cmdExport = "review export"
	cmdImport = "review import"
	cmdReport = "review report"
)

func TestNext(t *testing.T) {
	const id = "2026-11-05-order-retry"
	dir := ".plait/runs/" + id
	snaps := []run.Snapshot{{N: 1}, {N: 2}}
	reviews := []run.Review{{N: 2}}
	tests := []struct {
		name       string
		st         run.State
		cmd        string
		schema     string
		inputs     []string
		mustAllow  string
		forbidCmds []string
	}{
		{
			name:       "new",
			st:         run.State{Step: run.StepNew},
			cmd:        "plait ctx save < task.md",
			inputs:     []string{},
			mustAllow:  "ctx save",
			forbidCmds: []string{cmdExport, cmdImport, cmdReport},
		},
		{
			name:       "task saved",
			st:         run.State{Step: run.StepTaskSaved},
			cmd:        "plait review export",
			inputs:     []string{dir + "/task.md"},
			mustAllow:  cmdExport,
			forbidCmds: []string{cmdImport, cmdReport},
		},
		{
			name:       "exported",
			st:         run.State{Step: run.StepExported, Snapshots: snaps},
			cmd:        "plait review import <file|->",
			schema:     run.FindingsSchema,
			inputs:     []string{dir + "/review-2/package.md"},
			mustAllow:  cmdImport,
			forbidCmds: []string{cmdReport},
		},
		{
			name:      "imported",
			st:        run.State{Step: run.StepImported, Snapshots: snaps, Reviews: reviews},
			cmd:       "plait review report",
			inputs:    []string{dir + "/review-2.json"},
			mustAllow: cmdReport,
		},
		{
			name:      "reported",
			st:        run.State{Step: run.StepReported, Snapshots: snaps, Reviews: reviews},
			cmd:       "plait review export",
			inputs:    []string{dir + "/review-2.json"},
			mustAllow: cmdExport,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.st.ID = id
			in, err := run.Next(&tt.st)
			if err != nil {
				t.Fatal(err)
			}
			if in.Run != id || in.Step != tt.st.Step || in.Do == "" {
				t.Errorf("instruction = %+v", in)
			}
			if in.Output.Cmd != tt.cmd || in.Output.Schema != tt.schema {
				t.Errorf("output = %+v, want cmd %q schema %q", in.Output, tt.cmd, tt.schema)
			}
			if !slices.Equal(in.Inputs, tt.inputs) {
				t.Errorf("inputs = %q, want %q", in.Inputs, tt.inputs)
			}
			checkAllowed(t, in.Allowed, []string{tt.mustAllow, "next"}, tt.forbidCmds)
		})
	}
}

func TestNextBrokenState(t *testing.T) {
	for name, st := range map[string]run.State{
		"unknown step":          {ID: "r", Step: "bogus"},
		"exported, no snapshot": {ID: "r", Step: run.StepExported},
		"imported, no review":   {ID: "r", Step: run.StepImported},
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
