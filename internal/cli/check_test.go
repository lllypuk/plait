package cli_test

import (
	"bytes"
	"io"
	"slices"
	"strings"
	"testing"

	"github.com/lllypuk/plait/internal/cli"
)

func TestCheckSaveInputErrors(t *testing.T) {
	dir := flowRepo(t)
	save := []string{"check", "save", "test", "--exit-code", "0"}
	for _, tt := range []struct {
		name  string
		args  []string
		stdin io.Reader
		want  string
	}{
		{"no exit code", save[:3], nil, `"code":"no_exit_code"`},
		{"no stdin, no file", save, nil, `"code":"no_check_input"`},
		{"missing file", slices.Concat(save, []string{"--file", "none.log"}), nil,
			`"code":"check_file_not_found"`},
		{"oversized stdin", save, endless(64<<20 + 1),
			`"code":"check_output_too_large"`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var out bytes.Buffer
			code := cli.Run(cli.Options{
				Args: tt.args, Dir: dir, Stdin: tt.stdin, Stdout: &out, Stderr: io.Discard,
				IsTerminal: func(io.Writer) bool { return false },
			})
			if code != 2 || !strings.Contains(out.String(), tt.want) || !strings.Contains(out.String(), `"fix":"`) {
				t.Errorf("code = %d, out = %s", code, out.String())
			}
		})
	}
}

// endless returns a reader of n bytes without holding them in memory.
func endless(n int64) io.Reader {
	return io.LimitReader(zeros{}, n)
}

type zeros struct{}

func (zeros) Read(p []byte) (int, error) {
	clear(p)
	return len(p), nil
}
