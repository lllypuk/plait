package task_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lllypuk/plait/internal/output"
	"github.com/lllypuk/plait/internal/run"
	"github.com/lllypuk/plait/internal/task"
)

const withCriteria = `# Retry failed orders

Orders that fail on payment are retried.

` + "```sh\n# not a heading\n```" + `

## Acceptance criteria

- a failed order is retried 3 times
- the last error is logged

## Notes

Keep the queue.
`

const wantWithCriteria = `# PROJ-7: Retry failed orders
<!-- plait:source jira -->

Orders that fail on payment are retried.

` + "```sh\n# not a heading\n```" + `

## Notes

Keep the queue.

## Критерии приёмки

- a failed order is retried 3 times
- the last error is logged
`

const withoutCriteria = "Retry failed orders\r\n\r\nOrders that fail on payment are retried.\r\n\r\n## Критерии приёмки\r\n\r\n"

const wantWithoutCriteria = `# Retry failed orders
<!-- plait:source local -->

Orders that fail on payment are retried.

## Критерии приёмки

<!-- plait:gap acceptance_criteria -->
Не заданы.
`

const (
	key   = "PROJ-7"
	local = task.DefaultSource
	jira  = "jira"
)

func TestNormalize(t *testing.T) {
	tests := []struct {
		name     string
		in       task.Input
		want     string
		criteria bool
	}{
		{
			name:     "criteria",
			in:       task.Input{Text: []byte(withCriteria), Key: key, Source: jira},
			want:     wantWithCriteria,
			criteria: true,
		},
		{
			name: "no criteria",
			in:   task.Input{Text: []byte(withoutCriteria), Source: local},
			want: wantWithoutCriteria,
		},
		{
			name: "only title",
			in:   task.Input{Text: []byte("# Retry\n"), Source: "agent"},
			want: "# Retry\n<!-- plait:source agent -->\n\n## Критерии приёмки\n\n" +
				"<!-- plait:gap acceptance_criteria -->\nНе заданы.\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			doc, err := task.Normalize(tt.in)
			if err != nil {
				t.Fatal(err)
			}
			if string(doc.Markdown) != tt.want {
				t.Errorf("task.md:\n%s\nwant:\n%s", doc.Markdown, tt.want)
			}
			if doc.HasCriteria != tt.criteria {
				t.Errorf("HasCriteria = %v, want %v", doc.HasCriteria, tt.criteria)
			}

			again, err := task.Normalize(task.Input{Text: doc.Markdown, Key: tt.in.Key, Source: tt.in.Source})
			if err != nil {
				t.Fatal(err)
			}
			if string(again.Markdown) != string(doc.Markdown) || again.HasCriteria != doc.HasCriteria {
				t.Errorf("normalizing task.md again changed it:\n%s", again.Markdown)
			}
		})
	}
}

func TestNormalizeInvalid(t *testing.T) {
	tests := []struct {
		name string
		in   task.Input
		code string
	}{
		{"empty", task.Input{Text: []byte(" \n\t\n"), Source: local}, "empty_task"},
		{"only markers", task.Input{Text: []byte("<!-- plait:source local -->\n"), Source: local}, "empty_task"},
		{"no title", task.Input{Text: []byte("## Acceptance criteria\n- x\n"), Source: local}, "no_title"},
		{"bad source", task.Input{Text: []byte("# T\n"), Source: "mail"}, "invalid_source"},
		{"bad key", task.Input{Text: []byte("# T\n"), Key: "proj7", Source: local}, "invalid_key"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := task.Normalize(tt.in)
			wantInvalid(t, err, tt.code)
		})
	}
}

func TestSave(t *testing.T) {
	s := run.Open(t.TempDir())
	st, _, err := s.New("order-retry", key)
	if err != nil {
		t.Fatal(err)
	}
	in := task.Input{Text: []byte(withCriteria), Key: key, Source: jira}

	res, err := task.Save(s, in)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Changed || !res.HasCriteria || res.Step != run.StepTaskSaved {
		t.Errorf("result = %+v", res)
	}
	path := filepath.Join(s.RunDir(st.ID), "task.md")
	b, err := os.ReadFile(path)
	if err != nil || string(b) != wantWithCriteria {
		t.Fatalf("task.md = %q, err = %v", b, err)
	}
	cur, err := s.Current()
	if err != nil {
		t.Fatal(err)
	}
	want := run.Task{Source: jira, Key: key, HasCriteria: true}
	if cur.Task == nil || *cur.Task != want || cur.Step != run.StepTaskSaved {
		t.Errorf("state task = %+v, step = %s", cur.Task, cur.Step)
	}

	stateBefore := readFile(t, filepath.Join(s.RunDir(st.ID), "state.json"))
	res, err = task.Save(s, in)
	if err != nil || res.Changed {
		t.Fatalf("repeat: result = %+v, err = %v", res, err)
	}
	if got := readFile(t, filepath.Join(s.RunDir(st.ID), "state.json")); got != stateBefore {
		t.Errorf("repeat changed state.json:\n%s", got)
	}
}

func TestSaveAfterExportReturnsToTaskSaved(t *testing.T) {
	s := run.Open(t.TempDir())
	if _, _, err := s.New("order-retry", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := task.Save(s, task.Input{Text: []byte(withCriteria), Source: local}); err != nil {
		t.Fatal(err)
	}
	st, err := s.Current()
	if err != nil {
		t.Fatal(err)
	}
	st.Step = run.StepExported
	st.Snapshots = []run.Snapshot{{N: 1}}
	if err = s.Save(st); err != nil {
		t.Fatal(err)
	}

	res, err := task.Save(s, task.Input{Text: []byte(withCriteria), Source: local})
	if err != nil || res.Changed || res.Step != run.StepExported {
		t.Fatalf("same task after export: result = %+v, err = %v", res, err)
	}
	res, err = task.Save(s, task.Input{Text: []byte(withoutCriteria), Source: local})
	if err != nil || !res.Changed || res.Step != run.StepTaskSaved || res.HasCriteria {
		t.Fatalf("new task after export: result = %+v, err = %v", res, err)
	}
	if !strings.Contains(readFile(t, filepath.Join(s.RunDir(st.ID), "task.md")), "plait:gap acceptance_criteria") {
		t.Error("task.md lacks the criteria gap")
	}
}

func TestSaveWithoutRun(t *testing.T) {
	_, err := task.Save(run.Open(t.TempDir()), task.Input{Text: []byte("# T\n"), Source: local})
	wantInvalid(t, err, "no_current_run")
}

func wantInvalid(t *testing.T, err error, code string) {
	t.Helper()
	e, ok := errors.AsType[*output.Error](err)
	if !ok || e.Kind != output.KindInvalid || e.Code != code {
		t.Fatalf("err = %#v, want Invalid %q", err, code)
	}
	if e.Fix == "" {
		t.Errorf("error %q has no fix", code)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
