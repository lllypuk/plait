package cli_test

import (
	"bytes"
	"io"
	"strings"
	"testing"

	"github.com/lllypuk/plait/internal/cli"
)

func TestCheckSaveInputErrors(t *testing.T) {
	dir := flowRepo(t)
	for _, tt := range []struct {
		name string
		args []string
		want string
	}{
		{"no exit code", []string{"check", "save", "test"}, `"code":"no_exit_code"`},
		{"no stdin, no file", []string{"check", "save", "test", "--exit-code", "0"}, `"code":"no_check_input"`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var out bytes.Buffer
			code := cli.Run(cli.Options{
				Args: tt.args, Dir: dir, Stdout: &out, Stderr: io.Discard,
				IsTerminal: func(io.Writer) bool { return false },
			})
			if code != 2 || !strings.Contains(out.String(), tt.want) || !strings.Contains(out.String(), `"fix":"`) {
				t.Errorf("code = %d, out = %s", code, out.String())
			}
		})
	}
}
