package cli_test

import (
	"bytes"
	"io"
	"strings"
	"testing"

	"github.com/lllypuk/plait/internal/cli"
)

func TestMRDescribeInputErrors(t *testing.T) {
	dir := flowRepo(t)
	describe := func(summary string) []string { return []string{"mr", "describe", "--summary-file", summary} }
	for _, tt := range []struct {
		name  string
		args  []string
		stdin io.Reader
		want  string
	}{
		{"missing file", describe("none.md"), nil, `"code":"summary_not_found"`},
		{"no stdin", describe("-"), nil, `"code":"no_summary_input"`},
		{"oversized stdin", describe("-"), endless(64<<10 + 1),
			`"code":"summary_too_large"`},
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
