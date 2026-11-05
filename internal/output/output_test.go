package output_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/lllypuk/plait/internal/output"
)

func TestSelectMode(t *testing.T) {
	tests := []struct {
		name      string
		forceJSON bool
		tty       bool
		want      output.Mode
	}{
		{name: "tty", tty: true, want: output.Text},
		{name: "not tty", tty: false, want: output.JSON},
		{name: "tty with --json", forceJSON: true, tty: true, want: output.JSON},
		{name: "not tty with --json", forceJSON: true, tty: false, want: output.JSON},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := output.SelectMode(tt.forceJSON, tt.tty); got != tt.want {
				t.Errorf("SelectMode(%v, %v) = %v, want %v", tt.forceJSON, tt.tty, got, tt.want)
			}
		})
	}
}

func TestIsTerminal(t *testing.T) {
	f, err := os.Create(filepath.Join(t.TempDir(), "out"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if output.IsTerminal(f) {
		t.Error("regular file reported as terminal")
	}
	if output.IsTerminal(&bytes.Buffer{}) {
		t.Error("buffer reported as terminal")
	}
}

func TestExitCode(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want int
	}{
		{name: "nil", err: nil, want: 0},
		{name: "invalid", err: output.Invalid("bad_input", "m", "f"), want: 2},
		{name: "forbidden", err: output.Forbidden("policy", "m", "f"), want: 3},
		{name: "needs human", err: output.NeedsHuman("gate", "m", "f"), want: 4},
		{name: "internal", err: output.Internal(errors.New("boom")), want: 1},
		{name: "plain error", err: errors.New("boom"), want: 1},
		{name: "wrapped invalid", err: fmt.Errorf("ctx: %w", output.Invalid("bad_input", "m", "f")), want: 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := output.ExitCode(tt.err); got != tt.want {
				t.Errorf("ExitCode = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestInternalUnwraps(t *testing.T) {
	cause := errors.New("disk full")
	if err := output.Internal(cause); !errors.Is(err, cause) {
		t.Error("Internal does not unwrap to its cause")
	}
}

func TestSuccess(t *testing.T) {
	data := map[string]string{"cmd": "plait run new <slug>"}
	tests := []struct {
		name string
		mode output.Mode
		want string
	}{
		{name: "json", mode: output.JSON, want: `{"ok":true,"data":{"cmd":"plait run new <slug>"}}` + "\n"},
		{name: "text", mode: output.Text, want: "human text\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var out, errOut bytes.Buffer
			p := output.Printer{Out: &out, Err: &errOut, Mode: tt.mode}
			if err := p.Success(data, "human text"); err != nil {
				t.Fatal(err)
			}
			if out.String() != tt.want {
				t.Errorf("stdout = %q, want %q", out.String(), tt.want)
			}
			if errOut.Len() != 0 {
				t.Errorf("stderr = %q, want empty", errOut.String())
			}
		})
	}
}

func TestFailJSON(t *testing.T) {
	var out, errOut bytes.Buffer
	p := output.Printer{Out: &out, Err: &errOut, Mode: output.JSON}
	code := p.Fail(output.Invalid("no_run", "no current run", "plait run new <slug>"))
	if code != 2 {
		t.Errorf("exit code = %d, want 2", code)
	}
	var env struct {
		OK    bool `json:"ok"`
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
			Fix     string `json:"fix"`
		} `json:"error"`
	}
	if err := json.Unmarshal(out.Bytes(), &env); err != nil {
		t.Fatalf("stdout is not JSON: %v: %q", err, out.String())
	}
	if env.OK || env.Error.Code != "no_run" || env.Error.Message != "no current run" ||
		env.Error.Fix != "plait run new <slug>" {
		t.Errorf("envelope = %+v", env)
	}
	if errOut.Len() != 0 {
		t.Errorf("stderr = %q, want empty", errOut.String())
	}
}

func TestFailText(t *testing.T) {
	var out, errOut bytes.Buffer
	p := output.Printer{Out: &out, Err: &errOut, Mode: output.Text}
	code := p.Fail(output.Forbidden("autonomy", "merge is not allowed", "ask a human to merge"))
	if code != 3 {
		t.Errorf("exit code = %d, want 3", code)
	}
	if out.Len() != 0 {
		t.Errorf("stdout = %q, want empty", out.String())
	}
	want := "error: merge is not allowed\nfix: ask a human to merge\n"
	if errOut.String() != want {
		t.Errorf("stderr = %q, want %q", errOut.String(), want)
	}
}

func TestFailPlainErrorIsInternal(t *testing.T) {
	var out bytes.Buffer
	p := output.Printer{Out: &out, Err: &bytes.Buffer{}, Mode: output.JSON}
	if code := p.Fail(errors.New("boom")); code != 1 {
		t.Errorf("exit code = %d, want 1", code)
	}
	var env struct {
		Error struct {
			Code string `json:"code"`
			Fix  string `json:"fix"`
		} `json:"error"`
	}
	if err := json.Unmarshal(out.Bytes(), &env); err != nil {
		t.Fatal(err)
	}
	if env.Error.Code != "internal" || env.Error.Fix == "" {
		t.Errorf("error = %+v, want code internal with fix", env.Error)
	}
}
